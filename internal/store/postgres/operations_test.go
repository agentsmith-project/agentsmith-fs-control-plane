package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsContracts(t *testing.T) {
	var _ store.OperationReader = (*Store)(nil)
	var _ store.OperationWriter = (*Store)(nil)
	var _ store.OperationLeaseStore = (*Store)(nil)
	var _ store.IdempotencyStore = (*Store)(nil)
	var _ store.AuditSink = (*Store)(nil)

	if got := New(nil); got == nil {
		t.Fatal("New returned nil")
	}
}

func TestCreateOperationBuildsFullInsertWithSanitizedJSON(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	record := operationFixture(createdAt)
	record.ExternalResourceIDs = map[string]string{"jvs_repo_id": "jvs-secret-id"}
	record.InputSummary = map[string]any{
		"safe":    "visible",
		"command": "create --token input-secret-token",
	}
	record.JVSJSONOutput = map[string]any{"token": "output-secret-token"}
	record.Error = &operations.OperationError{
		Code:          "FAILED",
		Message:       "boom token=error-secret-token",
		Retryable:     true,
		CorrelationID: "corr-alpha",
		OperationID:   "op-alpha",
		Details:       map[string]any{"authorization": "Bearer error-detail-token"},
	}

	if err := st.CreateOperation(context.Background(), record.SanitizedForPersistence()); err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}

	if !strings.Contains(exec.query, "INSERT INTO operations") {
		t.Fatalf("query = %s, want operation insert", exec.query)
	}
	assertSQLContainsInOrder(t, exec.query,
		"operation_id", "operation_type", "operation_state", "phase", "attempt",
		"lease_owner", "lease_expires_at", "idempotency_scope", "idempotency_key", "request_hash",
		"correlation_id", "caller_service", "authorized_actor_type", "authorized_actor_id",
		"resource_type", "resource_id", "namespace_id", "repo_id", "template_id", "export_id",
		"mount_binding_id", "session_fence_id", "external_resource_ids", "input_summary",
		"jvs_json_output", "verification_result", "compensation_status", "error_json",
		"created_at", "started_at", "finished_at", "updated_at",
	)
	if len(exec.args) != len(operationColumns) {
		t.Fatalf("arg count = %d, want %d: %#v", len(exec.args), len(operationColumns), exec.args)
	}
	wantPrefix := []any{
		"op-alpha",
		string(operations.OperationRepoCreate),
		string(operations.OperationStateRunning),
		"write_repo",
		2,
		"worker-a",
		*record.LeaseExpiresAt,
		record.IdempotencyScope,
		"idem-alpha",
		"sha256:alpha",
		"corr-alpha",
		"afscp-api",
		"system",
		"svc-alpha",
		"repo",
		"repo-alpha",
		"",
		"repo-alpha",
	}
	for idx, want := range wantPrefix {
		if !reflect.DeepEqual(exec.args[idx], want) {
			t.Fatalf("arg %d = %#v, want %#v", idx+1, exec.args[idx], want)
		}
	}
	if exec.args[31] != createdAt {
		t.Fatalf("updated_at arg = %#v, want created_at", exec.args[31])
	}

	renderedArgs := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"jvs-secret-id", "input-secret-token", "output-secret-token", "error-secret-token", "error-detail-token"} {
		if strings.Contains(renderedArgs, forbidden) {
			t.Fatalf("CreateOperation args leaked %q in %s", forbidden, renderedArgs)
		}
	}
	if got := mustJSONMap(t, exec.args[22])["jvs_repo_id"]; got != observability.Redacted {
		t.Fatalf("external_resource_ids jvs_repo_id = %#v, want redacted", got)
	}
	if got := mustJSONMap(t, exec.args[23])["safe"]; got != "visible" {
		t.Fatalf("input_summary safe = %#v, want visible", got)
	}
}

func TestCreateOperationMapsNilMapsToJSONObjectAndEmptyNamespaceToEmptyString(t *testing.T) {
	exec := &fakeExecutor{rowsAffected: 1}
	st := &Store{exec: exec}
	record := operationFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))
	record.ExternalResourceIDs = nil
	record.InputSummary = nil
	record.NamespaceID = ""

	if err := st.CreateOperation(context.Background(), record.SanitizedForPersistence()); err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}

	if got := exec.args[16]; got != "" {
		t.Fatalf("namespace_id arg = %#v, want empty string", got)
	}
	if got := string(exec.args[22].([]byte)); got != "{}" {
		t.Fatalf("external_resource_ids json = %s, want {}", got)
	}
	if got := string(exec.args[23].([]byte)); got != "{}" {
		t.Fatalf("input_summary json = %s, want {}", got)
	}
}

