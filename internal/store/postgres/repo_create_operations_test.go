package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestRepoCreateRecoveryListAndAcquireAreSQLScoped(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := repoCreateRunningRecord(now)
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{{values: operationRowValues(record)}}}}
	st := &Store{exec: exec}

	got, err := st.ListRepoCreateOperationsForRecovery(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("ListRepoCreateOperationsForRecovery: %v", err)
	}
	if len(got) != 1 || got[0].ID != record.ID {
		t.Fatalf("records = %#v, want repo_create record", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WHERE operation_type = 'repo_create' AND phase = 'validate_repo_create' AND (",
		"ORDER BY created_at, operation_id LIMIT $2",
	)

	exec = &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st = &Store{exec: exec}
	_, err = st.AcquireRepoCreateOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireRepoCreateOperationLease: %v", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations SET",
		"WHERE operation_id = $1",
		"AND operation_type = 'repo_create'",
		"AND phase = 'validate_repo_create'",
		"RETURNING",
	)
}

func TestCommitRepoCreateSucceededWithLeaseAtomicBoundary(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo := repoCreateRepoFixture(now)
	record := repoCreateSucceededRecord(now)
	committedRecord := record.SanitizedForPersistence().Record()
	committedRecord.LeaseOwner = ""
	committedRecord.LeaseExpiresAt = nil
	exec := &fakeExecutor{row: fakeRow{values: append(repoRowValues(repo), operationRowValues(committedRecord)...)}}
	st := &Store{exec: exec}

	gotRepo, gotOperation, err := st.CommitRepoCreateSucceededWithLease(context.Background(), repo, record.SanitizedForPersistence(), "worker-a", now, repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeSucceeded), "fence-repo")
	if err != nil {
		t.Fatalf("CommitRepoCreateSucceededWithLease: %v", err)
	}
	if gotRepo.ID != repo.ID || gotOperation.ID != record.ID || gotOperation.State != operations.OperationStateSucceeded || gotOperation.Phase != operations.OperationPhaseRepoCreateCommitted || gotOperation.LeaseOwner != "" || gotOperation.LeaseExpiresAt != nil {
		t.Fatalf("commit returned repo/operation = %#v/%#v, want committed operation without running lease", gotRepo, gotOperation)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS (",
		"SELECT operation_id FROM operations",
		"WHERE operation_id = $12",
		"operation_state = 'running'",
		"lease_owner = $13",
		"lease_expires_at IS NOT NULL",
		"lease_expires_at > $11",
		"operation_type = 'repo_create'",
		"phase = 'validate_repo_create'",
		"namespace_id = $14",
		"repo_id = $19",
		"resource_type = 'repo'",
		"resource_id = $19",
		"FOR UPDATE",
		"), active_namespace AS (",
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'",
		"), active_binding AS (",
		"SELECT namespace_id, default_volume_id FROM namespace_volume_bindings WHERE namespace_id = $14 AND status = 'active'",
		"), active_volume AS (",
		"SELECT volume_id FROM volumes",
		"status = 'active'",
		"capabilities->>'jvs_external_control_root' = 'true'",
		"), held_fence AS (",
		"SELECT fence_id FROM repo_fences",
		"fence_kind = 'lifecycle'",
		"holder_operation_id = $12",
		"status = 'active'",
		"FOR UPDATE",
		"), inserted_repo AS (",
		"INSERT INTO repos",
		"SELECT",
		"FROM eligible_operation, active_namespace, active_binding, active_volume, held_fence",
		"WHERE NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $19)",
		"), updated_operation AS (",
		"UPDATE operations SET",
		"FROM eligible_operation, inserted_repo",
		"operations.operation_id = eligible_operation.operation_id",
		"RETURNING operations.operation_id",
		"), released_fence AS (",
		"UPDATE repo_fences SET",
		"released_at = $11",
		"FROM updated_operation, held_fence",
		"), inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"FROM updated_operation, inserted_repo, released_fence",
	)
	if strings.Contains(exec.query, "RETURNING operation_id") {
		t.Fatalf("repo_create success commit uses ambiguous operation RETURNING columns: %s", exec.query)
	}
	if exec.queryRowCalls != 1 || exec.execCalls != 0 || exec.queryCalls != 0 {
		t.Fatalf("executor calls queryRow/exec/query = %d/%d/%d, want 1/0/0", exec.queryRowCalls, exec.execCalls, exec.queryCalls)
	}
	rendered := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"/srv/afscp", "payload_root", "control_root", "password", "secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("repo_create success args leaked %q in %s", forbidden, rendered)
		}
	}
}

