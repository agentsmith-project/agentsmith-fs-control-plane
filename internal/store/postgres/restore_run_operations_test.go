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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func TestListRestoreRunOperationsForRecoveryScopesBeforeOrderAndLimit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreRunOperationRecord(now, operations.OperationStateQueued, operations.OperationPhaseRestoreRunValidate)
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{{values: operationRowValues(record)}}}}
	st := &Store{exec: exec}

	got, err := st.ListRestoreRunOperationsForRecovery(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("ListRestoreRunOperationsForRecovery: %v", err)
	}
	if len(got) != 1 || got[0].ID != record.ID {
		t.Fatalf("restore run candidates = %#v, want %q", got, record.ID)
	}

	assertSQLContainsInOrder(t, exec.query,
		"FROM operations",
		"operation_type = 'restore_run'",
		"phase IN ('validate_restore_run','restore_run_writer_fenced','restore_run_consuming')",
		"operation_state = 'cancel_requested' AND phase = 'validate_restore_run'",
		"ORDER BY created_at, operation_id LIMIT $2",
	)
	for _, forbidden := range []string{"UPDATE ", "INSERT ", "DELETE ", "FOR UPDATE"} {
		if strings.Contains(strings.ToUpper(exec.query), strings.ToUpper(forbidden)) {
			t.Fatalf("restore run recovery list must be read-only SELECT, got %s", exec.query)
		}
	}
}

func TestAcquireRestoreRunOperationLeaseAllowsMatchingActivePlanException(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreRunOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestoreRunWriterFenced)
	record.SessionFenceID = "fence_restore_run01"
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRestoreRunOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireRestoreRunOperationLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_run'",
		"phase IN ('validate_restore_run','restore_run_writer_fenced','restore_run_consuming')",
		"matching_restore_plan AS",
		"p.preview_operation_id = e.input_summary->>'preview_operation_id'",
		"((e.phase IN ('validate_restore_run','restore_run_writer_fenced') AND p.status = 'pending') OR (e.phase = 'restore_run_consuming' AND p.status = 'consuming'))",
		"earlier_jvs_mutation AS",
		"o.operation_type IN ('save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone')",
		"earlier_repo_lifecycle AS",
		"o.operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned', 'repo_purge')",
		"unrelated_active_restore_plan AS",
		"p.preview_operation_id <> e.input_summary->>'preview_operation_id'",
		"EXISTS (SELECT 1 FROM matching_restore_plan)",
		"NOT EXISTS (SELECT 1 FROM unrelated_active_restore_plan)",
	)
	if strings.Contains(exec.query, "NOT EXISTS (SELECT 1 FROM active_restore_plan)") {
		t.Fatalf("restore run acquire used blunt active restore plan gate instead of matching exception: %s", exec.query)
	}
}

func TestMarkRestoreRunWriterFencedWithLeaseCreatesOrConfirmsWriterFence(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreRunOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestoreRunWriterFenced)
	record.SessionFenceID = "fence_restore_run01"
	fence := restoreRunWriterFence(record, now)
	exec := &fakeExecutor{row: fakeRow{values: append(repoFenceRowValues(fence), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	gotFence, gotOperation, err := st.MarkRestoreRunWriterFencedWithLease(context.Background(), fence, record.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("MarkRestoreRunWriterFencedWithLease: %v", err)
	}
	if gotFence.ID != fence.ID || gotOperation.SessionFenceID != fence.ID || gotOperation.Phase != operations.OperationPhaseRestoreRunWriterFenced {
		t.Fatalf("writer fence mark = %#v/%#v", gotFence, gotOperation)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_run'",
		"phase = 'validate_restore_run'",
		"locked_repo AS",
		"FROM repos, eligible_operation",
		"FOR UPDATE",
		"held_lifecycle_fence AS",
		"fence_kind = 'lifecycle'",
		"active_writer_fence AS",
		"repo_fences.repo_id = locked_repo.repo_id",
		"fence_kind = 'writer_session'",
		"holder_operation_id = $12",
		"inserted_writer_fence AS",
		"INSERT INTO repo_fences",
		"FROM eligible_operation, locked_repo",
		"ON CONFLICT (repo_id, fence_kind) WHERE released_at IS NULL DO NOTHING",
		"confirmed_writer_fence AS",
		"updated_operation AS",
		"lease_owner = CASE WHEN $1 = 'running' THEN operations.lease_owner ELSE NULL END",
		"lease_expires_at = CASE WHEN $1 = 'running' THEN operations.lease_expires_at ELSE NULL END",
		"started_at = COALESCE(operations.started_at, $9, $11)",
		"session_fence_id = $21",
		"NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)",
	)
	for _, forbidden := range []string{
		"THEN lease_owner ELSE NULL END",
		"THEN lease_expires_at ELSE NULL END",
		"COALESCE(started_at, $9, $11)",
	} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("restore run writer-fence SQL preserves operation fields with ambiguous source column %q: %s", forbidden, exec.query)
		}
	}
}