func TestUpdateOperationOnlyUpdatesMutableColumns(t *testing.T) {
	updatedAt := time.Date(2026, 5, 4, 12, 45, 0, 0, time.UTC)
	exec := &fakeExecutor{rowsAffected: 1}
	st := &Store{exec: exec, clock: func() time.Time { return updatedAt }}
	record := operationFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))
	record.State = operations.OperationStateSucceeded
	record.Phase = "done"
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	record.JVSJSONOutput = map[string]any{"path": "/safe/output"}
	record.CompensationStatus = "none"
	record.FinishedAt = ptrTime(time.Date(2026, 5, 4, 12, 44, 0, 0, time.UTC))

	if err := st.UpdateOperation(context.Background(), record.SanitizedForPersistence()); err != nil {
		t.Fatalf("UpdateOperation: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"operation_state = $1", "phase = $2", "attempt = $3", "lease_owner = $4", "lease_expires_at = $5",
		"external_resource_ids = $6", "input_summary = $7", "jvs_json_output = $8", "verification_result = $9",
		"compensation_status = $10", "error_json = $11", "started_at = $12", "finished_at = $13", "updated_at = $14",
		"WHERE operation_id = $15",
	)
	updateSetClause := strings.Split(exec.query, " WHERE ")[0]
	for _, immutable := range []string{
		"idempotency_scope =",
		"idempotency_key =",
		"request_hash =",
		"caller_service =",
		"namespace_id =",
		"operation_type =",
		"operation_id =",
		"repo_id =",
		"template_id =",
		"export_id =",
		"mount_binding_id =",
		"session_fence_id =",
		"resource_type =",
		"resource_id =",
	} {
		if strings.Contains(updateSetClause, immutable) {
			t.Fatalf("UpdateOperation query updates immutable field %q: %s", immutable, exec.query)
		}
	}
	if exec.args[3] != nil || exec.args[4] != nil {
		t.Fatalf("lease args = %#v/%#v, want nil/nil", exec.args[3], exec.args[4])
	}
	if exec.args[13] != updatedAt {
		t.Fatalf("updated_at arg = %#v, want %#v", exec.args[13], updatedAt)
	}
	if exec.args[14] != "op-alpha" {
		t.Fatalf("where operation_id arg = %#v, want op-alpha", exec.args[14])
	}
}

func TestUpdateOperationReturnsNoRowsWhenOperationDoesNotExist(t *testing.T) {
	exec := &fakeExecutor{rowsAffected: 0}
	st := &Store{exec: exec, clock: func() time.Time {
		return time.Date(2026, 5, 4, 12, 45, 0, 0, time.UTC)
	}}
	record := operationFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))

	err := st.UpdateOperation(context.Background(), record.SanitizedForPersistence())
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UpdateOperation error = %v, want sql.ErrNoRows", err)
	}
}

func TestGetOperationScansFullRecord(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	startedAt := createdAt.Add(time.Minute)
	finishedAt := createdAt.Add(10 * time.Minute)
	leaseExpiresAt := createdAt.Add(5 * time.Minute)
	exec := &fakeExecutor{
		row: fakeRow{
			values: []any{
				"op-alpha", "repo_create", "succeeded", "done", 3,
				"worker-a", leaseExpiresAt, "afscp-api::repo_create:idem-alpha", "idem-alpha", "sha256:alpha",
				"corr-alpha", "afscp-api", "system", "svc-alpha", "repo", "repo-alpha",
				"", "repo-alpha", nil, "export-alpha", nil, nil,
				[]byte(`{"jvs_repo_id":"[REDACTED]"}`), []byte(`{"safe":"visible"}`),
				[]byte(`{"result":"ok"}`), []byte(`{"verified":true}`), "none",
				[]byte(`{"code":"FAILED","message":"[REDACTED]","retryable":true,"correlation_id":"corr-alpha","operation_id":"op-alpha","details":{"authorization":"[REDACTED]"}}`),
				createdAt, startedAt, finishedAt,
			},
		},
	}
	st := &Store{exec: exec}

	got, err := st.GetOperation(context.Background(), "op-alpha")
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}

	if got.ID != "op-alpha" || got.NamespaceID != "" || got.RepoID != "repo-alpha" || got.ExportID != "export-alpha" {
		t.Fatalf("scanned ids = %#v", got)
	}
	if got.LeaseOwner != "worker-a" || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("lease = %q/%v, want worker-a/%v", got.LeaseOwner, got.LeaseExpiresAt, leaseExpiresAt)
	}
	if got.ExternalResourceIDs["jvs_repo_id"] != observability.Redacted {
		t.Fatalf("external ids = %#v", got.ExternalResourceIDs)
	}
	if got.Error == nil || got.Error.Details["authorization"] != observability.Redacted {
		t.Fatalf("error = %#v, want sanitized details", got.Error)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(startedAt) || got.FinishedAt == nil || !got.FinishedAt.Equal(finishedAt) {
		t.Fatalf("times = started %v finished %v", got.StartedAt, got.FinishedAt)
	}
	if !strings.Contains(exec.query, "SELECT") || !strings.Contains(exec.query, "FROM operations") {
		t.Fatalf("query = %s, want select from operations", exec.query)
	}
}

