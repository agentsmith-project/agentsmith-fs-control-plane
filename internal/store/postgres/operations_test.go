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

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
	"github.com/lib/pq"
)

func TestStoreImplementsContracts(t *testing.T) {
	var _ store.OperationReader = (*Store)(nil)
	var _ store.OperationWriter = (*Store)(nil)
	var _ store.OperationRecoveryReader = (*Store)(nil)
	var _ store.OperationLeaseStore = (*Store)(nil)
	var _ store.OperationWorkerCommitStore = (*Store)(nil)
	var _ store.RepoCreateOperationIntakeStore = (*Store)(nil)
	var _ store.RestorePreviewOperationIntakeStore = (*Store)(nil)
	var _ store.RestorePreviewDiscardOperationIntakeStore = (*Store)(nil)
	var _ store.RestoreRunOperationIntakeStore = (*Store)(nil)
	var _ store.RepoCreateOperationCommitStore = (*Store)(nil)
	var _ store.RepoCreateOperationRecoveryStore = (*Store)(nil)
	var _ store.SavePointCreateOperationRecoveryStore = (*Store)(nil)
	var _ store.RestorePreviewOperationRecoveryStore = (*Store)(nil)
	var _ store.RestorePreviewDiscardOperationRecoveryStore = (*Store)(nil)
	var _ store.RestoreRunOperationRecoveryStore = (*Store)(nil)
	var _ store.RepoJVSMutationGateReader = (*Store)(nil)
	var _ store.RestoreRunIntakeGateReader = (*Store)(nil)
	var _ store.VolumeEnsureOperationCommitStore = (*Store)(nil)
	var _ store.VolumeEnsureOperationRecoveryStore = (*Store)(nil)
	var _ store.NamespaceUpsertOperationCommitStore = (*Store)(nil)
	var _ store.NamespaceUpsertOperationRecoveryStore = (*Store)(nil)
	var _ store.NamespaceVolumeBindingOperationCommitStore = (*Store)(nil)
	var _ store.NamespaceVolumeBindingOperationRecoveryStore = (*Store)(nil)
	var _ store.IdempotencyStore = (*Store)(nil)
	var _ store.OperationIdempotencyLookupStore = (*Store)(nil)
	var _ store.AuditSink = (*Store)(nil)

	if got := New(nil); got == nil {
		t.Fatal("New returned nil")
	}
}

func TestRepoHasNonTerminalJVSMutationScopesRepoTypeAndNonTerminalState(t *testing.T) {
	exec := &fakeExecutor{row: fakeRow{values: []any{true}}}
	st := &Store{exec: exec}

	got, err := st.RepoHasNonTerminalJVSMutation(context.Background(), "repo_alpha")
	if err != nil {
		t.Fatalf("RepoHasNonTerminalJVSMutation: %v", err)
	}
	if !got {
		t.Fatal("RepoHasNonTerminalJVSMutation = false, want true")
	}
	if exec.queryRowCalls != 1 || len(exec.args) != 1 || exec.args[0] != "repo_alpha" {
		t.Fatalf("query calls/args = %d/%#v, want repo query", exec.queryRowCalls, exec.args)
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT EXISTS",
		"FROM operations",
		"repo_id = $1",
		"operation_type IN ('save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone')",
		"operation_state NOT IN ('succeeded','failed','cancelled')",
	)
	for _, forbidden := range []string{"UPDATE ", "INSERT ", "DELETE ", "FOR UPDATE", "lease_owner", "repo_fences", "restore_plans"} {
		if strings.Contains(strings.ToUpper(exec.query), strings.ToUpper(forbidden)) {
			t.Fatalf("gate query contains mutating or unrelated SQL %q: %s", forbidden, exec.query)
		}
	}
}

func TestRestoreRunExistsForPreviewOperationScopesPreviewRepoNamespaceAndBlockingStates(t *testing.T) {
	exec := &fakeExecutor{row: fakeRow{values: []any{true}}}
	st := &Store{exec: exec}

	got, err := st.RestoreRunExistsForPreviewOperation(context.Background(), "ns_alpha01", "repo_alpha01", "op_preview01")
	if err != nil {
		t.Fatalf("RestoreRunExistsForPreviewOperation: %v", err)
	}
	if !got {
		t.Fatal("RestoreRunExistsForPreviewOperation = false, want true")
	}
	if exec.queryRowCalls != 1 || len(exec.args) != 3 || exec.args[0] != "ns_alpha01" || exec.args[1] != "repo_alpha01" || exec.args[2] != "op_preview01" {
		t.Fatalf("query calls/args = %d/%#v, want namespace/repo/preview query", exec.queryRowCalls, exec.args)
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT EXISTS",
		"FROM operations",
		"operation_type = 'restore_run'",
		"namespace_id = $1",
		"repo_id = $2",
		"resource_type = 'repo'",
		"resource_id = $2",
		"input_summary->>'preview_operation_id' = $3",
		"operation_state NOT IN ('failed','cancelled')",
	)
	for _, forbidden := range []string{"UPDATE ", "INSERT ", "DELETE ", "FOR UPDATE", "repo_fences", "restore_plans", "run_command", "recommended_next_command"} {
		if strings.Contains(strings.ToUpper(exec.query), strings.ToUpper(forbidden)) {
			t.Fatalf("restore run intake gate query contains forbidden SQL/detail %q: %s", forbidden, exec.query)
		}
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

func TestGetOperationByIdempotencyScopeSelectsExactScope(t *testing.T) {
	record := operationFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))
	scope := operations.NewIdempotencyScope("afscp-api", "", operations.OperationRepoCreate, "idem-alpha")
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.GetOperationByIdempotencyScope(context.Background(), scope)
	if err != nil {
		t.Fatalf("GetOperationByIdempotencyScope: %v", err)
	}

	if got.ID != record.ID || got.RequestHash != record.RequestHash {
		t.Fatalf("operation = %#v, want %q/%q", got, record.ID, record.RequestHash)
	}
	assertSQLContainsInOrder(t, exec.query,
		"FROM operations",
		"caller_service = $1",
		"namespace_id = $2",
		"operation_type = $3",
		"idempotency_key = $4",
	)
	wantArgs := []any{"afscp-api", "", string(operations.OperationRepoCreate), "idem-alpha"}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
}

