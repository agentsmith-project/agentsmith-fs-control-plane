package repoexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func TestRestorePreviewDiscardExecutorMarksPlanDiscardingBeforeJVSAndCommitsDiscarded(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
		restoreDiscardSummary: jvsrunner.RestoreDiscardSummary{PlanID: "plan_001", PlanDiscarded: true, Workspace: "main"},
	}
	runner.beforeRestoreDiscard = func() {
		if store.restorePreviewDiscardProgressUpdates != 1 || store.restorePlan.Status != restoreplan.StatusDiscarding {
			t.Fatalf("RestoreDiscard called before durable discarding mark, progress/plan = %d/%#v", store.restorePreviewDiscardProgressUpdates, store.restorePlan)
		}
	}
	executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_discard" {
		t.Fatalf("JVS calls = %#v, want recovery_status then restore_discard", runner.calls)
	}
	if store.restorePlan.Status != restoreplan.StatusDiscarded || store.restorePlan.ID != "plan_001" {
		t.Fatalf("restore plan = %#v, want discarded plan_001", store.restorePlan)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestorePreviewDiscardCommitted {
		t.Fatalf("operation = %#v, want succeeded discard committed", store.operation)
	}
	if got := store.operation.InputSummary["preview_operation_id"]; got != "op_preview01" {
		t.Fatalf("input summary preview_operation_id = %#v, want op_preview01", got)
	}
	jvsOutput := store.operation.JVSJSONOutput.(map[string]any)
	for _, forbidden := range []string{"run_command", "recommended_next_command"} {
		if _, ok := jvsOutput[forbidden]; ok {
			t.Fatalf("jvs output persisted forbidden command field %q: %#v", forbidden, jvsOutput)
		}
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestorePreviewDiscard || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want restore preview discard success", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestRestorePreviewDiscardExecutorAllowsMatchingPendingOrStaleRecoveryState(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name   string
		status jvsrunner.RecoveryStatusSummary
	}{
		{
			name:   "pending with active recovery plan id",
			status: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", ActiveRecoveryPlanID: "recovery_001", Blocking: true, Workspace: "main"},
		},
		{
			name:   "stale matching plan",
			status: jvsrunner.RecoveryStatusSummary{RestoreState: "stale_restore_preview", ActivePlanID: "plan_001", ActiveRecoveryPlanID: "recovery_001", Blocking: true, Workspace: "main"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			store.previewOperation = restorePreviewSucceededOperationRecord(now)
			store.restorePlan = restorePreviewPendingPlan(now)
			runner := &fakeJVSRunner{
				recoveryStatusSummary: tt.status,
				restoreDiscardSummary: jvsrunner.RestoreDiscardSummary{PlanID: "plan_001", PlanDiscarded: true, Workspace: "main"},
			}
			executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if strings.Join(runner.calls, ",") != "recovery_status,restore_discard" {
				t.Fatalf("JVS calls = %#v, want recovery_status then restore_discard", runner.calls)
			}
			if store.restorePlan.Status != restoreplan.StatusDiscarded || store.operation.State != operations.OperationStateSucceeded {
				t.Fatalf("plan/operation = %#v/%#v, want discarded/succeeded", store.restorePlan, store.operation)
			}
		})
	}
}

func TestRestorePreviewDiscardExecutorAllowsDisabledNamespaceCleanupAndCommitsDiscarded(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.namespace.Status = resources.NamespaceStatusDisabled
	disabledAt := now.Add(-time.Minute)
	store.namespace.DisabledAt = &disabledAt
	store.namespace.DisabledReason = "maintenance"
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
		restoreDiscardSummary: jvsrunner.RestoreDiscardSummary{PlanID: "plan_001", PlanDiscarded: true, Workspace: "main"},
	}
	executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_discard" {
		t.Fatalf("JVS calls = %#v, want recovery_status then restore_discard", runner.calls)
	}
	if store.restorePlan.Status != restoreplan.StatusDiscarded || store.operation.State != operations.OperationStateSucceeded {
		t.Fatalf("plan/operation = %#v/%#v, want discarded/succeeded cleanup", store.restorePlan, store.operation)
	}
}

func TestRestorePreviewDiscardExecutorRejectsCleanupAdmissionRisksBeforeJVS(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name string
		edit func(*fakeRepoCreateStore)
	}{
		{name: "disabled binding", edit: func(store *fakeRepoCreateStore) {
			store.binding.Status = resources.NamespaceStatusDisabled
		}},
		{name: "archived repo", edit: func(store *fakeRepoCreateStore) {
			store.repo.Status = resources.RepoStatusArchived
			store.repo.Lifecycle.Status = resources.RepoStatusArchived
		}},
		{name: "lifecycle fence", edit: func(store *fakeRepoCreateStore) {
			store.fences = []fences.Fence{repoCreateFence(now, "fence_lifecycle", "op_lifecycle")}
		}},
		{name: "invalid volume", edit: func(store *fakeRepoCreateStore) {
			store.volume.Status = resources.VolumeStatusDisabled
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			store.previewOperation = restorePreviewSucceededOperationRecord(now)
			store.restorePlan = restorePreviewPendingPlan(now)
			tt.edit(store)
			runner := &fakeJVSRunner{}
			executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if len(runner.calls) != 0 || store.restorePreviewDiscardProgressUpdates != 0 {
				t.Fatalf("JVS/progress = %#v/%d, want denied before JVS", runner.calls, store.restorePreviewDiscardProgressUpdates)
			}
			if store.operation.State != operations.OperationStateFailed || store.restorePlan.Status != restoreplan.StatusPending {
				t.Fatalf("operation/plan = %#v/%#v, want failed operation and pending plan", store.operation, store.restorePlan)
			}
		})
	}
}