func TestGetOperationReturnsSQLNoRows(t *testing.T) {
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.GetOperation(context.Background(), "missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetOperation error = %v, want sql.ErrNoRows", err)
	}
}

func TestCreateOrReuseOperationUsesAtomicBoundaryAndClassifiesRows(t *testing.T) {
	spec := queuedSpecFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))

	tests := []struct {
		name    string
		row     fakeRow
		wantNew bool
		wantErr bool
	}{
		{
			name: "inserted",
			row:  fakeRow{values: append(operationRowValues(operationFixture(spec.CreatedAt)), true)},
		},
		{
			name:    "existing same hash reused",
			row:     fakeRow{values: append(operationRowValues(operationFixture(spec.CreatedAt)), false)},
			wantNew: true,
		},
		{
			name: "existing different hash conflicts",
			row: func() fakeRow {
				record := operationFixture(spec.CreatedAt)
				record.RequestHash = "sha256:different"
				return fakeRow{values: append(operationRowValues(record), false)}
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: tt.row}
			st := &Store{exec: exec}

			got, err := st.CreateOrReuseOperation(context.Background(), spec)
			if tt.wantErr {
				if !errors.Is(err, operations.ErrIdempotencyConflict) {
					t.Fatalf("error = %v, want ErrIdempotencyConflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateOrReuseOperation: %v", err)
			}
			if !strings.Contains(exec.query, "ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key)") {
				t.Fatalf("query = %s, want idempotency boundary conflict clause", exec.query)
			}
			assertSQLContainsInOrder(t, exec.query,
				"INSERT INTO operations",
				"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id",
				"RETURNING",
				"(xmax = 0) AS inserted",
			)
			for _, forbidden := range []string{"DO NOTHING", "UNION ALL", "NOT EXISTS"} {
				if strings.Contains(exec.query, forbidden) {
					t.Fatalf("query = %s, must not use visibility-prone %q shape", exec.query, forbidden)
				}
			}
			if got.Existing != tt.wantNew || got.Reused != tt.wantNew {
				t.Fatalf("resolution Existing/Reused = %v/%v, want %v/%v", got.Existing, got.Reused, tt.wantNew, tt.wantNew)
			}
		})
	}
}

func TestCreateOrReuseOperationArgsAreSanitized(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := queuedSpecFixture(createdAt)
	spec.ExternalResourceIDs = map[string]string{"jvs_repo_id": "jvs-create-reuse-secret"}
	spec.InputSummary = map[string]any{
		"command": "repo create --token create-reuse-token-secret",
		"safe":    "visible",
	}
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), true)}}
	st := &Store{exec: exec}

	if _, err := st.CreateOrReuseOperation(context.Background(), spec); err != nil {
		t.Fatalf("CreateOrReuseOperation: %v", err)
	}

	renderedArgs := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"jvs-create-reuse-secret", "create-reuse-token-secret"} {
		if strings.Contains(renderedArgs, forbidden) {
			t.Fatalf("CreateOrReuseOperation args leaked %q in %s", forbidden, renderedArgs)
		}
	}
	if got := mustJSONMap(t, exec.args[22])["jvs_repo_id"]; got != observability.Redacted {
		t.Fatalf("external_resource_ids jvs_repo_id = %#v, want redacted", got)
	}
	if got := mustJSONMap(t, exec.args[23])["safe"]; got != "visible" {
		t.Fatalf("input_summary safe = %#v, want visible", got)
	}
}

