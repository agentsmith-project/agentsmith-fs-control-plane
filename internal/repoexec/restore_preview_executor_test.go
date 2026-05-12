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
		restorePreviewSummary: jvsrunner.RestorePreviewSummary{
			PlanID:            "plan_001",
			SourceSavePointID: "sp_001",
			BaseRevision:      "sp_002",
			HeadRevision:      "sp_002",
			Generation:        "sha256:preview-base",
			ManagedFiles: jvsrunner.RestorePreviewManagedFilesSummary{
				Added:       jvsrunner.RestorePreviewChangeSummary{Count: 1, Samples: []string{"src/new.ts"}},
				Changed:     jvsrunner.RestorePreviewChangeSummary{Count: 1, Samples: []string{"docs/readme.md"}},
				Removed:     jvsrunner.RestorePreviewChangeSummary{Count: 1, Samples: []string{"tmp/cache.txt"}},
				Destructive: true,
			},
			Workspace:         "main",
			RunCommandPresent: true,
		},
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
	if store.restorePlan.BaseRevision != "sp_002" || store.restorePlan.HeadRevision != "sp_002" || store.restorePlan.Generation != "sha256:preview-base" || store.restorePlan.FenceMarker != "preview_fence_op_preview" {
		t.Fatalf("restore plan metadata = %#v, want preview revision and fence metadata", store.restorePlan)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestorePreviewCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", store.operation)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["preflight_recovery_status_captured"] != true || verification["restore_plan_id"] != "plan_001" || verification["source_save_point_id"] != "sp_001" || verification["stale"] != false {
		t.Fatalf("verification = %#v, want marker and plan summary", verification)
	}
	jvsOutput := store.operation.JVSJSONOutput.(map[string]any)
	if jvsOutput["restore_plan_id"] != "plan_001" || jvsOutput["source_save_point_id"] != "sp_001" {
		t.Fatalf("jvs output = %#v, want product-visible restore plan metadata", jvsOutput)
	}
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

func TestRestorePreviewExecutorValidateDiscardsOrphanPendingPreviewWithoutDBPlan(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummaries: []jvsrunner.RecoveryStatusSummary{
			{RestoreState: "pending_restore_preview", ActivePlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", Blocking: true, Workspace: "main"},
			{RestoreState: "idle", Workspace: "main"},
		},
		restoreDiscardSummary: jvsrunner.RestoreDiscardSummary{PlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", PlanDiscarded: true, Workspace: "main"},
		restorePreviewSummary: jvsrunner.RestorePreviewSummary{
			PlanID:            "7f9bc1aa-7c81-4861-bbfd-366bc734d851",
			SourceSavePointID: "sp_001",
			BaseRevision:      "sp_002",
			HeadRevision:      "sp_002",
			Generation:        "sha256:preview-base",
			ManagedFiles: jvsrunner.RestorePreviewManagedFilesSummary{
				Added:       jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Changed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Removed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Destructive: false,
			},
			Workspace:         "main",
			RunCommandPresent: true,
		},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_discard,recovery_status,restore_preview" {
		t.Fatalf("JVS calls = %#v, want cleanup then same-operation preview", runner.calls)
	}
	if store.restorePlan.ID != "7f9bc1aa-7c81-4861-bbfd-366bc734d851" || store.operation.State != operations.OperationStateSucceeded {
		t.Fatalf("plan/operation = %#v/%#v, want same-operation preview after orphan cleanup", store.restorePlan, store.operation)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["orphan_restore_plan_id"] != "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7" || verification["jvs_orphan_preview_discarded"] != true || verification["restore_plan_id"] != "7f9bc1aa-7c81-4861-bbfd-366bc734d851" {
		t.Fatalf("verification = %#v, want discarded orphan JVS pending plan evidence", verification)
	}
}

func TestRestorePreviewExecutorValidatePreservesDurablePendingPlan(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.restorePlan = restoreplan.Plan{ID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", RepoID: "repo_alpha01", Status: restoreplan.StatusPending}
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", Blocking: true, Workspace: "main"},
		restoreDiscardSummary: jvsrunner.RestoreDiscardSummary{PlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", PlanDiscarded: true, Workspace: "main"},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention for durable active plan", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status" {
		t.Fatalf("JVS calls = %#v, want no discard while durable plan exists", runner.calls)
	}
	if store.restorePlan.Status != restoreplan.StatusPending || store.operation.Error == nil || store.operation.Error.Code != "RESTORE_PREVIEW_ACTIVE_PLAN_PRESENT" {
		t.Fatalf("plan/operation = %#v/%#v, want durable plan preserved", store.restorePlan, store.operation)
	}
}

func TestRestorePreviewExecutorPreflightRetryRerunsPreviewWhenRecoveryStatusIsIdle(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"},
		restorePreviewSummary: jvsrunner.RestorePreviewSummary{
			PlanID:            "7f9bc1aa-7c81-4861-bbfd-366bc734d851",
			SourceSavePointID: "sp_001",
			BaseRevision:      "sp_002",
			HeadRevision:      "sp_002",
			Generation:        "sha256:preview-base",
			ManagedFiles: jvsrunner.RestorePreviewManagedFilesSummary{
				Added:       jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Changed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Removed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Destructive: false,
			},
			Workspace:         "main",
			RunCommandPresent: true,
		},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)
	record := restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewPreflightIdle)
	record.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false}

	err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_preview" {
		t.Fatalf("JVS calls = %#v, want recovery_status then retry restore_preview", runner.calls)
	}
	if store.restorePlan.ID != "7f9bc1aa-7c81-4861-bbfd-366bc734d851" || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestorePreviewCommitted {
		t.Fatalf("plan/operation = %#v/%#v, want rerun preview committed from idle preflight recovery", store.restorePlan, store.operation)
	}
}

