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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestLifecycleExecutorArchivesActiveRepoAndCommitsBoundary(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusArchived || store.repo.Lifecycle.Status != resources.RepoStatusArchived || store.repo.Lifecycle.LastLifecycleOperationID != "op_repo_lifecycle" {
		t.Fatalf("repo lifecycle = %#v, want archived", store.repo)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRepoLifecycleCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", store.operation)
	}
	if store.releasedFenceID != "fence_op_repo_lifecycle" || len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRepoArchive {
		t.Fatalf("release/audit = %q/%#v, want archive success", store.releasedFenceID, store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestLifecycleExecutorRestoresArchivedRepoAfterDoctor(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusArchived)
	runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestLifecycleExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoRestoreArchived, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "doctor" {
		t.Fatalf("jvs calls = %#v, want doctor", runner.calls)
	}
	if store.repo.Status != resources.RepoStatusActive || store.operation.Type != operations.OperationRepoRestoreArchived || store.auditEvents[0].Type != audit.EventTypeRepoRestoreArchived {
		t.Fatalf("repo/operation/audit = %#v/%#v/%#v", store.repo, store.operation, store.auditEvents)
	}
}

func TestLifecycleExecutorRetriesSameOperationFenceWithoutCreatingNewFence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
	store.fences = []fences.Fence{repoLifecycleFence(now, "fence_existing", "op_repo_lifecycle", fences.StatusActive)}
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.createFenceCalls != 0 || store.releasedFenceID != "fence_existing" {
		t.Fatalf("create/release = %d/%q, want reused same-op fence", store.createFenceCalls, store.releasedFenceID)
	}
}

func TestLifecycleExecutorFenceHandling(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name      string
		fence     fences.Fence
		wantState operations.OperationState
	}{
		{name: "foreign active fence fails closed", fence: repoLifecycleFence(now, "fence_foreign", "op_other", fences.StatusActive), wantState: operations.OperationStateFailed},
		{name: "same operation expired fence intervention", fence: repoLifecycleFence(now, "fence_existing", "op_repo_lifecycle", fences.StatusExpired), wantState: operations.OperationStateOperatorInterventionRequired},
		{name: "foreign recovery required fence intervention", fence: repoLifecycleFence(now, "fence_foreign", "op_other", fences.StatusRecoveryRequired), wantState: operations.OperationStateOperatorInterventionRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
			store.fences = []fences.Fence{tt.fence}
			executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

			err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry})
			if tt.wantState == operations.OperationStateOperatorInterventionRequired {
				if !errors.Is(err, recovery.ErrOperationManualIntervention) {
					t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
				}
			} else if err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if store.operation.State != tt.wantState || store.releasedFenceID != "" {
				t.Fatalf("operation/release = %#v/%q, want %s keep fence", store.operation, store.releasedFenceID, tt.wantState)
			}
		})
	}
}

func TestLifecycleExecutorReleasesSameOperationActiveFenceOnSourceMismatch(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusArchived)
	store.fences = []fences.Fence{repoLifecycleFence(now, "fence_existing", "op_repo_lifecycle", fences.StatusActive)}
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateFailed || store.releasedFenceID != "fence_existing" {
		t.Fatalf("operation/release = %#v/%q, want failed with same-op fence released", store.operation, store.releasedFenceID)
	}
}

func TestLifecycleExecutorReleasesSameOperationActiveFenceOnMetadataValidationFailure(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
	store.volume.Status = resources.VolumeStatusDisabled
	store.fences = []fences.Fence{repoLifecycleFence(now, "fence_existing", "op_repo_lifecycle", fences.StatusActive)}
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateFailed || store.releasedFenceID != "fence_existing" {
		t.Fatalf("operation/release = %#v/%q, want failed with same-op fence released", store.operation, store.releasedFenceID)
	}
	if store.createFenceCalls != 0 {
		t.Fatalf("created fence calls = %d, want none", store.createFenceCalls)
	}
}