func TestAcquireOperationLeaseUsesAtomicConditionalUpdateReturningRecord(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	startedAt := now
	record := operationFixture(now.Add(-time.Hour))
	record.State = operations.OperationStateRunning
	record.Attempt = 1
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &leaseExpiresAt
	record.StartedAt = &startedAt
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.AcquireOperationLease(context.Background(), "op-alpha", operations.LeaseRequest{
		Owner:    " worker-a ",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("AcquireOperationLease: %v", err)
	}

	if got.LeaseOwner != "worker-a" || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("lease = %q/%v, want worker-a/%v", got.LeaseOwner, got.LeaseExpiresAt, leaseExpiresAt)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations",
		"operation_state = CASE",
		"attempt = CASE",
		"attempt + 1",
		"lease_owner = CASE",
		"lease_expires_at = CASE",
		"started_at = CASE",
		"COALESCE(started_at, $4)",
		"finished_at = CASE",
		"updated_at = $4",
		"WHERE operation_id = $1",
		"(operation_state = 'queued' AND lease_owner IS NULL AND lease_expires_at IS NULL)",
		"(operation_state = 'running'",
		"lease_expires_at <= $4",
		"(operation_state = 'operator_intervention_required' AND $5 = 'explicit_recovery_action'",
		"(operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation'",
		"RETURNING",
	)
	if strings.Contains(exec.query, "SELECT ") {
		t.Fatalf("AcquireOperationLease query must be a single UPDATE RETURNING, got %s", exec.query)
	}
	wantArgs := []any{
		"op-alpha",
		"worker-a",
		leaseExpiresAt,
		now,
		string(operations.LeaseRecoveryNone),
		string(operations.LeaseCancelPolicyNone),
	}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
}

func TestAcquireOperationLeasePassesExplicitRecoveryAndFinalizePolicies(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	t.Run("explicit recovery", func(t *testing.T) {
		leaseExpiresAt := now.Add(30 * time.Minute)
		record := operationFixture(now.Add(-time.Hour))
		record.State = operations.OperationStateRunning
		record.Attempt = 4
		record.LeaseOwner = "recovery-worker"
		record.LeaseExpiresAt = &leaseExpiresAt
		exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
		st := &Store{exec: exec}

		got, err := st.AcquireOperationLease(context.Background(), "op-alpha", operations.LeaseRequest{
			Owner:        "recovery-worker",
			Duration:     30 * time.Minute,
			Now:          now,
			RecoveryMode: operations.LeaseRecoveryExplicitAction,
		})
		if err != nil {
			t.Fatalf("AcquireOperationLease recovery: %v", err)
		}
		if got.State != operations.OperationStateRunning || got.LeaseOwner != "recovery-worker" {
			t.Fatalf("recovered operation = %#v, want running recovery-worker", got)
		}
		if exec.args[4] != string(operations.LeaseRecoveryExplicitAction) {
			t.Fatalf("recovery mode arg = %#v, want explicit recovery", exec.args[4])
		}
	})

	t.Run("finalize cancellation", func(t *testing.T) {
		finishedAt := now
		returned := operationFixture(now.Add(-time.Hour))
		returned.State = operations.OperationStateCancelled
		returned.Attempt = 3
		returned.LeaseOwner = ""
		returned.LeaseExpiresAt = nil
		returned.FinishedAt = &finishedAt
		exec := &fakeExecutor{row: fakeRow{values: operationRowValues(returned)}}
		st := &Store{exec: exec}

		got, err := st.AcquireOperationLease(context.Background(), "op-alpha", operations.LeaseRequest{
			Owner:        "canceller",
			Duration:     30 * time.Minute,
			Now:          now,
			CancelPolicy: operations.LeaseCancelPolicyFinalize,
		})
		if err != nil {
			t.Fatalf("AcquireOperationLease finalize cancellation: %v", err)
		}
		if got.State != operations.OperationStateCancelled || got.Attempt != 3 || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
			t.Fatalf("finalized operation = %#v, want cancelled without lease and unchanged attempt", got)
		}
		if exec.args[5] != string(operations.LeaseCancelPolicyFinalize) {
			t.Fatalf("cancel policy arg = %#v, want finalize cancellation", exec.args[5])
		}
		assertSQLContainsInOrder(t, exec.query,
			"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN 'cancelled'",
			"(operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation'",
		)
	})
}

