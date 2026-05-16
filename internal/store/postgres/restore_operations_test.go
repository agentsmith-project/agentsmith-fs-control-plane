package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestListRestoreOperationsForRecoveryScopesBeforeOrderAndLimit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreOperationRecord(now, operations.OperationStateQueued, operations.OperationPhaseRestoreValidate)
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{{values: operationRowValues(record)}}}}
	st := &Store{exec: exec}

	got, err := st.ListRestoreOperationsForRecovery(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("ListRestoreOperationsForRecovery: %v", err)
	}
	if len(got) != 1 || got[0].ID != record.ID {
		t.Fatalf("restore candidates = %#v, want %q", got, record.ID)
	}

	assertSQLContainsInOrder(t, exec.query,
		"FROM operations",
		"operation_type = 'restore'",
		"phase IN ('validate_restore','restore_writer_fenced')",
		"operation_state = 'cancel_requested' AND phase = 'validate_restore'",
		"ORDER BY created_at, operation_id LIMIT $2",
	)
	for _, forbidden := range []string{"UPDATE ", "INSERT ", "DELETE ", "FOR UPDATE"} {
		if strings.Contains(strings.ToUpper(exec.query), strings.ToUpper(forbidden)) {
			t.Fatalf("restore recovery list must be read-only SELECT, got %s", exec.query)
		}
	}
}

func TestAcquireRestoreOperationLeaseSerializesMutationsForDirectRestore(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestoreValidate)
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRestoreOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireRestoreOperationLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore'",
		"phase IN ('validate_restore','restore_writer_fenced')",
		"input_summary->>'save_point_id'",
		"earlier_jvs_mutation AS",
		"o.operation_type IN ('save_point_create', 'restore', 'template_create', 'template_clone')",
		"earlier_repo_lifecycle AS",
		"NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle)",
	)
	if strings.Contains(exec.query, "restore_plans") || strings.Contains(exec.query, "active_restore_plan") {
		t.Fatalf("direct restore acquire SQL must not inspect restore plans: %s", exec.query)
	}
}

func TestMarkRestoreWriterFencedWithLeaseCreatesOrConfirmsWriterFence(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestoreWriterFenced)
	record.SessionFenceID = "fence_restore01"
	fence := restoreWriterFence(record, now)
	exec := &fakeExecutor{row: fakeRow{values: append(repoFenceRowValues(fence), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	gotFence, gotOperation, err := st.MarkRestoreWriterFencedWithLease(context.Background(), fence, record.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("MarkRestoreWriterFencedWithLease: %v", err)
	}
	if gotFence.ID != fence.ID || gotOperation.SessionFenceID != fence.ID || gotOperation.Phase != operations.OperationPhaseRestoreWriterFenced {
		t.Fatalf("writer fence mark = %#v/%#v", gotFence, gotOperation)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore'",
		"phase = 'validate_restore'",
		"locked_repo AS",
		"held_lifecycle_fence AS",
		"active_writer_fence AS",
		"fence_kind = 'writer_session'",
		"inserted_writer_fence AS",
		"INSERT INTO repo_fences",
		"ON CONFLICT (repo_id, fence_kind) WHERE released_at IS NULL DO NOTHING",
		"updated_operation AS",
		"session_fence_id = $21",
		"NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)",
	)
}

func TestCommitRestoreSucceededWithLeaseAuditsAndReleasesFenceWithoutPlan(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestoreCommitted)
	record.SessionFenceID = "fence_restore01"
	record.JVSJSONOutput = map[string]any{"restored_save_point_id": "sp_001", "previous_head": "sp_before", "new_head": "sp_001", "workspace": "main", "mode": "direct_restore"}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.CommitRestoreSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restoreAudit(record, audit.OutcomeSucceeded, now))
	if err != nil {
		t.Fatalf("CommitRestoreSucceededWithLease: %v", err)
	}
	if got.State != operations.OperationStateSucceeded || got.Phase != operations.OperationPhaseRestoreCommitted {
		t.Fatalf("restore success = %#v", got)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore'",
		"phase = 'restore_writer_fenced'",
		"held_writer_fence AS",
		"released_writer_fence AS",
		"UPDATE repo_fences SET status = 'released'",
		"updated_operation AS",
		"inserted_audit AS",
		"INSERT INTO audit_outbox",
	)
	for _, forbidden := range []string{"restore_plans", "restore_plan_id", "run_command"} {
		if strings.Contains(strings.ToLower(exec.query), strings.ToLower(forbidden)) {
			t.Fatalf("direct restore success SQL references forbidden fragment %q: %s", forbidden, exec.query)
		}
	}
}

