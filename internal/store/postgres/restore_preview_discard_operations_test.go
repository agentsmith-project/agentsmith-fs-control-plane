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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func TestListRestorePreviewDiscardOperationsForRecoveryScopesBeforeOrderAndLimit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateQueued, operations.OperationPhaseRestorePreviewDiscardValidate)
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	exec := &fakeExecutor{rows: fakeRows{rows: []fakeRow{{values: operationRowValues(record)}}}}
	st := &Store{exec: exec}

	got, err := st.ListRestorePreviewDiscardOperationsForRecovery(context.Background(), now, 1)
	if err != nil {
		t.Fatalf("ListRestorePreviewDiscardOperationsForRecovery: %v", err)
	}
	if len(got) != 1 || got[0].ID != "op_discard01" {
		t.Fatalf("records = %#v, want op_discard01", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"FROM operations",
		"operation_type = 'restore_preview_discard'",
		"phase IN ('validate_restore_preview_discard','restore_preview_discarding')",
		"operation_state = 'queued'",
		"operation_state = 'running'",
		"operation_state = 'cancel_requested' AND phase = 'validate_restore_preview_discard'",
		"operator_intervention_required",
		"ORDER BY created_at, operation_id",
		"LIMIT $2",
	)
	if strings.Contains(exec.query, "UPDATE ") || strings.Contains(exec.query, " FOR UPDATE") {
		t.Fatalf("restore preview discard recovery list must be read-only SELECT, got %s", exec.query)
	}
}

func TestAcquireRestorePreviewDiscardOperationLeaseAllowsOnlyMatchingActivePlanException(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestorePreviewDiscardValidate)
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRestorePreviewDiscardOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireRestorePreviewDiscardOperationLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_preview_discard'",
		"phase IN ('validate_restore_preview_discard','restore_preview_discarding')",
		"matching_restore_plan AS",
		"p.preview_operation_id = e.input_summary->>'preview_operation_id'",
		"p.namespace_id = e.namespace_id",
		"p.repo_id = e.repo_id",
		"((e.phase = 'validate_restore_preview_discard' AND p.status = 'pending') OR (e.phase = 'restore_preview_discarding' AND p.status = 'discarding'))",
		"earlier_jvs_mutation AS",
		"o.operation_id <> e.operation_id",
		"NOT (o.operation_id = e.input_summary->>'preview_operation_id' AND o.operation_type = 'restore_preview' AND o.operation_state = 'succeeded')",
		"earlier_repo_lifecycle AS",
		"unrelated_active_restore_plan AS",
		"p.preview_operation_id <> e.input_summary->>'preview_operation_id'",
		"EXISTS (SELECT 1 FROM matching_restore_plan)",
		"NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle)",
		"NOT EXISTS (SELECT 1 FROM unrelated_active_restore_plan)",
		"RETURNING",
	)
	if strings.Contains(exec.query, "NOT EXISTS (SELECT 1 FROM active_restore_plan)") {
		t.Fatalf("discard acquire used blunt active plan gate instead of matching-plan exception: %s", exec.query)
	}
}

func TestAcquireRestorePreviewDiscardOperationLeaseFinalizesOnlyValidatePhaseCancellation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateCancelled, operations.OperationPhaseRestorePreviewDiscardValidate)
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRestorePreviewDiscardOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now, CancelPolicy: operations.LeaseCancelPolicyFinalize})
	if err != nil {
		t.Fatalf("AcquireRestorePreviewDiscardOperationLease finalize: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"operation_type = 'restore_preview_discard'",
		"phase IN ('validate_restore_preview_discard','restore_preview_discarding')",
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled'",
		"$5 <> 'finalize_cancellation' OR eligible_operation.phase = 'validate_restore_preview_discard'",
	)
}