func TestAcquireOperationLeaseNoRowsWrapsLeaseUnavailableAndSQLNoRows(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.AcquireOperationLease(context.Background(), "op-alpha", operations.LeaseRequest{
		Owner:    "worker-a",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("AcquireOperationLease error = %v, want ErrLeaseUnavailable and sql.ErrNoRows", err)
	}
}

func TestRenewOperationLeaseExtendsLiveOwnerLeaseAtomically(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	existingLeaseExpiresAt := now.Add(45 * time.Minute)
	record := operationFixture(now.Add(-time.Hour))
	record.State = operations.OperationStateRunning
	record.Attempt = 3
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &existingLeaseExpiresAt
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.RenewOperationLease(context.Background(), "op-alpha", operations.LeaseRequest{
		Owner:    "worker-a",
		Duration: 5 * time.Minute,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("RenewOperationLease: %v", err)
	}

	if got.Attempt != 3 {
		t.Fatalf("renew attempt = %d, want unchanged 3", got.Attempt)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations",
		"lease_expires_at = GREATEST(lease_expires_at, $3)",
		"updated_at = $4",
		"WHERE operation_id = $1",
		"operation_state = 'running'",
		"lease_owner = $2",
		"lease_expires_at > $4",
		"RETURNING",
	)
	updateSetClause := strings.Split(exec.query, " WHERE ")[0]
	if strings.Contains(updateSetClause, "attempt") || strings.Contains(updateSetClause, "operation_state =") {
		t.Fatalf("RenewOperationLease query must not change attempt or state: %s", exec.query)
	}
	wantArgs := []any{"op-alpha", "worker-a", now.Add(5 * time.Minute), now}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
}

func TestUpdateOperationWithLeaseUsesAtomicFenceAndPreservesRunningLease(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	existingLeaseExpiresAt := now.Add(30 * time.Minute)
	attemptedLeaseExtension := now.Add(2 * time.Hour)
	returned := operationFixture(now.Add(-time.Hour))
	returned.Phase = "verify_result"
	returned.Attempt = 2
	returned.LeaseOwner = "worker-a"
	returned.LeaseExpiresAt = &existingLeaseExpiresAt
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(returned)}}
	st := &Store{exec: exec}

	update := returned
	update.LeaseExpiresAt = &attemptedLeaseExtension
	got, err := st.UpdateOperationWithLease(context.Background(), update.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("UpdateOperationWithLease: %v", err)
	}

	if got.LeaseOwner != "worker-a" || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(existingLeaseExpiresAt) {
		t.Fatalf("running update lease = %q/%v, want preserved worker-a/%v", got.LeaseOwner, got.LeaseExpiresAt, existingLeaseExpiresAt)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations",
		"operation_state = $1",
		"phase = $2",
		"lease_owner = CASE WHEN $1 = 'running' THEN lease_owner ELSE NULL END",
		"lease_expires_at = CASE WHEN $1 = 'running' THEN lease_expires_at ELSE NULL END",
		"updated_at = $11",
		"WHERE operation_id = $12",
		"operation_state = 'running'",
		"lease_owner = $13",
		"lease_expires_at IS NOT NULL",
		"lease_expires_at > $11",
		"RETURNING",
	)
	updateSetClause := strings.Split(exec.query, " WHERE ")[0]
	if strings.Contains(updateSetClause, "lease_owner = $") || strings.Contains(updateSetClause, "lease_expires_at = $") {
		t.Fatalf("UpdateOperationWithLease must not accept lease fields from the update record: %s", exec.query)
	}
	if exec.args[0] != string(operations.OperationStateRunning) || exec.args[11] != "op-alpha" || exec.args[12] != "worker-a" {
		t.Fatalf("fence args = %#v, want state running, op-alpha, worker-a", exec.args)
	}
}

func TestUpdateOperationWithLeaseRejectsRecordLeaseTransferBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	record.LeaseOwner = "worker-b"
	exec := &fakeExecutor{}
	st := &Store{exec: exec}

	_, err := st.UpdateOperationWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now)
	if !errors.Is(err, operations.ErrInvalidLeaseRequest) {
		t.Fatalf("UpdateOperationWithLease error = %v, want ErrInvalidLeaseRequest", err)
	}
	if exec.query != "" {
		t.Fatalf("UpdateOperationWithLease issued SQL after lease transfer request: %s", exec.query)
	}
}