func TestCommitRepoCreateSucceededWithLeaseRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo := repoCreateRepoFixture(now)
	record := repoCreateSucceededRecord(now)
	tests := []struct {
		name    string
		repo    resources.Repo
		record  operations.OperationRecord
		event   audit.Event
		fenceID string
	}{
		{name: "wrong type", repo: repo, record: func() operations.OperationRecord {
			edited := record
			edited.Type = operations.OperationNamespaceUpsert
			return edited
		}(), event: repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeSucceeded), fenceID: "fence-repo"},
		{name: "wrong state", repo: repo, record: func() operations.OperationRecord {
			edited := record
			edited.State = operations.OperationStateRunning
			return edited
		}(), event: repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeSucceeded), fenceID: "fence-repo"},
		{name: "wrong terminal phase", repo: repo, record: func() operations.OperationRecord {
			edited := record
			edited.Phase = operations.OperationPhaseRepoCreateValidate
			return edited
		}(), event: repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeSucceeded), fenceID: "fence-repo"},
		{name: "wrong resource", repo: repo, record: func() operations.OperationRecord {
			edited := record
			edited.Resource.ID = "repo_other01"
			return edited
		}(), event: repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeSucceeded), fenceID: "fence-repo"},
		{name: "missing fence", repo: repo, record: record, event: repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeSucceeded), fenceID: ""},
		{name: "wrong audit outcome", repo: repo, record: record, event: repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeFailed), fenceID: "fence-repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			_, _, err := st.CommitRepoCreateSucceededWithLease(context.Background(), tt.repo, tt.record.SanitizedForPersistence(), "worker-a", now, tt.event, tt.fenceID)
			if err == nil {
				t.Fatal("CommitRepoCreateSucceededWithLease succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid request: %s", exec.query)
			}
		})
	}
}

func TestCommitRepoCreateSucceededWithLeaseNoRowsFailsClosed(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo := repoCreateRepoFixture(now)
	record := repoCreateSucceededRecord(now)
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, _, err := st.CommitRepoCreateSucceededWithLease(context.Background(), repo, record.SanitizedForPersistence(), "worker-a", now, repoCreateAuditEvent("audit-repo", record.ID, repo.ID, now, audit.OutcomeSucceeded), "fence-repo")
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitRepoCreateSucceededWithLease error = %v, want ErrLeaseUnavailable/sql.ErrNoRows", err)
	}
}

func TestCommitRepoCreateFailedWithLeaseCanReleaseOrKeepFenceAtomically(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name           string
		state          operations.OperationState
		releaseFenceID string
		wantReleaseSQL bool
	}{
		{name: "failed releases fence", state: operations.OperationStateFailed, releaseFenceID: "fence-repo", wantReleaseSQL: true},
		{name: "intervention keeps fence", state: operations.OperationStateOperatorInterventionRequired, releaseFenceID: "", wantReleaseSQL: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			record := repoCreateRunningRecord(now)
			record.State = tt.state
			record.Phase = operations.OperationPhaseRepoCreateValidate
			record.Error = &operations.OperationError{Code: "JVS_COMMAND_FAILED", Message: "jvs command failed", Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID}
			exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record.SanitizedForPersistence().Record())}}
			st := &Store{exec: exec}

			got, err := st.CommitRepoCreateFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, repoCreateAuditEvent("audit-repo", record.ID, record.RepoID, now, audit.OutcomeFailed), tt.releaseFenceID)
			if err != nil {
				t.Fatalf("CommitRepoCreateFailedWithLease: %v", err)
			}
			if got.ID != record.ID || got.State != tt.state {
				t.Fatalf("operation = %#v, want state %s", got, tt.state)
			}
			assertSQLContainsInOrder(t, exec.query,
				"WITH eligible_operation AS (",
				"SELECT operation_id FROM operations",
				"operation_type = 'repo_create'",
				"phase = 'validate_repo_create'",
				"namespace_id = $14",
				"repo_id = $15",
				"resource_type = 'repo'",
				"resource_id = $15",
				"FOR UPDATE",
				"), released_fence AS (",
				"UPDATE repo_fences SET",
				"FROM eligible_operation",
				"WHERE $20 <> ''",
				"fence_kind = 'lifecycle'",
				"), updated_operation AS (",
				"UPDATE operations SET",
				"FROM eligible_operation",
				"operations.operation_id = eligible_operation.operation_id",
				"($20 = '' OR EXISTS (SELECT 1 FROM released_fence))",
				"), inserted_audit AS (",
				"INSERT INTO audit_outbox",
			)
			if strings.Contains(exec.query, "CreateRepo") || strings.Contains(exec.query, "CommitOperationWithLease") {
				t.Fatalf("failure boundary composed generic helpers: %s", exec.query)
			}
		})
	}
}