func TestListOperationsForRecoverySelectsOrderedCandidatesReadOnly(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	createdAt := now.Add(-time.Hour)
	queued := operationFixture(createdAt)
	queued.ID = "op-queued"
	queued.State = operations.OperationStateQueued
	queued.LeaseOwner = ""
	queued.LeaseExpiresAt = nil
	expired := operationFixture(createdAt.Add(time.Minute))
	expired.ID = "op-running-expired"
	expired.State = operations.OperationStateRunning
	expiredLease := now.Add(-time.Minute)
	expired.LeaseExpiresAt = &expiredLease
	cancel := operationFixture(createdAt.Add(2 * time.Minute))
	cancel.ID = "op-cancel-expired"
	cancel.State = operations.OperationStateCancelRequested
	cancel.LeaseExpiresAt = &expiredLease
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{
		{values: operationRowValues(queued)},
		{values: operationRowValues(expired)},
		{values: operationRowValues(cancel)},
	}}}
	st := &Store{exec: exec}

	got, err := st.ListOperationsForRecovery(context.Background(), now, 25)
	if err != nil {
		t.Fatalf("ListOperationsForRecovery: %v", err)
	}
	if gotIDs := operationIDsForPostgresTest(got); strings.Join(gotIDs, ",") != "op-queued,op-running-expired,op-cancel-expired" {
		t.Fatalf("candidate IDs = %#v", gotIDs)
	}
	if !exec.rows.closed {
		t.Fatal("rows were not closed")
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"FROM operations",
		"operation_state = 'queued'",
		"operation_state = 'running'",
		"(lease_owner IS NULL AND lease_expires_at IS NULL)",
		"(lease_owner IS NULL AND lease_expires_at IS NOT NULL)",
		"lease_expires_at <= $1",
		"operation_state = 'cancel_requested'",
		"operator_intervention_required",
		"ORDER BY created_at, operation_id",
		"LIMIT $2",
	)
	if strings.Contains(exec.query, "UPDATE ") || strings.Contains(exec.query, " FOR UPDATE") {
		t.Fatalf("ListOperationsForRecovery must be read-only SELECT, got %s", exec.query)
	}
	if strings.Contains(exec.query, "lease_expires_at > $1") {
		t.Fatalf("ListOperationsForRecovery must not select live running leases: %s", exec.query)
	}
	cancelBranch := exec.query[strings.Index(exec.query, "operation_state = 'cancel_requested'"):]
	if !strings.Contains(cancelBranch, "(lease_owner IS NULL AND lease_expires_at IS NOT NULL)") ||
		!strings.Contains(cancelBranch, "(lease_owner IS NOT NULL AND btrim(lease_owner) = '')") ||
		!strings.Contains(cancelBranch, "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL)") {
		t.Fatalf("cancel_requested branch must include invalid lease pair visibility: %s", exec.query)
	}
	if !reflect.DeepEqual(exec.args, []any{now, 25}) {
		t.Fatalf("args = %#v, want now and limit", exec.args)
	}
}

func TestListOperationsForRecoveryRejectsInvalidArgsBeforeSQL(t *testing.T) {
	tests := []struct {
		name  string
		now   time.Time
		limit int
	}{
		{name: "zero now", limit: 1},
		{name: "zero limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)},
		{name: "negative limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC), limit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.ListOperationsForRecovery(context.Background(), tt.now, tt.limit)
			if err == nil {
				t.Fatal("ListOperationsForRecovery succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid args: %s", exec.query)
			}
		})
	}
}

func TestListNamespaceUpsertOperationsForRecoveryScopesBeforeOrderAndLimit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-namespace"
	record.Type = operations.OperationNamespaceUpsert
	record.State = operations.OperationStateQueued
	record.Phase = operations.OperationPhaseNamespaceUpsertValidate
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{{values: operationRowValues(record)}}}}
	st := &Store{exec: exec}

	got, err := st.ListNamespaceUpsertOperationsForRecovery(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("ListNamespaceUpsertOperationsForRecovery: %v", err)
	}
	if len(got) != 1 || got[0].ID != "op-namespace" {
		t.Fatalf("records = %#v, want op-namespace", got)
	}
	if !exec.rows.closed {
		t.Fatal("rows were not closed")
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"FROM operations",
		"operation_type = 'namespace_upsert'",
		"phase = 'validate_namespace_upsert'",
		"operation_state = 'queued'",
		"operation_state = 'running'",
		"operation_state = 'cancel_requested'",
		"operator_intervention_required",
		"ORDER BY created_at, operation_id",
		"LIMIT $2",
	)
	if strings.Contains(exec.query, "UPDATE ") || strings.Contains(exec.query, " FOR UPDATE") {
		t.Fatalf("namespace upsert recovery list must be read-only SELECT, got %s", exec.query)
	}
	if !reflect.DeepEqual(exec.args, []any{now, 1}) {
		t.Fatalf("args = %#v, want now and limit", exec.args)
	}
}

func TestListNamespaceUpsertOperationsForRecoveryRejectsInvalidArgsBeforeSQL(t *testing.T) {
	tests := []struct {
		name  string
		now   time.Time
		limit int
	}{
		{name: "zero now", limit: 1},
		{name: "zero limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)},
		{name: "negative limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC), limit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.ListNamespaceUpsertOperationsForRecovery(context.Background(), tt.now, tt.limit)
			if err == nil {
				t.Fatal("ListNamespaceUpsertOperationsForRecovery succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid args: %s", exec.query)
			}
		})
	}
}

