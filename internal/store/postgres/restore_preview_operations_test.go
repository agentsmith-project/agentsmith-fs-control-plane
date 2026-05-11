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

func TestAcquireRestorePreviewOperationLeaseSerializesEarlierMutationsAndPlans(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestorePreviewValidate)
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRestorePreviewOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireRestorePreviewOperationLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_preview'",
		"phase IN ('validate_restore_preview','restore_preview_preflight_idle')",
		"earlier_jvs_mutation AS",
		"o.operation_id <> e.operation_id",
		"o.operation_type IN ('save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone')",
		"earlier_repo_lifecycle AS",
		"o.operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned', 'repo_purge')",
		"active_restore_plan AS",
		"FROM restore_plans p, eligible_operation e",
		"p.status IN ('pending', 'consuming', 'discarding', 'operator_intervention_required')",
		"NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle)",
		"NOT EXISTS (SELECT 1 FROM active_restore_plan)",
		"RETURNING",
	)
}

func TestAcquireRestorePreviewOperationLeaseFinalizesOnlyValidatePhaseCancellation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewOperationRecord(now, operations.OperationStateCancelled, operations.OperationPhaseRestorePreviewValidate)
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRestorePreviewOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now, CancelPolicy: operations.LeaseCancelPolicyFinalize})
	if err != nil {
		t.Fatalf("AcquireRestorePreviewOperationLease finalize: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"operation_type = 'restore_preview'",
		"phase IN ('validate_restore_preview','restore_preview_preflight_idle')",
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled'",
		"$5 <> 'finalize_cancellation' OR eligible_operation.phase = 'validate_restore_preview'",
	)
	if strings.Contains(exec.query, "operator_intervention_required' AND $5") {
		t.Fatalf("restore preview typed acquire must not explicitly recover operator intervention: %s", exec.query)
	}
}

func TestUpdateRestorePreviewPreflightWithLeaseRequiresSafeMarkerAndStoredValidate(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewOperationRecord(now, operations.OperationStateRunning, operations.OperationPhaseRestorePreviewPreflightIdle)
	record.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false}
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.UpdateRestorePreviewPreflightWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now)
	if err != nil {
		t.Fatalf("UpdateRestorePreviewPreflightWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"UPDATE operations SET",
		"operation_type = 'restore_preview'",
		"phase = 'validate_restore_preview'",
		"RETURNING",
	)
	if got := mustJSONMap(t, exec.args[5])["preflight_recovery_status_captured"]; got != true {
		t.Fatalf("verification marker arg = %#v, want captured true", got)
	}

	invalid := record
	invalid.VerificationResult = nil
	exec = &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st = &Store{exec: exec}
	_, err = st.UpdateRestorePreviewPreflightWithLease(context.Background(), invalid.SanitizedForPersistence(), "worker-a", now)
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("UpdateRestorePreviewPreflightWithLease error = %v, want validation before SQL", err)
	}
	if exec.query != "" {
		t.Fatalf("issued SQL for missing preflight marker: %s", exec.query)
	}
}