func TestMarkRestoreRunConsumingWithLeaseCASPlanAndConfirmsFenceAndPreview(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreRunOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestoreRunConsuming)
	record.SessionFenceID = "fence_restore_run01"
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "restore_plan_status": "consuming"}
	plan := restorePreviewPlan(restorePreviewOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewCommitted), now)
	plan.Status = restoreplan.StatusConsuming
	exec := &fakeExecutor{row: fakeRow{values: append(restorePlanRowValues(plan), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.MarkRestoreRunConsumingWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("MarkRestoreRunConsumingWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_run'",
		"phase = 'restore_run_writer_fenced'",
		"active_writer_fence AS",
		"fence_kind = 'writer_session'",
		"preview_operation AS",
		"operation_type = 'restore_preview'",
		"operation_state = 'succeeded'",
		"pending_restore_plan AS",
		"status = 'pending'",
		"stale = false",
		"UPDATE restore_plans SET status = 'consuming'",
		"updated_operation AS",
	)
}

func TestMarkRestoreRunConsumingWithLeaseRejectsStalePendingPlanInSQL(t *testing.T) {
	sql := restoreRunConsumingMarkWithLeaseSQL()

	assertSQLContainsInOrder(t, sql,
		"pending_restore_plan AS",
		"status = 'pending'",
		"stale = false",
		"UPDATE restore_plans SET status = 'consuming'",
	)
}

func TestCommitRestoreRunSucceededWithLeaseConsumesPlanAuditAndReleasesFenceAtomically(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreRunOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestoreRunCommitted)
	record.SessionFenceID = "fence_restore_run01"
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	record.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", "workspace": "main", "restore_applied": true}
	record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "restore_plan_status": "consumed"}
	record.FinishedAt = &now
	plan := restorePreviewPlan(restorePreviewOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewCommitted), now)
	plan.Status = restoreplan.StatusConsumed
	exec := &fakeExecutor{row: fakeRow{values: append(restorePlanRowValues(plan), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.CommitRestoreRunSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restoreRunAudit(record, audit.OutcomeSucceeded, now))
	if err != nil {
		t.Fatalf("CommitRestoreRunSucceededWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_run'",
		"phase = 'restore_run_consuming'",
		"consuming_restore_plan AS",
		"status = 'consuming'",
		"held_writer_fence AS",
		"released_writer_fence AS",
		"UPDATE repo_fences SET status = 'released'",
		"updated_plan AS",
		"UPDATE restore_plans SET status = 'consumed'",
		"FROM consuming_restore_plan, released_writer_fence",
		"updated_operation AS",
		"FROM eligible_operation, updated_plan, released_writer_fence",
		"inserted_audit AS",
		"INSERT INTO audit_outbox",
		"FROM updated_operation, updated_plan, released_writer_fence",
	)
}

func TestCommitRestoreRunFailedWithLeaseHandlesValidateWriterFencedAndConsumingBoundaries(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		phase      string
		sessionID  string
		wantParts  []string
		statusCode string
	}{
		{
			name:       "validate terminates operation only",
			phase:      operations.OperationPhaseRestoreRunValidate,
			statusCode: "RESTORE_RUN_PLAN_INVALID",
			wantParts:  []string{"phase IN ('validate_restore_run','restore_run_writer_fenced','restore_run_consuming')", "eligible_operation.phase = 'validate_restore_run'", "INSERT INTO audit_outbox"},
		},
		{
			name:       "writer fenced releases fence and keeps pending plan",
			phase:      operations.OperationPhaseRestoreRunWriterFenced,
			sessionID:  "fence_restore_run01",
			statusCode: "RESTORE_RUN_WRITER_DENIED",
			wantParts:  []string{"pending_restore_plan AS", "status = 'pending'", "held_writer_fence AS", "released_writer_fence AS", "UPDATE repo_fences SET status = 'released'", "FROM held_writer_fence, pending_restore_plan", "updated_operation AS", "eligible_operation.phase = 'restore_run_writer_fenced'", "EXISTS (SELECT 1 FROM released_writer_fence)", "inserted_audit AS"},
		},
		{
			name:       "consuming moves plan to operator intervention and keeps fence",
			phase:      operations.OperationPhaseRestoreRunConsuming,
			sessionID:  "fence_restore_run01",
			statusCode: "RESTORE_RUN_AMBIGUOUS",
			wantParts:  []string{"consuming_restore_plan AS", "status = 'consuming'", "UPDATE restore_plans SET status = 'operator_intervention_required'", "eligible_operation.phase = 'restore_run_consuming'"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := restoreRunOperationRecord(now, operations.OperationStateOperatorInterventionRequired, tt.phase)
			record.SessionFenceID = tt.sessionID
			record.Error = &operations.OperationError{Code: tt.statusCode, Message: "restore run failed", CorrelationID: record.CorrelationID, OperationID: record.ID}
			record.FinishedAt = &now
			exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
			st := &Store{exec: exec}

			_, err := st.CommitRestoreRunFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restoreRunAudit(record, audit.OutcomeFailed, now))
			if err != nil {
				t.Fatalf("CommitRestoreRunFailedWithLease: %v", err)
			}

			assertSQLContainsInOrder(t, exec.query, tt.wantParts...)
		})
	}
}