func TestRestorePreviewExecutorPreflightRetryDiscardsPendingPreviewWithoutDBPlan(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", Blocking: true, Workspace: "main"},
		restoreDiscardSummary: jvsrunner.RestoreDiscardSummary{PlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", PlanDiscarded: true, Workspace: "main"},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)
	record := restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewPreflightIdle)
	record.VerificationResult = map[string]any{"preflight_recovery_status_captured": true, "preflight_restore_state": "idle", "preflight_blocking": false}

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_discard" {
		t.Fatalf("JVS calls = %#v, want recovery_status then restore_discard", runner.calls)
	}
	if store.restorePlan.ID != "" {
		t.Fatalf("restore plan = %#v, want no DB plan adoption without durable preview metadata", store.restorePlan)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Phase != operations.OperationPhaseRestorePreviewPreflightIdle {
		t.Fatalf("operation = %#v, want terminal failed preflight operation", store.operation)
	}
	if store.operation.Error == nil || store.operation.Error.Code != "RESTORE_PREVIEW_DURABLE_COMMIT_LOST" {
		t.Fatalf("operation error = %#v, want durable commit lost failure", store.operation.Error)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["restore_plan_id"] != "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7" || verification["jvs_pending_preview_discarded"] != true {
		t.Fatalf("verification = %#v, want discarded JVS pending plan evidence", verification)
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

func TestRestorePreviewExecutorPreviewCommandErrorIdleStatusFailsRetryableWithoutManualIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummaries: []jvsrunner.RecoveryStatusSummary{
			{RestoreState: "idle", Workspace: "main"},
			{RestoreState: "idle", Workspace: "main"},
		},
		restorePreviewErr: &jvsrunner.CommandError{Command: "restore", ExitCode: 7, Code: "E_REPO_BUSY"},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_preview,recovery_status" || store.restorePreviewProgressUpdates != 1 {
		t.Fatalf("JVS/progress = %#v/%d, want preflight, preview, post-error status", runner.calls, store.restorePreviewProgressUpdates)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "RESTORE_PREVIEW_NO_SIDE_EFFECT_RETRYABLE" || !store.operation.Error.Retryable {
		t.Fatalf("operation = %#v, want retryable no-side-effect failed preview", store.operation)
	}
	if store.restorePlan.ID != "" {
		t.Fatalf("restore plan = %#v, want no durable plan when post-error status is idle", store.restorePlan)
	}
	if store.operation.Error.Details["jvs_error_code"] != "E_REPO_BUSY" || store.operation.Error.Details["jvs_command"] != "restore" {
		t.Fatalf("operation error details = %#v, want redacted JVS command error details", store.operation.Error.Details)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestRestorePreviewExecutorPreviewTimeoutReconcilesAndDiscardsPendingPreview(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeJVSRunner{
		recoveryStatusSummaries: []jvsrunner.RecoveryStatusSummary{
			{RestoreState: "idle", Workspace: "main"},
			{RestoreState: "pending_restore_preview", ActivePlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", Blocking: true, Workspace: "main"},
		},
		restorePreviewErr:                   context.Canceled,
		restoreDiscardSummary:               jvsrunner.RestoreDiscardSummary{PlanID: "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7", PlanDiscarded: true, Workspace: "main"},
		failRecoveryStatusOnCanceledContext: true,
		failRestoreDiscardOnCanceledContext: true,
		beforeRestorePreview: func() {
			cancel()
		},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(ctx, restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("test context was not cancelled by restore preview")
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_preview,recovery_status,restore_discard" {
		t.Fatalf("JVS calls = %#v, want status, preview, detached status, discard", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "RESTORE_PREVIEW_DURABLE_COMMIT_LOST" {
		t.Fatalf("operation = %#v, want failed durable commit lost after timeout reconciliation", store.operation)
	}
	verification := store.operation.VerificationResult.(map[string]any)
	if verification["restore_plan_id"] != "1fa7ce01-3b2a-48fc-8c56-d1ff4959a2a7" || verification["jvs_pending_preview_discarded"] != true {
		t.Fatalf("verification = %#v, want discarded pending preview evidence", verification)
	}
}

func TestRestorePreviewExecutorPreviewTimeoutIdleRecoveryStatusFailsRetryable(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeJVSRunner{
		recoveryStatusSummaries: []jvsrunner.RecoveryStatusSummary{
			{RestoreState: "idle", Workspace: "main"},
			{RestoreState: "idle", Workspace: "main"},
		},
		restorePreviewErr:                   context.Canceled,
		failRecoveryStatusOnCanceledContext: true,
		beforeRestorePreview: func() {
			cancel()
		},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(ctx, restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("test context was not cancelled by restore preview")
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_preview,recovery_status" {
		t.Fatalf("JVS calls = %#v, want status, preview, detached status", runner.calls)
	}
	if store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "RESTORE_PREVIEW_TIMEOUT_RETRYABLE" || !store.operation.Error.Retryable {
		t.Fatalf("operation = %#v, want retryable timeout failure when JVS has no pending plan", store.operation)
	}
	if store.restorePlan.ID != "" {
		t.Fatalf("restore plan = %#v, want no DB plan when JVS has no pending preview", store.restorePlan)
	}
}

func TestRestorePreviewExecutorPreviewTimeoutUnknownRecoveryStatusRequiresOperator(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeJVSRunner{
		recoveryStatusSummaries: []jvsrunner.RecoveryStatusSummary{
			{RestoreState: "idle", Workspace: "main"},
			{RestoreState: "unknown", Workspace: "main", Blocking: true},
		},
		restorePreviewErr:                   context.Canceled,
		failRecoveryStatusOnCanceledContext: true,
		beforeRestorePreview: func() {
			cancel()
		},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(ctx, restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_preview,recovery_status" {
		t.Fatalf("JVS calls = %#v, want status, preview, detached status", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Error == nil || store.operation.Error.Code != "RESTORE_PREVIEW_TIMEOUT_RECONCILE_REQUIRES_OPERATOR" {
		t.Fatalf("operation = %#v, want timeout reconcile operator intervention for ambiguous JVS recovery state", store.operation)
	}
}

func TestRestorePreviewExecutorSuccessCommitSurvivesCallerContextCancellationAfterJVS(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.failOnCanceledCommitContext = true
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"},
		restorePreviewSummary: jvsrunner.RestorePreviewSummary{
			PlanID:            "plan_001",
			SourceSavePointID: "sp_001",
			BaseRevision:      "sp_002",
			HeadRevision:      "sp_002",
			Generation:        "sha256:preview-base",
			ManagedFiles: jvsrunner.RestorePreviewManagedFilesSummary{
				Added:       jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Changed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Removed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Destructive: false,
			},
			Workspace:         "main",
			RunCommandPresent: true,
		},
		beforeRestorePreview: func() {
			cancel()
		},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(ctx, restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("test context was not cancelled after JVS success")
	}
	if store.restorePlan.ID != "plan_001" || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestorePreviewCommitted {
		t.Fatalf("plan/operation = %#v/%#v, want durable commit despite caller context cancellation", store.restorePlan, store.operation)
	}
}

func TestRestorePreviewExecutorPreservesTypedSuccessCommitFailure(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.restorePreviewSuccessErr = errors.Join(operations.ErrLeaseUnavailable, errors.New("pq: password=secret lease expired"))
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"},
		restorePreviewSummary: jvsrunner.RestorePreviewSummary{
			PlanID:            "plan_001",
			SourceSavePointID: "sp_001",
			BaseRevision:      "sp_002",
			HeadRevision:      "sp_002",
			Generation:        "sha256:preview-base",
			ManagedFiles: jvsrunner.RestorePreviewManagedFilesSummary{
				Added:       jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Changed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Removed:     jvsrunner.RestorePreviewChangeSummary{Count: 0, Samples: []string{}},
				Destructive: false,
			},
			Workspace:         "main",
			RunCommandPresent: true,
		},
	}
	executor := newTestRestorePreviewExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restorePreviewLeasedRecord(now, operations.OperationPhaseRestorePreviewValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want typed lease unavailable", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "secret") || strings.Contains(strings.ToLower(err.Error()), "password") {
		t.Fatalf("ExecuteOperationRecovery leaked sensitive commit error: %v", err)
	}
	if !strings.Contains(err.Error(), "restore preview success commit failed") {
		t.Fatalf("ExecuteOperationRecovery error = %v, want commit failure context", err)
	}
	if store.restorePlan.ID != "plan_001" || store.operation.State != operations.OperationStateSucceeded {
		t.Fatalf("fake commit inputs = %#v/%#v, want attempted success commit before failure return", store.restorePlan, store.operation)
	}
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
	record.IdempotencyScope = operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String()
	record.IdempotencyKey = "idem_preview"
	record.RequestHash = "sha256:restore-preview"
	record.InputSummary = map[string]any{"save_point_id": "sp_001"}
	return record
}