func TestListVolumeEnsureOperationsForRecoveryScopesBeforeOrderAndLimit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-volume"
	record.Type = operations.OperationVolumeEnsure
	record.State = operations.OperationStateQueued
	record.Phase = operations.OperationPhaseVolumeEnsureValidate
	record.Resource = operations.ResourceRef{Type: "volume", ID: "vol_123"}
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{{values: operationRowValues(record)}}}}
	st := &Store{exec: exec}

	got, err := st.ListVolumeEnsureOperationsForRecovery(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("ListVolumeEnsureOperationsForRecovery: %v", err)
	}
	if len(got) != 1 || got[0].ID != "op-volume" {
		t.Fatalf("records = %#v, want op-volume", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"FROM operations",
		"operation_type = 'volume_ensure'",
		"phase = 'validate_volume_ensure'",
		"namespace_id = ''",
		"operation_state = 'queued'",
		"operation_state = 'running'",
		"operation_state = 'cancel_requested'",
		"operator_intervention_required",
		"ORDER BY created_at, operation_id",
		"LIMIT $2",
	)
	if strings.Contains(exec.query, "UPDATE ") || strings.Contains(exec.query, " FOR UPDATE") {
		t.Fatalf("volume ensure recovery list must be read-only SELECT, got %s", exec.query)
	}
	if !reflect.DeepEqual(exec.args, []any{now, 1}) {
		t.Fatalf("args = %#v, want now and limit", exec.args)
	}
}

func TestListVolumeEnsureOperationsForRecoveryRejectsInvalidArgsBeforeSQL(t *testing.T) {
	tests := []struct {
		name  string
		now   time.Time
		limit int
	}{
		{name: "zero now", limit: 1},
		{name: "zero limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)},
		{name: "negative limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC), limit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.ListVolumeEnsureOperationsForRecovery(context.Background(), tt.now, tt.limit)
			if err == nil {
				t.Fatal("ListVolumeEnsureOperationsForRecovery succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid args: %s", exec.query)
			}
		})
	}
}

func TestListNamespaceVolumeBindingPutOperationsForRecoveryScopesBeforeOrderAndLimit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-binding"
	record.Type = operations.OperationNamespaceVolumeBindingPut
	record.State = operations.OperationStateQueued
	record.Phase = operations.OperationPhaseNamespaceVolumeBindingPutValidate
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace_volume_binding", ID: "ns_alpha01"}
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{{values: operationRowValues(record)}}}}
	st := &Store{exec: exec}

	got, err := st.ListNamespaceVolumeBindingPutOperationsForRecovery(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("ListNamespaceVolumeBindingPutOperationsForRecovery: %v", err)
	}
	if len(got) != 1 || got[0].ID != "op-binding" {
		t.Fatalf("records = %#v, want op-binding", got)
	}
	if !exec.rows.closed {
		t.Fatal("rows were not closed")
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"FROM operations",
		"operation_type = 'namespace_volume_binding_put'",
		"phase = 'validate_namespace_volume_binding_put'",
		"operation_state = 'queued'",
		"operation_state = 'running'",
		"operation_state = 'cancel_requested'",
		"operator_intervention_required",
		"ORDER BY created_at, operation_id",
		"LIMIT $2",
	)
	if strings.Contains(exec.query, "UPDATE ") || strings.Contains(exec.query, " FOR UPDATE") {
		t.Fatalf("binding recovery list must be read-only SELECT, got %s", exec.query)
	}
	if !reflect.DeepEqual(exec.args, []any{now, 1}) {
		t.Fatalf("args = %#v, want now and limit", exec.args)
	}
}

func TestListNamespaceVolumeBindingPutOperationsForRecoveryRejectsInvalidArgsBeforeSQL(t *testing.T) {
	tests := []struct {
		name  string
		now   time.Time
		limit int
	}{
		{name: "zero now", limit: 1},
		{name: "zero limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)},
		{name: "negative limit", now: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC), limit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.ListNamespaceVolumeBindingPutOperationsForRecovery(context.Background(), tt.now, tt.limit)
			if err == nil {
				t.Fatal("ListNamespaceVolumeBindingPutOperationsForRecovery succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid args: %s", exec.query)
			}
		})
	}
}

func TestListOperationsForRecoveryClosesRowsAndPropagatesRowsAndScanErrors(t *testing.T) {
	valid := operationFixture(time.Date(2026, 5, 5, 11, 0, 0, 0, time.UTC))
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rows fakeRows
	}{
		{name: "rows err", rows: fakeRows{rows: []fakeRow{{values: operationRowValues(valid)}}, err: errors.New("rows failed")}},
		{name: "scan err", rows: fakeRows{rows: []fakeRow{{err: errors.New("scan failed")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{rows: tt.rows}
			st := &Store{exec: exec}

			_, err := st.ListOperationsForRecovery(context.Background(), now, 10)
			if err == nil {
				t.Fatal("ListOperationsForRecovery succeeded, want error")
			}
			if !exec.rows.closed {
				t.Fatal("rows were not closed after error")
			}
		})
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

func TestCreateOrReuseOperationMapsJVSUniqueIndexViolation(t *testing.T) {
	spec := queuedSpecFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: &pq.Error{Code: "23505", Constraint: "operations_one_non_terminal_jvs_mutation_per_repo_idx"}}}}

	_, err := st.CreateOrReuseOperation(context.Background(), spec)
	if !errors.Is(err, operations.ErrRepoJVSMutationInProgress) {
		t.Fatalf("CreateOrReuseOperation error = %v, want ErrRepoJVSMutationInProgress", err)
	}
}