func TestCommitRestoreRunStalePreviewWithLeaseMarksPlanStaleAndFailsOperationAtomically(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreRunOperationRecord(now, operations.OperationStateFailed, operations.OperationPhaseRestoreRunValidate)
	record.Error = &operations.OperationError{Code: "RESTORE_PREVIEW_STALE", Message: "restore preview is stale", CorrelationID: record.CorrelationID, OperationID: record.ID}
	record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "stale": true, "blockers": []any{map[string]any{"code": "restore_preview_stale"}}}
	record.FinishedAt = &now
	plan := restorePreviewPlan(restorePreviewOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewCommitted), now)
	plan.Stale = true
	plan.Blockers = []restoreplan.Blocker{{Code: "restore_preview_stale", Message: "Preview is stale; discard and create a new preview before running restore."}}
	exec := &fakeExecutor{row: fakeRow{values: append(restorePlanRowValues(plan), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	gotPlan, gotOperation, err := st.CommitRestoreRunStalePreviewWithLease(context.Background(), plan, record.SanitizedForPersistence(), "worker-a", now, restoreRunAudit(record, audit.OutcomeFailed, now))
	if err != nil {
		t.Fatalf("CommitRestoreRunStalePreviewWithLease: %v", err)
	}
	if !gotPlan.Stale || len(gotPlan.Blockers) != 1 || gotPlan.Blockers[0].Code != "restore_preview_stale" || gotOperation.State != operations.OperationStateFailed {
		t.Fatalf("plan/operation = %#v/%#v, want stale plan source of truth and failed operation", gotPlan, gotOperation)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_run'",
		"phase = 'validate_restore_run'",
		"pending_restore_plan AS",
		"status = 'pending'",
		"updated_plan AS",
		"UPDATE restore_plans SET stale = $22, blockers_json = $23, updated_at = $11",
		"updated_operation AS",
		"inserted_audit AS",
		"INSERT INTO audit_outbox",
		"FROM updated_operation, updated_plan",
	)
}

func TestRestoreRunCommitsRejectRawCommandsBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	for _, key := range []string{"recommended_next_command", "mount_command", "raw_mount_command", "direct_mount_command"} {
		t.Run(key, func(t *testing.T) {
			record := restoreRunOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestoreRunCommitted)
			record.SessionFenceID = "fence_restore_run01"
			record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
			record.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", key: "juicefs mount repo_main /mnt/workspace"}
			record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "restore_plan_status": "consumed"}
			record.FinishedAt = &now
			exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
			st := &Store{exec: exec}

			_, _, err := st.CommitRestoreRunSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restoreRunAudit(record, audit.OutcomeSucceeded, now))
			if err == nil || errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("CommitRestoreRunSucceededWithLease error = %v, want validation before SQL", err)
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for raw command output: %s", exec.query)
			}
		})
	}
}

