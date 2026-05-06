package repoexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestRestoreRunExecutorFencesWriterRunsDoctorChecksIdleAndCommitsConsumed(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummaries: []jvsrunner.RecoveryStatusSummary{
			{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
			{RestoreState: "idle", Workspace: "main"},
		},
		restoreRunSummary: jvsrunner.RestoreRunSummary{PlanID: "plan_001", SourceSavePointID: "sp_001", RestoredSavePointID: "sp_restored", Workspace: "main"},
		doctorSummary:     jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"},
	}
	store.beforeListSessions = func() {
		if store.restoreRunWriterFenceMarks != 1 || activeWriterFenceCount(store.fences, "op_run") != 1 {
			t.Fatalf("session gate ran before durable writer fence, marks/fences = %d/%#v", store.restoreRunWriterFenceMarks, store.fences)
		}
	}
	runner.beforeRestoreRun = func() {
		if store.restoreRunConsumingMarks != 1 || store.restorePlan.Status != restoreplan.StatusConsuming {
			t.Fatalf("RestoreRun called before durable consuming mark, marks/plan = %d/%#v", store.restoreRunConsumingMarks, store.restorePlan)
		}
	}
	executor := newTestRestoreRunExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), restoreRunLeasedRecord(now, operations.OperationPhaseRestoreRunValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_run,doctor,recovery_status" {
		t.Fatalf("JVS calls = %#v, want recovery_status,restore_run,doctor,recovery_status", runner.calls)
	}
	if store.restorePlan.Status != restoreplan.StatusConsumed || store.restorePlan.ID != "plan_001" {
		t.Fatalf("restore plan = %#v, want consumed plan_001", store.restorePlan)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestoreRunCommitted {
		t.Fatalf("operation = %#v, want succeeded restore_run_committed", store.operation)
	}
	if store.releasedFenceID != "fence_op_run" || activeWriterFenceCount(store.fences, "op_run") != 0 {
		t.Fatalf("released/active writer fence = %q/%#v, want released same-op fence", store.releasedFenceID, store.fences)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestoreRun || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want restore run success", store.auditEvents)
	}
	assertNoRestoreRunCommandLeak(t, store.operation, store.auditEvents)
}