func TestUpdateOperationWithLeaseCanWriteTerminalAndClearLease(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	returned := operationFixture(now.Add(-time.Hour))
	returned.State = operations.OperationStateSucceeded
	returned.Phase = "done"
	returned.LeaseOwner = ""
	returned.LeaseExpiresAt = nil
	returned.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(returned)}}
	st := &Store{exec: exec}

	update := operationFixture(now.Add(-time.Hour))
	update.State = operations.OperationStateSucceeded
	update.Phase = "done"
	update.FinishedAt = nil
	got, err := st.UpdateOperationWithLease(context.Background(), update.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("UpdateOperationWithLease terminal: %v", err)
	}

	if got.State != operations.OperationStateSucceeded || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("terminal update = %#v, want succeeded with cleared lease", got)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(now) {
		t.Fatalf("terminal finished_at = %v, want %v", got.FinishedAt, now)
	}
	assertSQLContainsInOrder(t, exec.query,
		"lease_owner = CASE WHEN $1 = 'running' THEN lease_owner ELSE NULL END",
		"lease_expires_at = CASE WHEN $1 = 'running' THEN lease_expires_at ELSE NULL END",
		"finished_at = CASE WHEN $1 IN ('succeeded', 'failed', 'cancelled') THEN COALESCE($10, $11) ELSE NULL END",
	)
}

func TestUpdateOperationWithLeaseReturnsUnavailableWhenLeaseExpiredOrReclaimed(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.UpdateOperationWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now)
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UpdateOperationWithLease error = %v, want ErrLeaseUnavailable and sql.ErrNoRows", err)
	}
}

func TestMarshalObjectRejectsNonObjectJSON(t *testing.T) {
	for _, value := range []any{
		[]string{"not", "object"},
		"not-object",
		123,
	} {
		if _, err := marshalObject(value); err == nil {
			t.Fatalf("marshalObject(%#v) succeeded, want error", value)
		}
	}
}

func operationFixture(createdAt time.Time) operations.OperationRecord {
	leaseExpiresAt := createdAt.Add(5 * time.Minute)
	startedAt := createdAt.Add(time.Minute)
	return operations.OperationRecord{
		ID:                  "op-alpha",
		Type:                operations.OperationRepoCreate,
		State:               operations.OperationStateRunning,
		Phase:               "write_repo",
		Attempt:             2,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &leaseExpiresAt,
		IdempotencyScope:    "afscp-api::repo_create:idem-alpha",
		IdempotencyKey:      "idem-alpha",
		RequestHash:         "sha256:alpha",
		CorrelationID:       "corr-alpha",
		CallerService:       "afscp-api",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo-alpha"},
		NamespaceID:         "",
		RepoID:              "repo-alpha",
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-redacted"},
		InputSummary:        map[string]any{"safe": "visible"},
		StartedAt:           &startedAt,
		CreatedAt:           createdAt,
	}
}

func queuedSpecFixture(createdAt time.Time) operations.QueuedOperationSpec {
	return operations.QueuedOperationSpec{
		OperationID:     "op-alpha",
		Scope:           operations.NewIdempotencyScope("afscp-api", "", operations.OperationRepoCreate, "idem-alpha"),
		RequestHash:     "sha256:alpha",
		Phase:           "write_repo",
		CorrelationID:   "corr-alpha",
		CallerService:   "afscp-api",
		AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:        operations.ResourceRef{Type: "repo", ID: "repo-alpha"},
		NamespaceID:     "",
		RepoID:          "repo-alpha",
		InputSummary:    map[string]any{"safe": "visible"},
		CreatedAt:       createdAt,
	}
}