func TestLifecycleExecutorActiveSessionWaitsWithoutMutation(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
	store.exports = []sessionstate.ExportSession{{ID: "export_alpha", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadOnly, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}}
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.ID != "" || store.releasedFenceID != "" || len(store.auditEvents) != 0 {
		t.Fatalf("operation/release/audit = %#v/%q/%#v, want wait without mutation", store.operation, store.releasedFenceID, store.auditEvents)
	}
}

func TestLifecycleExecutorRestoreArchivedWithNonTerminalSessionRequiresInterventionBeforeDoctor(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusArchived)
	store.exports = []sessionstate.ExportSession{{ID: "export_alpha", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadOnly, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}}
	runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestLifecycleExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoRestoreArchived, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" {
		t.Fatalf("operation/release = %#v/%q, want intervention keep fence", store.operation, store.releasedFenceID)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("jvs calls = %#v, want no doctor before session intervention", runner.calls)
	}
}

func TestLifecycleExecutorStaleSessionAndDoctorMismatchRequireIntervention(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name   string
		typ    operations.OperationType
		status resources.RepoStatus
		edit   func(*fakeRepoCreateStore, *fakeJVSRunner)
	}{
		{name: "stale session", typ: operations.OperationRepoArchive, status: resources.RepoStatusActive, edit: func(store *fakeRepoCreateStore, _ *fakeJVSRunner) {
			store.exports = []sessionstate.ExportSession{{ID: "export_alpha", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadOnly, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now}}
		}},
		{name: "doctor mismatch", typ: operations.OperationRepoRestoreArchived, status: resources.RepoStatusArchived, edit: func(_ *fakeRepoCreateStore, runner *fakeJVSRunner) {
			runner.doctorSummary = jvsrunner.DoctorSummary{RepoID: "jvs_repo_other", Healthy: true, Workspace: "main"}
		}},
		{name: "doctor unhealthy", typ: operations.OperationRepoRestoreArchived, status: resources.RepoStatusArchived, edit: func(_ *fakeRepoCreateStore, runner *fakeJVSRunner) {
			runner.doctorSummary = jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: false, Workspace: "main"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = repoLifecycleResource(now, tt.status)
			runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
			tt.edit(store, runner)
			executor := newTestLifecycleExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, tt.typ, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" {
				t.Fatalf("operation/release = %#v/%q, want intervention keep fence", store.operation, store.releasedFenceID)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestLifecycleExecutorSourceStateMismatchFailsWithoutFence(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusArchived)
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateFailed || store.createFenceCalls != 0 {
		t.Fatalf("operation/fence = %#v/%d, want failed without fence", store.operation, store.createFenceCalls)
	}
}

func newTestLifecycleExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *LifecycleExecutor {
	t.Helper()
	executor, err := NewLifecycleExecutor(LifecycleConfig{Store: store, JVSRunner: runner, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_lifecycle" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewLifecycleExecutor: %v", err)
	}
	return executor
}

func repoLifecycleLeasedRecord(now time.Time, typ operations.OperationType, attempt int) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, attempt)
	record.ID = "op_repo_lifecycle"
	record.Type = typ
	record.Phase = operations.OperationPhaseRepoLifecycleValidate
	record.IdempotencyScope = operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", typ, "idem_lifecycle").String()
	return record
}

func repoLifecycleResource(now time.Time, status resources.RepoStatus) resources.Repo {
	return resources.Repo{ID: "repo_alpha01", NamespaceID: "ns_alpha01", VolumeID: "vol_123", JVSRepoID: "jvs_repo_alpha", Kind: resources.RepoKindRepo, Status: status, ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control", PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload", Lifecycle: resources.RepoLifecycle{Status: status, LastLifecycleOperationID: "op_repo_create"}, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
}

func repoLifecycleFence(now time.Time, fenceID, operationID string, status fences.Status) fences.Fence {
	return fences.Fence{ID: fenceID, RepoID: "repo_alpha01", Kind: fences.KindLifecycle, HolderOperationID: operationID, Status: status, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}