func TestCreateOrReuseRepoCreateOperationReusesExistingBeforeRepoExists(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := repoCreateQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), false)}}
	st := &Store{exec: exec}

	got, err := st.CreateOrReuseRepoCreateOperation(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateOrReuseRepoCreateOperation: %v", err)
	}
	if !got.Existing || !got.Reused || got.Operation.ID != spec.OperationID {
		t.Fatalf("resolution = %#v, want existing reused operation", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH existing_operation AS (",
		"FROM operations",
		"caller_service = $12",
		"namespace_id = $17",
		"operation_type = 'repo_create'",
		"idempotency_key = $9",
		"), inserted_operation AS (",
		"INSERT INTO operations",
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation)",
		"AND NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $18)",
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key)",
		"SELECT",
		"FROM existing_operation",
		"UNION ALL",
		"SELECT",
		"FROM inserted_operation",
	)
	if strings.Contains(exec.query, "SELECT "+strings.Join(repoColumns, ", ")) {
		t.Fatalf("repo create intake must not perform a separate repo read: %s", exec.query)
	}
}

func TestCreateOrReuseRepoCreateOperationNewRequestExistingRepoReturnsRepoAlreadyExists(t *testing.T) {
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.CreateOrReuseRepoCreateOperation(context.Background(), repoCreateQueuedSpecFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)))
	if !errors.Is(err, operations.ErrRepoAlreadyExists) {
		t.Fatalf("CreateOrReuseRepoCreateOperation error = %v, want ErrRepoAlreadyExists", err)
	}
}

func TestCreateOrReuseRepoCreateOperationDifferentHashReturnsIdempotencyConflictBeforeRepoExists(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := repoCreateQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	record.RequestHash = "sha256:different"
	exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), false)}}
	st := &Store{exec: exec}

	_, err = st.CreateOrReuseRepoCreateOperation(context.Background(), spec)
	if !errors.Is(err, operations.ErrIdempotencyConflict) {
		t.Fatalf("CreateOrReuseRepoCreateOperation error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestCreateOrReuseRestorePreviewOperationUsesAtomicGateAfterIdempotency(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := restorePreviewQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), true, "")}}
	st := &Store{exec: exec}

	got, err := st.CreateOrReuseRestorePreviewOperation(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateOrReuseRestorePreviewOperation: %v", err)
	}
	if got.Existing || got.Reused {
		t.Fatalf("resolution = %#v, want brand-new preview operation", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH existing_operation AS (",
		"FROM operations",
		"operation_type = 'restore_preview'",
		"idempotency_key = $9",
		"), same_repo_jvs_mutation AS (",
		"operation_type IN ('save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone')",
		"operation_state NOT IN ('succeeded','failed','cancelled')",
		"NOT EXISTS (SELECT 1 FROM existing_operation)",
		"), active_restore_plan AS (",
		"FROM restore_plans",
		"status IN ('pending', 'consuming', 'discarding', 'operator_intervention_required')",
		"NOT EXISTS (SELECT 1 FROM existing_operation)",
		"), inserted_operation AS (",
		"INSERT INTO operations",
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation)",
		"AND NOT EXISTS (SELECT 1 FROM same_repo_jvs_mutation)",
		"AND NOT EXISTS (SELECT 1 FROM active_restore_plan)",
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key)",
	)
	for _, forbidden := range []string{"UPDATE restore_plans", "UPDATE repo_fences", "DELETE ", "repo_fences", "INSERT INTO restore_plans"} {
		if strings.Contains(strings.ToUpper(exec.query), strings.ToUpper(forbidden)) {
			t.Fatalf("restore preview intake SQL contains forbidden fragment %q: %s", forbidden, exec.query)
		}
	}
}

func TestCreateOrReuseRestorePreviewOperationMapsAtomicGateFailures(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := restorePreviewQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	for _, tt := range []struct {
		name string
		gate string
		want error
	}{
		{name: "active restore plan", gate: "active_restore_plan", want: operations.ErrActiveRestorePlan},
		{name: "same repo jvs mutation", gate: "same_repo_jvs_mutation", want: operations.ErrRepoJVSMutationInProgress},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), false, tt.gate)}}
			st := &Store{exec: exec}

			_, err := st.CreateOrReuseRestorePreviewOperation(context.Background(), spec)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateOrReuseRestorePreviewOperation error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCreateOrReuseRestorePreviewOperationMapsJVSUniqueIndexViolation(t *testing.T) {
	spec := restorePreviewQueuedSpecFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: &pq.Error{Code: "23505", Constraint: "operations_one_non_terminal_jvs_mutation_per_repo_idx"}}}}

	_, err := st.CreateOrReuseRestorePreviewOperation(context.Background(), spec)
	if !errors.Is(err, operations.ErrRepoJVSMutationInProgress) {
		t.Fatalf("CreateOrReuseRestorePreviewOperation error = %v, want ErrRepoJVSMutationInProgress", err)
	}
}

func TestCreateOrReuseRestoreRunOperationUsesAtomicPlanAndDuplicateGatesAfterIdempotency(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := restoreRunQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), true, "")}}
	st := &Store{exec: exec}

	got, err := st.CreateOrReuseRestoreRunOperation(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateOrReuseRestoreRunOperation: %v", err)
	}
	if got.Existing || got.Reused {
		t.Fatalf("resolution = %#v, want brand-new restore-run operation", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH existing_operation AS (",
		"operation_type = 'restore_run'",
		"idempotency_key = $9",
		"), duplicate_restore_run AS (",
		"FROM operations",
		"operation_type = 'restore_run'",
		"namespace_id = $17",
		"repo_id = $18",
		"resource_type = 'repo'",
		"resource_id = $18",
		"input_summary->>'preview_operation_id' = $24::jsonb->>'preview_operation_id'",
		"operation_state NOT IN ('failed','cancelled')",
		"NOT EXISTS (SELECT 1 FROM existing_operation)",
		"), matching_pending_restore_plan AS (",
		"FROM restore_plans",
		"namespace_id = $17",
		"repo_id = $18",
		"preview_operation_id = $24::jsonb->>'preview_operation_id'",
		"status = 'pending'",
		"NOT EXISTS (SELECT 1 FROM existing_operation)",
		"FOR UPDATE",
		"), inserted_operation AS (",
		"INSERT INTO operations",
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation)",
		"AND EXISTS (SELECT 1 FROM matching_pending_restore_plan)",
		"AND NOT EXISTS (SELECT 1 FROM duplicate_restore_run)",
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key)",
	)
	for _, forbidden := range []string{"UPDATE restore_plans", "UPDATE repo_fences", "DELETE ", "repo_fences", "INSERT INTO restore_plans", "run_command", "recommended_next_command"} {
		if strings.Contains(strings.ToUpper(exec.query), strings.ToUpper(forbidden)) {
			t.Fatalf("restore run intake SQL contains forbidden fragment %q: %s", forbidden, exec.query)
		}
	}
}