func TestCommitRestoreFailedWithLeaseAllowsValidateOrWriterFenced(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		state     operations.OperationState
		phase     string
		sessionID string
		wantParts []string
	}{
		{
			name:      "validate terminates operation only",
			state:     operations.OperationStateFailed,
			phase:     operations.OperationPhaseRestoreValidate,
			wantParts: []string{"phase IN ('validate_restore','restore_writer_fenced')", "phase = 'validate_restore' AND $21 = ''", "INSERT INTO audit_outbox"},
		},
		{
			name:      "writer fenced releases fence",
			state:     operations.OperationStateFailed,
			phase:     operations.OperationPhaseRestoreWriterFenced,
			sessionID: "fence_restore01",
			wantParts: []string{"phase = 'restore_writer_fenced' AND session_fence_id = $21", "held_writer_fence AS", "$1 = 'failed'", "released_writer_fence AS", "UPDATE repo_fences SET status = 'released'", "EXISTS (SELECT 1 FROM released_writer_fence)", "INSERT INTO audit_outbox"},
		},
		{
			name:      "writer fenced operator intervention retains fence",
			state:     operations.OperationStateOperatorInterventionRequired,
			phase:     operations.OperationPhaseRestoreWriterFenced,
			sessionID: "fence_restore01",
			wantParts: []string{"phase = 'restore_writer_fenced' AND session_fence_id = $21", "held_writer_fence AS", "$1 = 'failed'", "released_writer_fence AS", "$1 = 'operator_intervention_required'", "INSERT INTO audit_outbox"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := restoreOperationRecord(now, tt.state, tt.phase)
			record.SessionFenceID = tt.sessionID
			record.Error = &operations.OperationError{Code: "RESTORE_FAILED", Message: "restore failed", CorrelationID: record.CorrelationID, OperationID: record.ID}
			record.FinishedAt = &now
			exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
			st := &Store{exec: exec}

			_, err := st.CommitRestoreFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restoreAudit(record, audit.OutcomeFailed, now))
			if err != nil {
				t.Fatalf("CommitRestoreFailedWithLease: %v", err)
			}
			assertSQLContainsInOrder(t, exec.query, tt.wantParts...)
		})
	}
}

func TestRestoreCommitsRejectRawCommandsBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	for _, key := range []string{"plan_id", "run_command", "recommended_next_command", "mount_command"} {
		t.Run(key, func(t *testing.T) {
			record := restoreOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestoreCommitted)
			record.SessionFenceID = "fence_restore01"
			record.JVSJSONOutput = map[string]any{"restored_save_point_id": "sp_001", "previous_head": "sp_before", "new_head": "sp_001", key: "jvs restore --run plan_001"}
			record.FinishedAt = &now
			exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
			st := &Store{exec: exec}

			_, err := st.CommitRestoreSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restoreAudit(record, audit.OutcomeSucceeded, now))
			if err == nil || errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("CommitRestoreSucceededWithLease error = %v, want validation before SQL", err)
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for raw command output: %s", exec.query)
			}
		})
	}
	t.Run("external restore plan id", func(t *testing.T) {
		record := restoreOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestoreCommitted)
		record.SessionFenceID = "fence_restore01"
		record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
		record.JVSJSONOutput = map[string]any{"restored_save_point_id": "sp_001", "previous_head": "sp_before", "new_head": "sp_001"}
		record.FinishedAt = &now
		exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
		st := &Store{exec: exec}

		_, err := st.CommitRestoreSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restoreAudit(record, audit.OutcomeSucceeded, now))
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("CommitRestoreSucceededWithLease error = %v, want validation before SQL", err)
		}
		if exec.query != "" {
			t.Fatalf("issued SQL for restore plan external id: %s", exec.query)
		}
	})
}

func restoreOperationRecord(now time.Time, state operations.OperationState, phase string) operations.OperationRecord {
	lease := now.Add(time.Minute)
	started := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_restore01",
		Type:                operations.OperationRestore,
		State:               state,
		Phase:               phase,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &lease,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestore, "idem_restore").String(),
		IdempotencyKey:      "idem_restore",
		RequestHash:         "sha256:restore",
		CorrelationID:       "corr-restore",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		InputSummary:        map[string]any{"save_point_id": "sp_001"},
		ExternalResourceIDs: map[string]string{},
		StartedAt:           &started,
		CreatedAt:           now.Add(-time.Hour),
	}
}

func restoreWriterFence(record operations.OperationRecord, now time.Time) fences.Fence {
	return fences.Fence{
		ID:                record.SessionFenceID,
		RepoID:            record.RepoID,
		Kind:              fences.KindWriterSession,
		HolderOperationID: record.ID,
		Status:            fences.StatusActive,
		ExpiresAt:         now.Add(30 * time.Minute),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func restoreAudit(record operations.OperationRecord, outcome audit.Outcome, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{EventID: "evt_restore", Type: audit.EventTypeRestore, Time: now, CallerService: record.CallerService, AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID}, CorrelationID: record.CorrelationID, OperationID: record.ID, Resource: audit.Resource{Type: "repo", ID: record.RepoID, NamespaceID: record.NamespaceID}, Outcome: outcome, Reason: "restore"})
}