func TestMarkRepoCreateMetadataReadPendingExpiresLeaseWithoutAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := repoCreateRunningRecord(now)
	record.Error = &operations.OperationError{
		Code:          "REPO_CREATE_METADATA_READ_PENDING",
		Message:       "repo create metadata read is pending",
		Retryable:     true,
		CorrelationID: record.CorrelationID,
		OperationID:   record.ID,
		Details:       map[string]any{"repo_id": record.RepoID, "retry_reason": "volume_read_unavailable", "volume_id": "vol_123"},
	}
	record.VerificationResult = map[string]any{"repo_id": record.RepoID, "retry_reason": "volume_read_unavailable", "volume_id": "vol_123"}
	returned := record
	returned.LeaseExpiresAt = &now
	values := operationRowValues(returned)
	values[25] = mustMarshalJSONForTest(returned.VerificationResult)
	values[27] = mustMarshalJSONForTest(returned.Error)
	exec := &fakeExecutor{row: fakeRow{values: values}}
	st := &Store{exec: exec}

	got, err := st.MarkRepoCreateMetadataReadPendingWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("MarkRepoCreateMetadataReadPendingWithLease: %v", err)
	}

	if got.State != operations.OperationStateRunning || got.Error == nil || got.Error.Code != "REPO_CREATE_METADATA_READ_PENDING" || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(now) {
		t.Fatalf("operation = %#v, want running metadata-read pending with expired lease", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_state = 'running'",
		"operation_type = 'repo_create'",
		"phase = 'validate_repo_create'",
		"FOR UPDATE",
		"UPDATE operations SET",
		"operation_state = $1",
		"lease_owner = operations.lease_owner",
		"lease_expires_at = $11",
		"finished_at = NULL",
	)
	if strings.Contains(exec.query, "audit_outbox") {
		t.Fatalf("metadata read pending update must not write audit outbox: %s", exec.query)
	}
}

func repoCreateRepoFixture(now time.Time) resources.Repo {
	return resources.Repo{
		ID:                  "repo_alpha01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_repo_alpha",
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive, LastLifecycleOperationID: "op_repo"},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func repoCreateRunningRecord(now time.Time) operations.OperationRecord {
	leaseExpiresAt := now.Add(time.Minute)
	startedAt := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_repo",
		Type:                operations.OperationRepoCreate,
		State:               operations.OperationStateRunning,
		Phase:               operations.OperationPhaseRepoCreateValidate,
		Attempt:             1,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &leaseExpiresAt,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRepoCreate, "idem_repo").String(),
		IdempotencyKey:      "idem_repo",
		RequestHash:         "sha256:repo",
		CorrelationID:       "corr-alpha",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        map[string]any{"namespace_id": "ns_alpha01", "target_repo_id": "repo_alpha01"},
		CreatedAt:           now.Add(-time.Hour),
		StartedAt:           &startedAt,
	}
}

func repoCreateSucceededRecord(now time.Time) operations.OperationRecord {
	record := repoCreateRunningRecord(now)
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseRepoCreateCommitted
	record.ExternalResourceIDs = map[string]string{"jvs_repo_id": "jvs_repo_alpha"}
	record.JVSJSONOutput = map[string]any{"repo_id": "jvs_repo_alpha", "workspace": "main"}
	record.VerificationResult = map[string]any{"repo_id": "jvs_repo_alpha", "workspace": "main", "healthy": true}
	record.FinishedAt = &now
	return record
}

func repoCreateAuditEvent(eventID, operationID, repoID string, now time.Time, outcome audit.Outcome) audit.Event {
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeRepoCreate,
		Time:            now,
		CallerService:   "product-caller",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		CorrelationID:   "corr-alpha",
		OperationID:     operationID,
		Resource:        audit.Resource{Type: "repo", ID: repoID, NamespaceID: "ns_alpha01"},
		Outcome:         outcome,
		Reason:          "repo_create_committed",
		Details:         map[string]any{"repo_id": repoID, "volume_id": "vol_123"},
	})
}