func TestCreateOrReuseRestoreRunOperationMapsPlanAndDuplicateGateFailures(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := restoreRunQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	tests := []struct {
		name string
		gate string
		want error
	}{
		{name: "duplicate run", gate: "duplicate_restore_run", want: operations.ErrRestoreRunAlreadyExists},
		{name: "plan not pending", gate: "plan_not_pending", want: operations.ErrRestorePlanNotPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), false, tt.gate)}}
			st := &Store{exec: exec}

			_, err = st.CreateOrReuseRestoreRunOperation(context.Background(), spec)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateOrReuseRestoreRunOperation error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCreateOrReuseRestoreRunOperationMapsUniqueIndexViolations(t *testing.T) {
	spec := restoreRunQueuedSpecFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))
	tests := []struct {
		name       string
		constraint string
		want       error
	}{
		{name: "same repo jvs mutation", constraint: "operations_one_non_terminal_jvs_mutation_per_repo_idx", want: operations.ErrRepoJVSMutationInProgress},
		{name: "duplicate run preview", constraint: "operations_restore_run_one_per_preview_idx", want: operations.ErrRestoreRunAlreadyExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &Store{exec: &fakeExecutor{row: fakeRow{err: &pq.Error{Code: "23505", Constraint: tt.constraint}}}}

			_, err := st.CreateOrReuseRestoreRunOperation(context.Background(), spec)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateOrReuseRestoreRunOperation error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCreateOrReuseRestorePreviewDiscardOperationUsesAtomicPlanGateAfterIdempotency(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := restorePreviewDiscardQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), true, "")}}
	st := &Store{exec: exec}

	got, err := st.CreateOrReuseRestorePreviewDiscardOperation(context.Background(), spec)
	if err != nil {
		t.Fatalf("CreateOrReuseRestorePreviewDiscardOperation: %v", err)
	}
	if got.Existing || got.Reused {
		t.Fatalf("resolution = %#v, want brand-new restore-preview discard operation", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH existing_operation AS (",
		"operation_type = 'restore_preview_discard'",
		"idempotency_key = $9",
		"), matching_pending_restore_plan AS (",
		"FROM restore_plans",
		"namespace_id = $17",
		"repo_id = $18",
		"preview_operation_id = $24::jsonb->>'preview_operation_id'",
		"status = 'pending'",
		"NOT EXISTS (SELECT 1 FROM existing_operation)",
		"FOR UPDATE",
		"), inserted_operation AS (",
		"INSERT INTO operations",
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation)",
		"AND EXISTS (SELECT 1 FROM matching_pending_restore_plan)",
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key)",
	)
	for _, forbidden := range []string{"UPDATE restore_plans", "UPDATE repo_fences", "DELETE ", "repo_fences", "INSERT INTO restore_plans", "run_command", "recommended_next_command"} {
		if strings.Contains(strings.ToUpper(exec.query), strings.ToUpper(forbidden)) {
			t.Fatalf("restore preview discard intake SQL contains forbidden fragment %q: %s", forbidden, exec.query)
		}
	}
}

func TestCreateOrReuseRestorePreviewDiscardOperationMapsPlanGateFailure(t *testing.T) {
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	spec := restorePreviewDiscardQueuedSpecFixture(createdAt)
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		t.Fatalf("NewQueuedOperationRecord: %v", err)
	}
	exec := &fakeExecutor{row: fakeRow{values: append(operationRowValues(record.Sanitized()), false, "plan_not_pending")}}
	st := &Store{exec: exec}

	_, err = st.CreateOrReuseRestorePreviewDiscardOperation(context.Background(), spec)
	if !errors.Is(err, operations.ErrRestorePlanNotPending) {
		t.Fatalf("CreateOrReuseRestorePreviewDiscardOperation error = %v, want ErrRestorePlanNotPending", err)
	}
}

func TestCreateOrReuseRestorePreviewDiscardOperationMapsJVSUniqueIndexViolation(t *testing.T) {
	spec := restorePreviewDiscardQueuedSpecFixture(time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC))
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: &pq.Error{Code: "23505", Constraint: "operations_one_non_terminal_jvs_mutation_per_repo_idx"}}}}

	_, err := st.CreateOrReuseRestorePreviewDiscardOperation(context.Background(), spec)
	if !errors.Is(err, operations.ErrRepoJVSMutationInProgress) {
		t.Fatalf("CreateOrReuseRestorePreviewDiscardOperation error = %v, want ErrRepoJVSMutationInProgress", err)
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
			"((lease_owner IS NULL AND lease_expires_at IS NULL) OR",
			"(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4)",
		)
	})
}

func TestAcquireOperationLeaseFinalizeCancellationNoRowsIsLeaseUnavailable(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.AcquireOperationLease(context.Background(), "op-alpha", operations.LeaseRequest{
		Owner:        "canceller",
		Duration:     30 * time.Minute,
		Now:          now,
		CancelPolicy: operations.LeaseCancelPolicyFinalize,
	})
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("AcquireOperationLease finalize cancellation error = %v, want ErrLeaseUnavailable and sql.ErrNoRows", err)
	}
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