func TestRestoreRunCommitsRejectRawCommandAuditDetailsBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restoreRunOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestoreRunCommitted)
	record.SessionFenceID = "fence_restore_run01"
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	record.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", "workspace": "main", "restore_applied": true}
	record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "restore_plan_status": "consumed"}
	record.FinishedAt = &now
	tests := []struct {
		name    string
		details map[string]any
	}{
		{name: "top-level", details: map[string]any{"recommended_next_command": "jvs restore run plan_001"}},
		{name: "nested map string string", details: map[string]any{"jvs": map[string]string{"run_command": "jvs restore run plan_001"}}},
		{name: "mount command", details: map[string]any{"mount_command": "juicefs mount repo_main /mnt/workspace"}},
		{name: "raw mount command", details: map[string]any{"jvs": map[string]string{"raw_mount_command": "juicefs mount repo_raw /mnt/raw"}}},
		{name: "direct mount command", details: map[string]any{"commands": []any{map[string]any{"direct_mount_command": "juicefs mount repo_direct /mnt/direct"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := restoreRunAudit(record, audit.OutcomeSucceeded, now)
			event.Details = tt.details
			exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
			st := &Store{exec: exec}

			_, _, err := st.CommitRestoreRunSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, event)
			if err == nil || errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("CommitRestoreRunSucceededWithLease error = %v, want audit validation before SQL", err)
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for raw command audit details: %s", exec.query)
			}
		})
	}
}

func restoreRunOperationRecord(now time.Time, state operations.OperationState, phase string) operations.OperationRecord {
	lease := now.Add(time.Minute)
	started := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_restore_run01",
		Type:                operations.OperationRestoreRun,
		State:               state,
		Phase:               phase,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &lease,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestoreRun, "idem_restore_run").String(),
		IdempotencyKey:      "idem_restore_run",
		RequestHash:         "sha256:restore-run",
		CorrelationID:       "corr-restore-run",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		InputSummary:        map[string]any{"preview_operation_id": "op_preview01"},
		ExternalResourceIDs: map[string]string{},
		StartedAt:           &started,
		CreatedAt:           now.Add(-time.Hour),
	}
}

func restoreRunWriterFence(record operations.OperationRecord, now time.Time) fences.Fence {
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

func restoreRunAudit(record operations.OperationRecord, outcome audit.Outcome, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{EventID: "evt_restore_run", Type: audit.EventTypeRestoreRun, Time: now, CallerService: record.CallerService, AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID}, CorrelationID: record.CorrelationID, OperationID: record.ID, Resource: audit.Resource{Type: "repo", ID: record.RepoID, NamespaceID: record.NamespaceID}, Outcome: outcome, Reason: "restore_run"})
}