func TestRestorePreviewDiscardExecutorRecoveryStatusMismatchFailsClosedBeforeJVS(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"}}
	executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status" || store.restorePreviewDiscardProgressUpdates != 0 {
		t.Fatalf("JVS/progress = %#v/%d, want status only and no mark", runner.calls, store.restorePreviewDiscardProgressUpdates)
	}
	if store.restorePlan.Status != restoreplan.StatusPending || store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("plan/operation = %#v/%#v, want pending plan and operator intervention operation", store.restorePlan, store.operation)
	}
}

func TestRestorePreviewDiscardExecutorMismatchedRecoveryPlanFailsClosedBeforeJVS(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_other", ActiveRecoveryPlanID: "recovery_001", Blocking: true, Workspace: "main"}}
	executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status" || store.restorePreviewDiscardProgressUpdates != 0 {
		t.Fatalf("JVS/progress = %#v/%d, want status only and no mark", runner.calls, store.restorePreviewDiscardProgressUpdates)
	}
	if store.restorePlan.Status != restoreplan.StatusPending || store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("plan/operation = %#v/%#v, want pending plan and operator intervention operation", store.restorePlan, store.operation)
	}
}

func TestRestorePreviewDiscardExecutorMissingPlanCommitsValidateInterventionWithoutJVS(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	runner := &fakeJVSRunner{}
	executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if len(runner.calls) != 0 || store.restorePreviewDiscardProgressUpdates != 0 {
		t.Fatalf("JVS/progress = %#v/%d, want no JVS and no mark", runner.calls, store.restorePreviewDiscardProgressUpdates)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Phase != operations.OperationPhaseRestorePreviewDiscardValidate {
		t.Fatalf("operation = %#v, want validate operator intervention", store.operation)
	}
	if store.operation.Error == nil || store.operation.Error.Code != "RESTORE_PREVIEW_DISCARD_PLAN_INVALID" {
		t.Fatalf("operation error = %#v, want plan invalid", store.operation.Error)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Outcome != audit.OutcomeFailed {
		t.Fatalf("audit events = %#v, want failed audit", store.auditEvents)
	}
}

func TestRestorePreviewDiscardExecutorJVSFailureAfterMarkMovesPlanToOperatorIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
		restoreDiscardErr:     errors.New("jvs discard failed with recommended_next_command=secret"),
	}
	executor := newTestRestorePreviewDiscardExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewDiscardLeasedRecord(now, operations.OperationPhaseRestorePreviewDiscardValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_discard" || store.restorePreviewDiscardProgressUpdates != 1 {
		t.Fatalf("JVS/progress = %#v/%d, want mark then discard attempt", runner.calls, store.restorePreviewDiscardProgressUpdates)
	}
	if store.restorePlan.Status != restoreplan.StatusOperatorInterventionRequired || store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("plan/operation = %#v/%#v, want operator intervention", store.restorePlan, store.operation)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func newTestRestorePreviewDiscardExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *RestorePreviewDiscardExecutor {
	t.Helper()
	executor, err := NewRestorePreviewDiscardExecutor(RestorePreviewDiscardConfig{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "audit_restore_preview_discard" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewRestorePreviewDiscardExecutor: %v", err)
	}
	return executor
}

func restorePreviewDiscardLeasedRecord(now time.Time, phase string) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, 1)
	record.ID = "op_discard"
	record.Type = operations.OperationRestorePreviewDiscard
	record.Phase = phase
	record.IdempotencyScope = operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreviewDiscard, "idem_discard").String()
	record.IdempotencyKey = "idem_discard"
	record.RequestHash = "sha256:restore-preview-discard"
	record.InputSummary = map[string]any{"preview_operation_id": "op_preview01"}
	return record
}

func restorePreviewSucceededOperationRecord(now time.Time) operations.OperationRecord {
	record := restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewCommitted)
	record.ID = "op_preview01"
	record.State = operations.OperationStateSucceeded
	record.LeaseOwner = ""
	record.LeaseExpiresAt = nil
	record.FinishedAt = &now
	record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001", "restore_plan_status": "pending"}
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	return record
}

func restorePreviewPendingPlan(now time.Time) restoreplan.Plan {
	return restoreplan.Plan{
		ID:                 "plan_001",
		NamespaceID:        "ns_alpha01",
		RepoID:             "repo_alpha01",
		PreviewOperationID: "op_preview01",
		SourceSavePointID:  "sp_001",
		Status:             restoreplan.StatusPending,
		CreatedAt:          now.Add(-time.Minute),
		UpdatedAt:          now.Add(-time.Minute),
	}
}