func TestAcquireNamespaceUpsertOperationLeaseScopesAtomicUpdateBeforeMutation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-namespace"
	record.Type = operations.OperationNamespaceUpsert
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseNamespaceUpsertValidate
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &leaseExpiresAt
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.AcquireNamespaceUpsertOperationLease(context.Background(), "op-namespace", operations.LeaseRequest{
		Owner:    " worker-a ",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("AcquireNamespaceUpsertOperationLease: %v", err)
	}
	if got.ID != "op-namespace" || got.Type != operations.OperationNamespaceUpsert || got.Phase != operations.OperationPhaseNamespaceUpsertValidate {
		t.Fatalf("got = %#v, want namespace upsert validate", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations",
		"operation_state = CASE",
		"attempt = CASE",
		"lease_owner = CASE",
		"lease_expires_at = CASE",
		"started_at = CASE",
		"finished_at = CASE",
		"WHERE operation_id = $1",
		"operation_type = 'namespace_upsert'",
		"phase = 'validate_namespace_upsert'",
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL)",
		"(operation_state = 'running'",
		"lease_expires_at <= $4",
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation'",
		"RETURNING",
	)
	if strings.Contains(exec.query, "operator_intervention_required") {
		t.Fatalf("namespace upsert acquire must not perform explicit operator recovery: %s", exec.query)
	}
	if strings.Contains(exec.query, "SELECT ") {
		t.Fatalf("AcquireNamespaceUpsertOperationLease query must be a single UPDATE RETURNING, got %s", exec.query)
	}
	wantArgs := []any{"op-namespace", "worker-a", leaseExpiresAt, now, string(operations.LeaseCancelPolicyNone)}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
}

func TestAcquireVolumeEnsureOperationLeaseScopesAtomicUpdateBeforeMutation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-volume"
	record.Type = operations.OperationVolumeEnsure
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseVolumeEnsureValidate
	record.Resource = operations.ResourceRef{Type: "volume", ID: "vol_123"}
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &leaseExpiresAt
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.AcquireVolumeEnsureOperationLease(context.Background(), "op-volume", operations.LeaseRequest{Owner: "worker-a", Duration: 30 * time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireVolumeEnsureOperationLease: %v", err)
	}
	if got.ID != "op-volume" || got.LeaseOwner != "worker-a" {
		t.Fatalf("leased record = %#v, want op-volume worker-a", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations",
		"WHERE operation_id = $1",
		"operation_type = 'volume_ensure'",
		"phase = 'validate_volume_ensure'",
		"namespace_id = ''",
		"(operation_state = 'queued' AND $5 = ''",
		"(operation_state = 'running' AND $5 = ''",
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation'",
		"RETURNING",
	)
	if strings.Contains(exec.query, "SELECT ") {
		t.Fatalf("AcquireVolumeEnsureOperationLease query must be single UPDATE RETURNING, got %s", exec.query)
	}
	wantArgs := []any{"op-volume", "worker-a", leaseExpiresAt, now, string(operations.LeaseCancelPolicyNone)}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
}

func TestAcquireVolumeEnsureOperationLeaseRejectsMismatchedScopeWithoutMutation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.AcquireVolumeEnsureOperationLease(context.Background(), "op-repo", operations.LeaseRequest{
		Owner:    "worker-a",
		Duration: time.Minute,
		Now:      now,
	})
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("AcquireVolumeEnsureOperationLease error = %v, want ErrLeaseUnavailable/sql.ErrNoRows", err)
	}
}

func TestAcquireNamespaceVolumeBindingPutOperationLeaseScopesAtomicUpdateBeforeMutation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(30 * time.Minute)
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-binding"
	record.Type = operations.OperationNamespaceVolumeBindingPut
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseNamespaceVolumeBindingPutValidate
	record.NamespaceID = "ns_alpha01"
	record.Resource = operations.ResourceRef{Type: "namespace_volume_binding", ID: "ns_alpha01"}
	record.LeaseOwner = "worker-a"
	record.LeaseExpiresAt = &leaseExpiresAt
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.AcquireNamespaceVolumeBindingPutOperationLease(context.Background(), "op-binding", operations.LeaseRequest{
		Owner:    " worker-a ",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("AcquireNamespaceVolumeBindingPutOperationLease: %v", err)
	}
	if got.ID != "op-binding" || got.LeaseOwner != "worker-a" || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("leased record = %#v, want op-binding worker-a", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations",
		"WHERE operation_id = $1",
		"operation_type = 'namespace_volume_binding_put'",
		"phase = 'validate_namespace_volume_binding_put'",
		"(operation_state = 'queued' AND $5 = ''",
		"(operation_state = 'running' AND $5 = ''",
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation'",
		"RETURNING",
	)
	if strings.Contains(exec.query, "SELECT ") {
		t.Fatalf("AcquireNamespaceVolumeBindingPutOperationLease query must be single UPDATE RETURNING, got %s", exec.query)
	}
	wantArgs := []any{"op-binding", "worker-a", leaseExpiresAt, now, string(operations.LeaseCancelPolicyNone)}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
}

func TestAcquireNamespaceVolumeBindingPutOperationLeaseRejectsMismatchedScopeWithoutMutation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.AcquireNamespaceVolumeBindingPutOperationLease(context.Background(), "op-repo", operations.LeaseRequest{
		Owner:    "worker-a",
		Duration: time.Minute,
		Now:      now,
	})
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("AcquireNamespaceVolumeBindingPutOperationLease error = %v, want ErrLeaseUnavailable/sql.ErrNoRows", err)
	}
}

