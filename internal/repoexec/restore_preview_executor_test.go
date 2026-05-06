package repoexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func TestRestorePreviewExecutorPersistsIdleMarkerBeforePreviewAndCommitsPlan(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"},
		restorePreviewSummary: jvsrunner.RestorePreviewSummary{PlanID: "plan_001", SourceSavePointID: "sp_001", Workspace: "main", RunCommandPresent: true},
	}
	runner.beforeRestorePreview = func() {
		if store.restorePreviewProgressUpdates != 1 {
			t.Fatalf("RestorePreview called before durable preflight marker, progress updates = %d", store.restorePreviewProgressUpdates)
		}
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_preview" {
		t.Fatalf("JVS calls = %#v, want recovery_status then restore_preview", runner.calls)
	}
	if !strings.HasSuffix(runner.controlRoot, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/control") {
		t.Fatalf("control root = %q", runner.controlRoot)
	}
	if store.restorePlan.ID != "plan_001" || store.restorePlan.Status != restoreplan.StatusPending || store.restorePlan.PreviewOperationID != "op_preview" {
		t.Fatalf("restore plan = %#v, want pending plan linked to preview operation", store.restorePlan)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestorePreviewCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", store.operation)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["preflight_recovery_status_captured"] != true || verification["restore_plan_id"] != "plan_001" || verification["source_save_point_id"] != "sp_001" {
		t.Fatalf("verification = %#v, want marker and plan summary", verification)
	}
	jvsOutput := store.operation.JVSJSONOutput.(map[string]any)
	for _, forbidden := range []string{"run_command", "recommended_next_command"} {
		if _, ok := jvsOutput[forbidden]; ok {
			t.Fatalf("jvs output persisted forbidden command field %q: %#v", forbidden, jvsOutput)
		}
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestorePreview || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want restore preview success", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestRestorePreviewExecutorValidationFailureFailsBeforeJVS(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name string
		edit func(*fakeRepoCreateStore)
	}{
		{name: "inactive namespace", edit: func(store *fakeRepoCreateStore) {
			store.namespace.Status = resources.NamespaceStatusDisabled
			disabledAt := now
			store.namespace.DisabledAt = &disabledAt
			store.namespace.DisabledReason = "disabled"
		}},
		{name: "inactive volume", edit: func(store *fakeRepoCreateStore) { store.volume.Status = resources.VolumeStatusDisabled }},
		{name: "missing volume root", edit: func(store *fakeRepoCreateStore) { store.volumeRoots = map[string]string{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			tt.edit(store)
			runner := &fakeJVSRunner{}
			executor := newTestRestorePreviewExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if len(runner.calls) != 0 || store.restorePreviewProgressUpdates != 0 {
				t.Fatalf("JVS/progress = %#v/%d, want none", runner.calls, store.restorePreviewProgressUpdates)
			}
			if store.operation.State != operations.OperationStateFailed || store.operation.Phase != operations.OperationPhaseRestorePreviewValidate {
				t.Fatalf("operation = %#v, want stable validation failure", store.operation)
			}
		})
	}
}

func TestRestorePreviewExecutorNonIdleRecoveryStatusRequiresOperatorIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_existing", ActiveRecoveryPlanID: "recovery_001", Blocking: true, Workspace: "main"}}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status" {
		t.Fatalf("JVS calls = %#v, want recovery_status only", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.restorePreviewProgressUpdates != 0 || store.restorePlan.ID != "" {
		t.Fatalf("operation/progress/plan = %#v/%d/%#v, want operator intervention before preview", store.operation, store.restorePreviewProgressUpdates, store.restorePlan)
	}
}

func TestRestorePreviewExecutorPreflightRetryFailsClosedWithoutPreview(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"}}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)
	record := restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewPreflightIdle)
	record.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false}

	err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status" {
		t.Fatalf("JVS calls = %#v, want recovery_status only", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation = %#v, want operator intervention", store.operation)
	}
}

func TestRestorePreviewExecutorMissingPreflightMarkerFailsClosed(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewPreflightIdle), recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls = %#v, want none when durable marker is missing", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation = %#v, want operator intervention", store.operation)
	}
}

func TestRestorePreviewExecutorPreviewFailureAfterPreflightRequiresOperatorIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"},
		restorePreviewErr:     errors.New("jvs restore failed with run_command=secret"),
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_preview" || store.restorePreviewProgressUpdates != 1 {
		t.Fatalf("JVS/progress = %#v/%d, want preflight then preview", runner.calls, store.restorePreviewProgressUpdates)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.restorePlan.ID != "" {
		t.Fatalf("operation/plan = %#v/%#v, want operator intervention without plan", store.operation, store.restorePlan)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func newTestRestorePreviewExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *RestorePreviewExecutor {
	t.Helper()
	executor, err := NewRestorePreviewExecutor(RestorePreviewConfig{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "audit_restore_preview" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewRestorePreviewExecutor: %v", err)
	}
	return executor
}

func restorePreviewLeasedRecord(now time.Time, phase string) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, 1)
	record.ID = "op_preview"
	record.Type = operations.OperationRestorePreview
	record.Phase = phase
	record.IdempotencyScope = operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String()
	record.IdempotencyKey = "idem_preview"
	record.RequestHash = "sha256:restore-preview"
	record.InputSummary = map[string]any{"save_point_id": "sp_001"}
	return record
}