func TestCommitRestorePreviewSucceededWithLeaseInsertsPlanAuditAndOperationAtomically(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	planID := "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7"
	sourceSavePointID := "1778482397674-64d97186"
	headSavePointID := "1778482397734-bc8dc9ff"
	generation := "4395c73549f237da40314869dd1d0f86db76bd4855d79d3df7b3a31075788abd"
	record := restorePreviewOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewCommitted)
	record.InputSummary = map[string]any{"save_point_id": sourceSavePointID}
	record.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false, "restore_plan_id": planID, "source_save_point_id": sourceSavePointID, "base_revision": headSavePointID, "head_revision": headSavePointID, "generation": generation}
	record.JVSJSONOutput = map[string]any{"restore_plan_id": planID, "source_save_point_id": sourceSavePointID, "base_revision": headSavePointID, "head_revision": headSavePointID, "generation": generation, "workspace": "main", "run_command_present": true}
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": planID}
	record.FinishedAt = &now
	plan := restorePreviewPlan(record, now)
	plan.ID = planID
	plan.SourceSavePointID = sourceSavePointID
	plan.BaseRevision = headSavePointID
	plan.HeadRevision = headSavePointID
	plan.Generation = generation
	exec := &fakeExecutor{row: fakeRow{values: append(restorePlanRowValues(plan), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	gotPlan, gotOperation, err := st.CommitRestorePreviewSucceededWithLease(context.Background(), plan, record.SanitizedForPersistence(), "worker-a", now, restorePreviewAudit(record, audit.OutcomeSucceeded, now))
	if err != nil {
		t.Fatalf("CommitRestorePreviewSucceededWithLease: %v", err)
	}
	if gotPlan.ID != planID || gotPlan.SourceSavePointID != sourceSavePointID || gotOperation.ID != record.ID || gotOperation.State != operations.OperationStateSucceeded {
		t.Fatalf("commit return = %#v/%#v, want durable plan and succeeded operation", gotPlan, gotOperation)
	}
	if got := exec.args[19]; got != planID {
		t.Fatalf("restore_plan_id insert arg = %#v, want real JVS UUID plan id", got)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'restore_preview'",
		"phase = 'restore_preview_preflight_idle'",
		"verification_result->>'preflight_recovery_status_captured' = 'true'",
		"updated_operation AS",
		"INSERT INTO restore_plans",
		"inserted_audit AS",
		"INSERT INTO audit_outbox",
	)
	if strings.Contains(exec.query, "CreatePendingRestorePlan") {
		t.Fatalf("typed commit composed through generic restore plan helper: %s", exec.query)
	}
}

func TestRestorePreviewSuccessCommitSQLQualifiesPlanOperationReturnColumns(t *testing.T) {
	query := restorePreviewSuccessCommitWithLeaseSQL()
	want := "SELECT inserted_restore_plan.restore_plan_id, inserted_restore_plan.namespace_id, inserted_restore_plan.repo_id"
	if !strings.Contains(query, want) {
		t.Fatalf("restore preview success SQL must qualify restore plan columns to avoid ambiguous postgres columns\nwant fragment: %s\nquery: %s", want, query)
	}
	if !strings.Contains(query, "updated_operation.operation_id") {
		t.Fatalf("restore preview success SQL must qualify operation columns in final projection: %s", query)
	}
	if strings.Contains(query, ") SELECT "+strings.Join(restorePlanColumns, ", ")+", "+strings.Join(operationSelectColumns, ", ")+" FROM inserted_restore_plan, updated_operation") {
		t.Fatalf("restore preview success SQL uses ambiguous unqualified plan/operation projection: %s", query)
	}
}

func TestCommitRestorePreviewSucceededRejectsRawCommandsAndPlanMismatchBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewOperationRecord(now, operations.OperationStateSucceeded, operations.OperationPhaseRestorePreviewCommitted)
	record.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false, "restore_plan_id": "plan_001", "source_save_point_id": "sp_001"}
	record.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001", "run_command": "jvs restore --run plan_001"}
	record.FinishedAt = &now
	plan := restorePreviewPlan(record, now)
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}

	_, _, err := st.CommitRestorePreviewSucceededWithLease(context.Background(), plan, record.SanitizedForPersistence(), "worker-a", now, restorePreviewAudit(record, audit.OutcomeSucceeded, now))
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitRestorePreviewSucceededWithLease error = %v, want validation before SQL", err)
	}
	if exec.query != "" {
		t.Fatalf("issued SQL for raw command output: %s", exec.query)
	}

	record.JVSJSONOutput = map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001", "run_command_present": true}
	plan.RepoID = "repo_other"
	_, _, err = st.CommitRestorePreviewSucceededWithLease(context.Background(), plan, record.SanitizedForPersistence(), "worker-a", now, restorePreviewAudit(record, audit.OutcomeSucceeded, now))
	if err == nil {
		t.Fatal("CommitRestorePreviewSucceededWithLease accepted mismatched plan")
	}
}

func TestCommitRestorePreviewFailedWithLeaseAllowsValidateOrPreflightAndAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := restorePreviewOperationRecord(now, operations.OperationStateOperatorInterventionRequired, operations.OperationPhaseRestorePreviewPreflightIdle)
	record.Error = &operations.OperationError{Code: "RESTORE_PREVIEW_RECOVERY_AMBIGUOUS", Message: "restore preview recovery ambiguous", CorrelationID: record.CorrelationID, OperationID: record.ID}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.CommitRestorePreviewFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, restorePreviewAudit(record, audit.OutcomeFailed, now))
	if err != nil {
		t.Fatalf("CommitRestorePreviewFailedWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"operation_type = 'restore_preview'",
		"phase IN ('validate_restore_preview','restore_preview_preflight_idle')",
		"INSERT INTO audit_outbox",
	)
}

func restorePreviewOperationRecord(now time.Time, state operations.OperationState, phase string) operations.OperationRecord {
	lease := now.Add(time.Minute)
	started := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_preview01",
		Type:                operations.OperationRestorePreview,
		State:               state,
		Phase:               phase,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &lease,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String(),
		IdempotencyKey:      "idem_preview",
		RequestHash:         "sha256:restore-preview",
		CorrelationID:       "corr-preview",
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

func restorePreviewPlan(record operations.OperationRecord, now time.Time) restoreplan.Plan {
	return restoreplan.Plan{
		ID:                 "plan_001",
		NamespaceID:        record.NamespaceID,
		RepoID:             record.RepoID,
		PreviewOperationID: record.ID,
		SourceSavePointID:  "sp_001",
		BaseRevision:       "sp_002",
		HeadRevision:       "sp_002",
		Generation:         "sha256:preview-base",
		FenceMarker:        "preview_fence_" + record.ID,
		Summary: restoreplan.Summary{
			Added:       restoreplan.ChangeSummary{Count: 1, Samples: []string{"src/new.ts"}},
			Changed:     restoreplan.ChangeSummary{Count: 1, Samples: []string{"docs/readme.md"}},
			Removed:     restoreplan.ChangeSummary{Count: 1, Samples: []string{"tmp/cache.txt"}},
			Destructive: true,
		},
		Blockers:  []restoreplan.Blocker{},
		Stale:     false,
		Status:    restoreplan.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func restorePreviewAudit(record operations.OperationRecord, outcome audit.Outcome, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{EventID: "evt_restore_preview", Type: audit.EventTypeRestorePreview, Time: now, CallerService: record.CallerService, AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID}, CorrelationID: record.CorrelationID, OperationID: record.ID, Resource: audit.Resource{Type: "repo", ID: record.RepoID, NamespaceID: record.NamespaceID}, Outcome: outcome, Reason: "restore_preview"})
}