func TestMarkRestorePreviewDiscardingWithLeaseCASPlanAndOperationAtomically(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestorePreviewDiscarding)
	record.VerificationResult = map[string]any{"preview_operation_id": "op_preview01", "restore_plan_id": "plan_001", "restore_plan_status": "discarding"}
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	inputPlan := restorePreviewPlan(restorePreviewOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewCommitted), now)
	returnedPlan := inputPlan
	returnedPlan.Status = restoreplan.StatusDiscarding
	returnedPlan.UpdatedAt = now
	exec := &fakeExecutor{row: fakeRow{values: append(restorePlanRowValues(returnedPlan), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.MarkRestorePreviewDiscardingWithLease(context.Background(), inputPlan, record.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("MarkRestorePreviewDiscardingWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_preview_discard'",
		"phase = 'validate_restore_preview_discard'",
		"preview_operation AS",
		"operation_type = 'restore_preview'",
		"operation_state = 'succeeded'",
		"phase = 'restore_preview_committed'",
		"pending_restore_plan AS",
		"status = 'pending'",
		"updated_plan AS",
		"UPDATE restore_plans SET status = 'discarding'",
		"updated_operation AS",
		"SELECT",
	)
}

func TestCommitRestorePreviewDiscardSucceededWithLeaseDiscardsPlanAuditAndOperationAtomically(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewDiscardCommitted)
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	record.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", "plan_discarded": true, "workspace": "main"}
	record.VerificationResult = map[string]any{"preview_operation_id": "op_preview01", "restore_plan_id": "plan_001", "restore_plan_status": "discarded"}
	record.FinishedAt = &now
	plan := restorePreviewPlan(restorePreviewOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewCommitted), now)
	plan.Status = restoreplan.StatusDiscarded
	exec := &fakeExecutor{row: fakeRow{values: append(restorePlanRowValues(plan), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.CommitRestorePreviewDiscardSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restorePreviewDiscardAudit(record, audit.OutcomeSucceeded, now))
	if err != nil {
		t.Fatalf("CommitRestorePreviewDiscardSucceededWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_preview_discard'",
		"phase = 'restore_preview_discarding'",
		"discarding_restore_plan AS",
		"status = 'discarding'",
		"updated_plan AS",
		"UPDATE restore_plans SET status = 'discarded'",
		"updated_operation AS",
		"inserted_audit AS",
		"INSERT INTO audit_outbox",
	)
	if strings.Contains(exec.query, "operator_intervention_required") {
		t.Fatalf("restore preview discard success commit must not retain a manual blocker path: %s", exec.query)
	}
}

func TestCommitRestorePreviewDiscardFailedWithLeaseMovesDiscardingPlanToOperatorIntervention(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateOperatorInterventionRequired, operations.OperationPhaseRestorePreviewDiscarding)
	record.Error = &operations.OperationError{Code: "RESTORE_PREVIEW_DISCARD_AMBIGUOUS", Message: "restore preview discard ambiguous", CorrelationID: record.CorrelationID, OperationID: record.ID}
	record.VerificationResult = map[string]any{"preview_operation_id": "op_preview01", "restore_plan_id": "plan_001"}
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.CommitRestorePreviewDiscardFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restorePreviewDiscardAudit(record, audit.OutcomeFailed, now))
	if err != nil {
		t.Fatalf("CommitRestorePreviewDiscardFailedWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_preview_discard'",
		"phase IN ('validate_restore_preview_discard','restore_preview_discarding')",
		"discarding_restore_plan AS",
		"status = 'discarding'",
		"updated_plan AS",
		"UPDATE restore_plans SET status = 'operator_intervention_required'",
		"inserted_audit AS",
	)
}

func TestCommitRestorePreviewDiscardFailedWithLeaseValidatePhaseDoesNotRequirePlanRow(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateOperatorInterventionRequired, operations.OperationPhaseRestorePreviewDiscardValidate)
	record.Error = &operations.OperationError{Code: "RESTORE_PREVIEW_DISCARD_PLAN_INVALID", Message: "restore preview discard plan invalid", CorrelationID: record.CorrelationID, OperationID: record.ID}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	got, err := st.CommitRestorePreviewDiscardFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restorePreviewDiscardAudit(record, audit.OutcomeFailed, now))
	if err != nil {
		t.Fatalf("CommitRestorePreviewDiscardFailedWithLease validate no plan: %v", err)
	}
	if got.ID != record.ID || got.State != operations.OperationStateOperatorInterventionRequired || got.Phase != operations.OperationPhaseRestorePreviewDiscardValidate {
		t.Fatalf("operation = %#v, want validate operator intervention", got)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_preview_discard'",
		"phase IN ('validate_restore_preview_discard','restore_preview_discarding')",
		"updated_operation AS",
		"eligible_operation.phase = 'validate_restore_preview_discard'",
		"inserted_audit AS",
		"INSERT INTO audit_outbox",
	)
	if strings.Contains(exec.query, "returned_plan") {
		t.Fatalf("validate failure commit should not require returned_plan row: %s", exec.query)
	}
}

func TestCommitRestorePreviewDiscardRejectsRawCommandsAndPlanMismatchBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewDiscardOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewDiscardCommitted)
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	record.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", "recommended_next_command": "jvs restore discard plan_001"}
	record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "restore_plan_status": "discarded"}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, _, err := st.CommitRestorePreviewDiscardSucceededWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restorePreviewDiscardAudit(record, audit.OutcomeSucceeded, now))
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitRestorePreviewDiscardSucceededWithLease error = %v, want validation before SQL", err)
	}
	if exec.query != "" {
		t.Fatalf("issued SQL for raw command output: %s", exec.query)
	}
}

func restorePreviewDiscardOperationRecord(now time.Time, state operations.OperationState, phase string) operations.OperationRecord {
	lease := now.Add(time.Minute)
	started := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_discard01",
		Type:                operations.OperationRestorePreviewDiscard,
		State:               state,
		Phase:               phase,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &lease,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestorePreviewDiscard, "idem_discard").String(),
		IdempotencyKey:      "idem_discard",
		RequestHash:         "sha256:restore-preview-discard",
		CorrelationID:       "corr-discard",
		CallerService:       "product-caller",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		InputSummary:        map[string]any{"preview_operation_id": "op_preview01"},
		ExternalResourceIDs: map[string]string{},
		StartedAt:           &started,
		CreatedAt:           now.Add(-30 * time.Minute),
	}
}

func restorePreviewDiscardAudit(record operations.OperationRecord, outcome audit.Outcome, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{EventID: "evt_restore_preview_discard", Type: audit.EventTypeRestorePreviewDiscard, Time: now, CallerService: record.CallerService, AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID}, CorrelationID: record.CorrelationID, OperationID: record.ID, Resource: audit.Resource{Type: "repo", ID: record.RepoID, NamespaceID: record.NamespaceID}, Outcome: outcome, Reason: "restore_preview_discard"})
}