func TestAcquireNamespaceUpsertOperationLeaseCanFinalizeCancellation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	returned := operationFixture(now.Add(-time.Hour))
	returned.ID = "op-namespace-cancel"
	returned.Type = operations.OperationNamespaceUpsert
	returned.State = operations.OperationStateCancelled
	returned.Phase = operations.OperationPhaseNamespaceUpsertValidate
	returned.NamespaceID = "ns_alpha01"
	returned.Resource = operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"}
	returned.LeaseOwner = ""
	returned.LeaseExpiresAt = nil
	returned.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(returned)}}
	st := &Store{exec: exec}

	got, err := st.AcquireNamespaceUpsertOperationLease(context.Background(), "op-namespace-cancel", operations.LeaseRequest{
		Owner:        "worker-a",
		Duration:     30 * time.Minute,
		Now:          now,
		CancelPolicy: operations.LeaseCancelPolicyFinalize,
	})
	if err != nil {
		t.Fatalf("AcquireNamespaceUpsertOperationLease finalize: %v", err)
	}
	if got.State != operations.OperationStateCancelled || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("finalized operation = %#v, want cancelled without lease", got)
	}
	if exec.args[4] != string(operations.LeaseCancelPolicyFinalize) {
		t.Fatalf("cancel policy arg = %#v, want finalize", exec.args[4])
	}
	assertSQLContainsInOrder(t, exec.query,
		"operation_type = 'namespace_upsert'",
		"phase = 'validate_namespace_upsert'",
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation'",
	)
}

func TestAcquireNamespaceUpsertOperationLeaseNoRowsIsUnavailableBeforeStateChange(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, err := st.AcquireNamespaceUpsertOperationLease(context.Background(), "op-repo", operations.LeaseRequest{
		Owner:    "worker-a",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("AcquireNamespaceUpsertOperationLease error = %v, want ErrLeaseUnavailable and sql.ErrNoRows", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations",
		"WHERE operation_id = $1",
		"operation_type = 'namespace_upsert'",
		"phase = 'validate_namespace_upsert'",
	)
}

func TestAcquireNamespaceUpsertOperationLeaseRejectsUnsupportedPoliciesBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{}
	st := &Store{exec: exec}

	_, err := st.AcquireNamespaceUpsertOperationLease(context.Background(), "op-namespace", operations.LeaseRequest{
		Owner:        "worker-a",
		Duration:     time.Minute,
		Now:          now,
		RecoveryMode: operations.LeaseRecoveryExplicitAction,
	})
	if !errors.Is(err, operations.ErrInvalidLeaseRequest) {
		t.Fatalf("AcquireNamespaceUpsertOperationLease error = %v, want ErrInvalidLeaseRequest", err)
	}
	if exec.query != "" {
		t.Fatalf("issued SQL for invalid namespace acquire request: %s", exec.query)
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
	returned.Type = operations.OperationExportCreate
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

func TestCommitOperationWithLeaseAtomicallyUpdatesOperationAndAppendsAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	returned := operationFixture(now.Add(-time.Hour))
	returned.Type = operations.OperationExportCreate
	returned.State = operations.OperationStateSucceeded
	returned.Phase = "done"
	returned.LeaseOwner = ""
	returned.LeaseExpiresAt = nil
	returned.ExternalResourceIDs = map[string]string{"jvs_repo_id": "jvs-commit-secret"}
	returned.InputSummary = map[string]any{"command": "export --token commit-input-secret", "safe": "visible"}
	returned.JVSJSONOutput = map[string]any{"token": "commit-output-secret"}
	returned.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(returned.SanitizedForPersistence().Record())}}
	st := &Store{exec: exec}

	update := returned
	event := commitAuditEvent("audit-op-alpha", "op-alpha", now)
	event.Reason = "finished token=audit-reason-secret"
	event.Resource.Path = "/control/root/raw-secret"
	event.Details = map[string]any{"authorization": "Bearer audit-detail-secret", "safe": "visible"}

	got, err := st.CommitOperationWithLease(context.Background(), update.SanitizedForPersistence(), "worker-a", now, event)
	if err != nil {
		t.Fatalf("CommitOperationWithLease: %v", err)
	}
	if got.ID != "op-alpha" || got.State != operations.OperationStateSucceeded || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("committed operation = %#v", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH updated_operation AS (",
		"UPDATE operations SET",
		"WHERE operation_id = $12",
		"operation_state = 'running'",
		"lease_owner = $13",
		"lease_expires_at IS NOT NULL",
		"lease_expires_at > $11",
		"RETURNING",
		"), inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"SELECT",
		"FROM updated_operation",
		"RETURNING audit_event_id",
		") SELECT",
		"FROM updated_operation",
		"WHERE EXISTS (SELECT 1 FROM inserted_audit)",
	)
	if strings.Contains(exec.query, "UPDATE operations SET") && strings.Contains(exec.query, "INSERT INTO audit_outbox") {
		// The important assertion is that both mutations live in one QueryRow CTE.
	} else {
		t.Fatalf("commit SQL missing update or audit insert: %s", exec.query)
	}
	if len(exec.args) != len(operationLeaseFencedUpdateArgsForTest(t, update, "worker-a", now))+len(auditOutboxColumns) {
		t.Fatalf("arg count = %d, want operation args plus audit args", len(exec.args))
	}
	if exec.queryRowCalls != 1 || exec.execCalls != 0 || exec.queryCalls != 0 {
		t.Fatalf("executor calls queryRow/exec/query = %d/%d/%d, want 1/0/0", exec.queryRowCalls, exec.execCalls, exec.queryCalls)
	}
	rendered := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"commit-input-secret", "commit-output-secret", "jvs-commit-secret", "audit-reason-secret", "raw-secret", "audit-detail-secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("CommitOperationWithLease args leaked %q in %s", forbidden, rendered)
		}
	}
}

func TestCommitOperationWithLeaseRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	tests := []struct {
		name  string
		owner string
		now   time.Time
		event audit.Event
	}{
		{name: "blank owner", owner: " \t", now: now, event: commitAuditEvent("audit-op-alpha", "op-alpha", now)},
		{name: "zero now", owner: "worker-a", event: commitAuditEvent("audit-op-alpha", "op-alpha", now)},
		{name: "missing audit operation", owner: "worker-a", now: now, event: commitAuditEvent("audit-op-alpha", "", now)},
		{name: "mismatched audit operation", owner: "worker-a", now: now, event: commitAuditEvent("audit-op-alpha", "op-other", now)},
		{name: "invalid audit event", owner: "worker-a", now: now, event: commitAuditEvent("", "op-alpha", now)},
		{name: "invalid audit payload", owner: "worker-a", now: now, event: func() audit.Event {
			event := commitAuditEvent("audit-op-alpha", "op-alpha", now)
			event.Details = map[string]any{"bad": make(chan int)}
			return event
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.CommitOperationWithLease(context.Background(), record.SanitizedForPersistence(), tt.owner, tt.now, tt.event)
			if err == nil {
				t.Fatal("CommitOperationWithLease succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid request: %s", exec.query)
			}
		})
	}
}