func operationRowValues(record operations.OperationRecord) []any {
	var leaseExpiresAt any
	if record.LeaseExpiresAt != nil {
		leaseExpiresAt = *record.LeaseExpiresAt
	}
	var startedAt any
	if record.StartedAt != nil {
		startedAt = *record.StartedAt
	}
	var finishedAt any
	if record.FinishedAt != nil {
		finishedAt = *record.FinishedAt
	}
	return []any{
		record.ID, string(record.Type), string(record.State), record.Phase, record.Attempt,
		nullableArgString(record.LeaseOwner), leaseExpiresAt, record.IdempotencyScope, record.IdempotencyKey, string(record.RequestHash),
		record.CorrelationID, record.CallerService, record.AuthorizedActor.Type, record.AuthorizedActor.ID, record.Resource.Type, record.Resource.ID,
		record.NamespaceID, nullableArgString(record.RepoID), nullableArgString(record.TemplateID), nullableArgString(record.ExportID), nullableArgString(record.MountBindingID), nullableArgString(record.SessionFenceID),
		mustMarshalJSONForTest(record.ExternalResourceIDs), mustMarshalJSONForTest(record.InputSummary), nil, nil, nullableArgString(record.CompensationStatus), nil,
		record.CreatedAt, startedAt, finishedAt,
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func assertSQLContainsInOrder(t *testing.T, sql string, parts ...string) {
	t.Helper()
	cursor := 0
	for _, part := range parts {
		idx := strings.Index(sql[cursor:], part)
		if idx < 0 {
			t.Fatalf("SQL %q missing %q after offset %d", sql, part, cursor)
		}
		cursor += idx + len(part)
	}
}

func renderArgs(t *testing.T, args ...any) string {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return string(encoded)
}

func mustJSONMap(t *testing.T, value any) map[string]any {
	t.Helper()
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		t.Fatalf("value %T is not json bytes/string", value)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal json %s: %v", raw, err)
	}
	return out
}

func mustMarshalJSONForTest(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func nullableArgString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type fakeExecutor struct {
	query        string
	args         []any
	row          fakeRow
	rows         fakeRows
	err          error
	rowsAffected int64
}

func (fake *fakeExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	fake.query = query
	fake.args = args
	return fakeResult{rowsAffected: fake.rowsAffected}, fake.err
}

func (fake *fakeExecutor) QueryRowContext(_ context.Context, query string, args ...any) rowScanner {
	fake.query = query
	fake.args = args
	return fake.row
}

func (fake *fakeExecutor) QueryContext(_ context.Context, query string, args ...any) (rowsScanner, error) {
	fake.query = query
	fake.args = args
	return &fake.rows, fake.err
}

type fakeRow struct {
	values []any
	err    error
}

func (row fakeRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(dest) != len(row.values) {
		return errors.New("scan destination count mismatch")
	}
	for idx := range dest {
		if err := assignScanValue(dest[idx], row.values[idx]); err != nil {
			return err
		}
	}
	return nil
}

type fakeRows struct {
	rows     []fakeRow
	index    int
	err      error
	closeErr error
	closed   bool
}

func (rows *fakeRows) Close() error {
	rows.closed = true
	return rows.closeErr
}

func (rows *fakeRows) Err() error {
	return rows.err
}

func (rows *fakeRows) Next() bool {
	if rows.index >= len(rows.rows) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeRows) Scan(dest ...any) error {
	if rows.index == 0 || rows.index > len(rows.rows) {
		return errors.New("scan called without current row")
	}
	return rows.rows[rows.index-1].Scan(dest...)
}

func assignScanValue(dest any, value any) error {
	switch ptr := dest.(type) {
	case *string:
		if value == nil {
			*ptr = ""
			return nil
		}
		*ptr = value.(string)
	case *int:
		*ptr = value.(int)
	case *int64:
		*ptr = value.(int64)
	case *time.Time:
		*ptr = value.(time.Time)
	case *sql.NullString:
		if value == nil {
			*ptr = sql.NullString{}
			return nil
		}
		*ptr = sql.NullString{String: value.(string), Valid: true}
	case *sql.NullTime:
		if value == nil {
			*ptr = sql.NullTime{}
			return nil
		}
		*ptr = sql.NullTime{Time: value.(time.Time), Valid: true}
	case **time.Time:
		if value == nil {
			*ptr = nil
			return nil
		}
		value := value.(time.Time)
		*ptr = &value
	case *[]byte:
		if value == nil {
			*ptr = nil
			return nil
		}
		*ptr = append((*ptr)[:0], value.([]byte)...)
	case *bool:
		*ptr = value.(bool)
	default:
		return errors.New("unsupported scan destination")
	}
	return nil
}

type fakeResult struct {
	rowsAffected int64
}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (result fakeResult) RowsAffected() (int64, error) {
	if result.rowsAffected == 0 {
		return 0, nil
	}
	return result.rowsAffected, nil
}