func TestRestoreRunExecutorPreJVSWriterSessionDenialReleasesFenceAndKeepsPlanPending(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name      string
		edit      func(*fakeRepoCreateStore)
		wantState operations.OperationState
	}{
		{
			name: "active rw export",
			edit: func(store *fakeRepoCreateStore) {
				store.exports = []sessionstate.ExportSession{{ID: "export_alpha", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadWrite, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}}
			},
			wantState: operations.OperationStateFailed,
		},
		{
			name: "stale rw workload mount",
			edit: func(store *fakeRepoCreateStore) {
				store.mounts = []sessionstate.WorkloadMountBinding{{ID: "wmb_alpha", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", ReadOnly: false, Status: sessionstate.MountStatusReleasing, LeaseExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)}}
			},
			wantState: operations.OperationStateFailed,
		},
		{
			name: "invalid same repo session state",
			edit: func(store *fakeRepoCreateStore) {
				store.exports = []sessionstate.ExportSession{{ID: "export_alpha", NamespaceID: "ns_other01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadWrite, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}}
			},
			wantState: operations.OperationStateFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			store.previewOperation = restorePreviewSucceededOperationRecord(now)
			store.restorePlan = restorePreviewPendingPlan(now)
			tt.edit(store)
			runner := &fakeJVSRunner{recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"}}
			executor := newTestRestoreRunExecutor(t, store, runner, now)

			err := executor.ExecuteOperationRecovery(context.Background(), restoreRunLeasedRecord(now, operations.OperationPhaseRestoreRunValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if strings.Join(runner.calls, ",") != "recovery_status" {
				t.Fatalf("JVS calls = %#v, want preflight status only", runner.calls)
			}
			if store.restorePlan.Status != restoreplan.StatusPending || store.releasedFenceID != "fence_op_run" || activeWriterFenceCount(store.fences, "op_run") != 0 {
				t.Fatalf("plan/released/fences = %#v/%q/%#v, want pending plan and released writer fence", store.restorePlan, store.releasedFenceID, store.fences)
			}
			if store.operation.State != tt.wantState || store.operation.Phase != operations.OperationPhaseRestoreRunWriterFenced {
				t.Fatalf("operation = %#v, want %s writer-fenced terminal", store.operation, tt.wantState)
			}
		})
	}
}

func TestRestoreRunExecutorPreflightRecoveryMismatchFailsClosedBeforeFence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"}}
	executor := newTestRestoreRunExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restoreRunLeasedRecord(now, operations.OperationPhaseRestoreRunValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status" || store.restoreRunWriterFenceMarks != 0 {
		t.Fatalf("JVS/fence marks = %#v/%d, want status only and no fence", runner.calls, store.restoreRunWriterFenceMarks)
	}
	if store.restorePlan.Status != restoreplan.StatusPending || store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("plan/operation = %#v/%#v, want pending plan and operator intervention", store.restorePlan, store.operation)
	}
}

func TestRestoreRunExecutorConsumingRecoveryDoesNotRerunJVSAndRequiresOperator(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	store.restorePlan.Status = restoreplan.StatusConsuming
	store.fences = []fences.Fence{restoreRunWriterFence(now, "op_run")}
	runner := &fakeJVSRunner{}
	executor := newTestRestoreRunExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restoreRunLeasedRecord(now, operations.OperationPhaseRestoreRunConsuming), recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls = %#v, want no rerun from consuming recovery", runner.calls)
	}
	if store.restorePlan.Status != restoreplan.StatusOperatorInterventionRequired || activeWriterFenceCount(store.fences, "op_run") != 1 {
		t.Fatalf("plan/fences = %#v/%#v, want plan OIR and retained writer fence", store.restorePlan, store.fences)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Phase != operations.OperationPhaseRestoreRunConsuming {
		t.Fatalf("operation = %#v, want consuming operator intervention", store.operation)
	}
}

func TestRestoreRunExecutorConsumingRecoverySkipsMetadataAndJVSWhenRepoUnavailable(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	store.restorePlan.Status = restoreplan.StatusConsuming
	store.fences = []fences.Fence{restoreRunWriterFence(now, "op_run")}
	runner := &fakeJVSRunner{}
	executor := newTestRestoreRunExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restoreRunLeasedRecord(now, operations.OperationPhaseRestoreRunConsuming), recovery.RecoveryPlan{Action: recovery.RecoveryActionReclaim})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls = %#v, want no JVS from consuming recovery", runner.calls)
	}
	if store.restorePlan.Status != restoreplan.StatusOperatorInterventionRequired || activeWriterFenceCount(store.fences, "op_run") != 1 {
		t.Fatalf("plan/fences = %#v/%#v, want plan OIR and retained writer fence", store.restorePlan, store.fences)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Error == nil || store.operation.Error.Code != "RESTORE_RUN_CONSUMING_RECOVERY_REQUIRES_OPERATOR" {
		t.Fatalf("operation = %#v, want consuming recovery OIR", store.operation)
	}
}

func TestRestoreRunExecutorJVSFailureAfterConsumingMovesPlanToOperatorAndKeepsFence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = activeRepoResource(now)
	store.previewOperation = restorePreviewSucceededOperationRecord(now)
	store.restorePlan = restorePreviewPendingPlan(now)
	runner := &fakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
		restoreRunErr:         errors.New("restore --run failed with recommended_next_command=secret"),
	}
	executor := newTestRestoreRunExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), restoreRunLeasedRecord(now, operations.OperationPhaseRestoreRunValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if strings.Join(runner.calls, ",") != "recovery_status,restore_run" || store.restoreRunConsumingMarks != 1 {
		t.Fatalf("JVS/consuming marks = %#v/%d, want restore_run after consuming mark", runner.calls, store.restoreRunConsumingMarks)
	}
	if store.restorePlan.Status != restoreplan.StatusOperatorInterventionRequired || activeWriterFenceCount(store.fences, "op_run") != 1 {
		t.Fatalf("plan/fences = %#v/%#v, want plan OIR and retained writer fence", store.restorePlan, store.fences)
	}
	assertNoRestoreRunCommandLeak(t, store.operation, store.auditEvents)
}