func TestCommitOperationWithLeaseNoRowsWrapsLeaseUnavailable(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, err := st.CommitOperationWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, commitAuditEvent("audit-op-alpha", "op-alpha", now))
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitOperationWithLease error = %v, want ErrLeaseUnavailable and sql.ErrNoRows", err)
	}
}

func TestCommitOperationWithLeaseAuditInsertFailureReturnsError(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now.Add(-time.Hour))
	insertErr := errors.New("audit insert failed")
	exec := &fakeExecutor{row: fakeRow{err: insertErr}}
	st := &Store{exec: exec}

	_, err := st.CommitOperationWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, commitAuditEvent("audit-op-alpha", "op-alpha", now))
	if !errors.Is(err, insertErr) {
		t.Fatalf("CommitOperationWithLease error = %v, want audit insert error", err)
	}
	if !strings.Contains(exec.query, "WITH updated_operation AS") || !strings.Contains(exec.query, "INSERT INTO audit_outbox") {
		t.Fatalf("commit did not use atomic CTE boundary: %s", exec.query)
	}
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

func repoCreateQueuedSpecFixture(createdAt time.Time) operations.QueuedOperationSpec {
	return operations.QueuedOperationSpec{
		OperationID:     "op-repo-create",
		Scope:           operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRepoCreate, "idem-repo"),
		RequestHash:     "sha256:repo-create",
		Phase:           operations.OperationPhaseRepoCreateValidate,
		CorrelationID:   "corr-repo",
		CallerService:   "agentsmith-api",
		AuthorizedActor: operations.Actor{Type: "user", ID: "user_123"},
		Resource:        operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:     "ns_alpha01",
		RepoID:          "repo_alpha01",
		InputSummary:    map[string]any{"namespace_id": "ns_alpha01", "target_repo_id": "repo_alpha01"},
		CreatedAt:       createdAt,
	}
}

func restorePreviewQueuedSpecFixture(createdAt time.Time) operations.QueuedOperationSpec {
	return operations.QueuedOperationSpec{
		OperationID:     "op_restore_preview",
		Scope:           operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreview, "idem-preview"),
		RequestHash:     "sha256:restore-preview",
		Phase:           operations.OperationPhaseRestorePreviewValidate,
		CorrelationID:   "corr-preview",
		CallerService:   "agentsmith-api",
		AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:        operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:     "ns_alpha01",
		RepoID:          "repo_alpha01",
		InputSummary:    map[string]any{"save_point_id": "sp_001"},
		CreatedAt:       createdAt,
	}
}

func restoreRunQueuedSpecFixture(createdAt time.Time) operations.QueuedOperationSpec {
	return operations.QueuedOperationSpec{
		OperationID:     "op_restore_run",
		Scope:           operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestoreRun, "idem-run"),
		RequestHash:     "sha256:restore-run",
		Phase:           operations.OperationPhaseRestoreRunValidate,
		CorrelationID:   "corr-run",
		CallerService:   "agentsmith-api",
		AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:        operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:     "ns_alpha01",
		RepoID:          "repo_alpha01",
		InputSummary:    map[string]any{"preview_operation_id": "op_preview01"},
		CreatedAt:       createdAt,
	}
}

func restorePreviewDiscardQueuedSpecFixture(createdAt time.Time) operations.QueuedOperationSpec {
	return operations.QueuedOperationSpec{
		OperationID:     "op_restore_discard",
		Scope:           operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreviewDiscard, "idem-discard"),
		RequestHash:     "sha256:restore-preview-discard",
		Phase:           operations.OperationPhaseRestorePreviewDiscardValidate,
		CorrelationID:   "corr-discard",
		CallerService:   "agentsmith-api",
		AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:        operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:     "ns_alpha01",
		RepoID:          "repo_alpha01",
		InputSummary:    map[string]any{"preview_operation_id": "op_preview01"},
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

func operationIDsForPostgresTest(records []operations.OperationRecord) []string {
	out := make([]string, len(records))
	for idx, record := range records {
		out[idx] = record.ID
	}
	return out
}

func operationLeaseFencedUpdateArgsForTest(t *testing.T, record operations.OperationRecord, owner string, now time.Time) []any {
	t.Helper()
	args, err := operationLeaseFencedUpdateArgs(record.SanitizedForPersistence().Record(), owner, now)
	if err != nil {
		t.Fatalf("operationLeaseFencedUpdateArgs: %v", err)
	}
	return args
}

func commitAuditEvent(eventID, operationID string, now time.Time) audit.Event {
	return audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeExportCreate,
		Time:            now,
		CallerService:   "afscp-api",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		CorrelationID:   "corr-alpha",
		OperationID:     operationID,
		Resource:        audit.Resource{Type: "repo", ID: "repo-alpha"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "operation committed",
		Details:         map[string]any{"safe": "visible"},
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
	query         string
	args          []any
	row           fakeRow
	rows          fakeRows
	err           error
	rowsAffected  int64
	execCalls     int
	queryRowCalls int
	queryCalls    int
}

func (fake *fakeExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	fake.execCalls++
	fake.query = query
	fake.args = args
	return fakeResult{rowsAffected: fake.rowsAffected}, fake.err
}

func (fake *fakeExecutor) QueryRowContext(_ context.Context, query string, args ...any) rowScanner {
	fake.queryRowCalls++
	fake.query = query
	fake.args = args
	return fake.row
}

func (fake *fakeExecutor) QueryContext(_ context.Context, query string, args ...any) (rowsScanner, error) {
	fake.queryCalls++
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