func TestRestoreRunExecutorDoctorOrPostStatusFailureAfterRunRequiresOperator(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name string
		edit func(*fakeJVSRunner)
		want string
	}{
		{
			name: "doctor failure",
			edit: func(runner *fakeJVSRunner) { runner.doctorErr = errors.New("doctor failed") },
			want: "recovery_status,restore_run,doctor",
		},
		{
			name: "post recovery status mismatch",
			edit: func(runner *fakeJVSRunner) {
				runner.recoveryStatusSummaries = []jvsrunner.RecoveryStatusSummary{
					{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
					{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
				}
			},
			want: "recovery_status,restore_run,doctor,recovery_status",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = activeRepoResource(now)
			store.previewOperation = restorePreviewSucceededOperationRecord(now)
			store.restorePlan = restorePreviewPendingPlan(now)
			runner := &fakeJVSRunner{
				recoveryStatusSummaries: []jvsrunner.RecoveryStatusSummary{
					{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
					{RestoreState: "idle", Workspace: "main"},
				},
				restoreRunSummary: jvsrunner.RestoreRunSummary{PlanID: "plan_001", SourceSavePointID: "sp_001", RestoredSavePointID: "sp_restored", Workspace: "main"},
				doctorSummary:     jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"},
			}
			tt.edit(runner)
			executor := newTestRestoreRunExecutor(t, store, runner, now)

			err := executor.ExecuteOperationRecovery(context.Background(), restoreRunLeasedRecord(now, operations.OperationPhaseRestoreRunValidate), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if strings.Join(runner.calls, ",") != tt.want {
				t.Fatalf("JVS calls = %#v, want %s", runner.calls, tt.want)
			}
			if store.restorePlan.Status != restoreplan.StatusOperatorInterventionRequired || activeWriterFenceCount(store.fences, "op_run") != 1 {
				t.Fatalf("plan/fences = %#v/%#v, want plan OIR and retained writer fence", store.restorePlan, store.fences)
			}
		})
	}
}

func newTestRestoreRunExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *RestoreRunExecutor {
	t.Helper()
	executor, err := NewRestoreRunExecutor(RestoreRunConfig{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "audit_restore_run" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewRestoreRunExecutor: %v", err)
	}
	return executor
}

func restoreRunLeasedRecord(now time.Time, phase string) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, 1)
	record.ID = "op_run"
	record.Type = operations.OperationRestoreRun
	record.Phase = phase
	record.IdempotencyScope = operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestoreRun, "idem_run").String()
	record.IdempotencyKey = "idem_run"
	record.RequestHash = "sha256:restore-run"
	record.InputSummary = map[string]any{"preview_operation_id": "op_preview01"}
	if phase == operations.OperationPhaseRestoreRunWriterFenced || phase == operations.OperationPhaseRestoreRunConsuming {
		record.SessionFenceID = "fence_op_run"
		record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	}
	return record
}

func activeWriterFenceCount(existing []fences.Fence, operationID string) int {
	count := 0
	for _, fence := range existing {
		if fence.Kind == fences.KindWriterSession && fence.HolderOperationID == operationID && fence.Status == fences.StatusActive && fence.ReleasedAt == nil && fence.RecoveredAt == nil {
			count++
		}
	}
	return count
}

func restoreRunWriterFence(now time.Time, operationID string) fences.Fence {
	return fences.Fence{ID: "fence_" + operationID, RepoID: "repo_alpha01", Kind: fences.KindWriterSession, HolderOperationID: operationID, Status: fences.StatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}
}

func assertNoRestoreRunCommandLeak(t *testing.T, operation operations.OperationRecord, events []audit.Event) {
	t.Helper()
	rendered := strings.ToLower(fmt.Sprint(operation.InputSummary, operation.JVSJSONOutput, operation.VerificationResult, operation.Error))
	for _, event := range events {
		rendered += " " + strings.ToLower(fmt.Sprint(event.Details))
	}
	for _, leaked := range []string{"run_command", "recommended_next_command", "restore_command", "jvs restore --run"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("restore-run persisted raw command marker %q in operation/events: %#v %#v", leaked, operation, events)
		}
	}
}
