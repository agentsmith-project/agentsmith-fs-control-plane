package repoexec

import (
	"context"
	"errors"
	"math"
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
	if strings.Join(runner.calls, ",") != "direct_doctor" {
		t.Fatalf("jvs calls = %#v, want direct doctor", runner.calls)
	}
	if store.repo.Status != resources.RepoStatusActive || store.operation.Type != operations.OperationRepoRestoreArchived || store.auditEvents[0].Type != audit.EventTypeRepoRestoreArchived {
		t.Fatalf("repo/operation/audit = %#v/%#v/%#v", store.repo, store.operation, store.auditEvents)
	}
}

func TestLifecycleExecutorAllowsCleanupPendingDoctorWarning(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusArchived)
	runner := &fakeJVSRunner{
		directDoctorSummary: jvsrunner.DirectDoctorSummary{
			RepoID:        "jvs_repo_alpha",
			Healthy:       false,
			FindingCount:  1,
			Findings:      []jvsrunner.DirectDoctorFindingSummary{{Severity: "warning", Message: "direct restore cleanup pending"}},
			MetadataState: "ready",
			Journal:       "clean",
			Recovery:      "cleanup_pending",
		},
	}
	executor := newTestLifecycleExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoRestoreArchived, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "direct_doctor" {
		t.Fatalf("jvs calls = %#v, want direct doctor", runner.calls)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.repo.Status != resources.RepoStatusActive {
		t.Fatalf("operation/repo = %#v/%#v, want lifecycle success despite cleanup warning", store.operation, store.repo)
	}
}

func TestLifecycleExecutorDeletesActiveAndArchivedReposToTombstone(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name   string
		source resources.RepoStatus
	}{
		{name: "active", source: resources.RepoStatusActive},
		{name: "archived", source: resources.RepoStatusArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = repoLifecycleResource(now, tt.source)
			executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleDeleteRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			wantRetention := now.Add(-time.Hour).Add(7 * 24 * time.Hour)
			if store.repo.Status != resources.RepoStatusTombstoned || store.repo.Lifecycle.Status != resources.RepoStatusTombstoned || store.repo.Lifecycle.PreDeleteStatus != tt.source {
				t.Fatalf("repo lifecycle = %#v, want tombstoned from %s", store.repo.Lifecycle, tt.source)
			}
			if store.repo.Lifecycle.RetentionExpiresAt == nil || !store.repo.Lifecycle.RetentionExpiresAt.Equal(wantRetention) {
				t.Fatalf("retention = %v, want %s from durable input summary snapshot", store.repo.Lifecycle.RetentionExpiresAt, wantRetention)
			}
			if store.operation.State != operations.OperationStateSucceeded || store.operation.Type != operations.OperationRepoDelete || store.auditEvents[0].Type != audit.EventTypeRepoDelete {
				t.Fatalf("operation/audit = %#v/%#v, want repo_delete success", store.operation, store.auditEvents)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestLifecycleExecutorDeleteAllowsZeroRetentionSnapshot(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
	record := repoLifecycleDeleteRecord(now, 1)
	record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = float64(0)
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusTombstoned || store.repo.Lifecycle.RetentionExpiresAt == nil || !store.repo.Lifecycle.RetentionExpiresAt.Equal(record.CreatedAt) {
		t.Fatalf("repo lifecycle = %#v, want tombstoned with retention at operation created_at %s", store.repo.Lifecycle, record.CreatedAt)
	}
}

func TestLifecycleExecutorDeleteMissingOrInvalidRetentionSnapshotRequiresManualIntervention(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name string
		edit func(operations.OperationRecord) operations.OperationRecord
	}{
		{name: "missing snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary = map[string]any{"repo_id": record.RepoID}
			return record
		}},
		{name: "invalid snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = "secret-duration"
			return record
		}},
		{name: "negative snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = float64(-1)
			return record
		}},
		{name: "fractional snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = float64(1.5)
			return record
		}},
		{name: "overflow int64 snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = int64(math.MaxInt64)
			return record
		}},
		{name: "overflow float snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = math.MaxFloat64
			return record
		}},
		{name: "nan snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = math.NaN()
			return record
		}},
		{name: "inf snapshot", edit: func(record operations.OperationRecord) operations.OperationRecord {
			record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["tombstone_retention_seconds"] = math.Inf(1)
			return record
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
			executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)
			record := tt.edit(repoLifecycleDeleteRecord(now, 1))

			if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.repo.Status != resources.RepoStatusActive {
				t.Fatalf("operation/repo = %#v/%#v, want manual without lifecycle mutation", store.operation, store.repo)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestLifecycleExecutorSuccessCommitWrapsCauseWithoutLeakingDetails(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
	store.lifecycleSuccessErr = errors.Join(operations.ErrLeaseUnavailable, errors.New("postgres password=secret failed"))
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil {
		t.Fatal("ExecuteOperationRecovery succeeded, want commit error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/srv/afscp") {
		t.Fatalf("error leaked sensitive detail: %v", err)
	}
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("error = %v, want wrapped ErrLeaseUnavailable for recovery classification", err)
	}
}

func TestLifecycleExecutorRestoresTombstonedRepoToPreDeleteStatusAfterDoctor(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name      string
		preDelete resources.RepoStatus
	}{
		{name: "active", preDelete: resources.RepoStatusActive},
		{name: "archived", preDelete: resources.RepoStatusArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = repoLifecycleTombstonedResource(now, tt.preDelete, now.Add(time.Hour))
			runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
			executor := newTestLifecycleExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleRestoreTombstonedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if strings.Join(runner.calls, ",") != "direct_doctor" {
				t.Fatalf("jvs calls = %#v, want direct doctor", runner.calls)
			}
			if store.repo.Status != tt.preDelete || store.repo.Lifecycle.Status != tt.preDelete || store.repo.Lifecycle.RetentionExpiresAt != nil || store.repo.Lifecycle.PreDeleteStatus != "" {
				t.Fatalf("repo lifecycle = %#v, want restored to %s with tombstone metadata cleared", store.repo.Lifecycle, tt.preDelete)
			}
			if store.operation.Type != operations.OperationRepoRestoreTombstoned || store.auditEvents[0].Type != audit.EventTypeRepoRestoreTombstoned {
				t.Fatalf("operation/audit = %#v/%#v, want restore tombstoned success", store.operation, store.auditEvents)
			}
		})
	}
}

func TestLifecycleExecutorRestoreTombstonedUsesOperationCreatedAtForRetentionEligibility(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	retentionExpiredBeforeWorker := now.Add(-time.Minute)
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, retentionExpiredBeforeWorker)
	store.repo.UpdatedAt = now.Add(-2 * time.Hour)
	record := repoLifecycleRestoreTombstonedRecord(now, 1)
	record.CreatedAt = retentionExpiredBeforeWorker.Add(-time.Minute)
	runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestLifecycleExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusActive || strings.Join(runner.calls, ",") != "direct_doctor" {
		t.Fatalf("repo/calls = %#v/%#v, want restore accepted by operation created_at within retention", store.repo, runner.calls)
	}
}

func TestLifecycleExecutorRestoreTombstonedValidationAndSessionFailuresRequireManualIntervention(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name       string
		edit       func(*fakeRepoCreateStore, *fakeJVSRunner)
		wantDoctor bool
	}{
		{name: "accepted after retention expiry", edit: func(store *fakeRepoCreateStore, _ *fakeJVSRunner) {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
		}},
		{name: "old restore from previous tombstone cycle", edit: func(store *fakeRepoCreateStore, _ *fakeJVSRunner) {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(time.Hour))
			store.repo.UpdatedAt = now.Add(-30 * time.Minute)
		}},
		{name: "missing pre-delete status", edit: func(store *fakeRepoCreateStore, _ *fakeJVSRunner) {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(time.Hour))
			store.repo.Lifecycle.PreDeleteStatus = ""
		}},
		{name: "doctor mismatch", wantDoctor: true, edit: func(store *fakeRepoCreateStore, runner *fakeJVSRunner) {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(time.Hour))
			runner.doctorSummary = jvsrunner.DoctorSummary{RepoID: "jvs_repo_other", Healthy: true, Workspace: "main"}
		}},
		{name: "nonterminal session before doctor", edit: func(store *fakeRepoCreateStore, _ *fakeJVSRunner) {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(time.Hour))
			store.exports = []sessionstate.ExportSession{freshExportSession(now, "export_alpha", sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour))}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
			tt.edit(store, runner)
			executor := newTestLifecycleExecutor(t, store, runner, now)

			record := repoLifecycleRestoreTombstonedRecord(now, 1)
			if tt.name == "old restore from previous tombstone cycle" {
				record.CreatedAt = now.Add(-time.Hour)
			}
			err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" {
				t.Fatalf("operation/release = %#v/%q, want intervention keep fence", store.operation, store.releasedFenceID)
			}
			if gotDoctor := strings.Contains(strings.Join(runner.calls, ","), "doctor"); gotDoctor != tt.wantDoctor {
				t.Fatalf("doctor called = %v, want %v; calls=%#v", gotDoctor, tt.wantDoctor, runner.calls)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestLifecycleExecutorDeleteSessionHandling(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name      string
		session   sessionstate.ExportSession
		wantWait  bool
		wantState operations.OperationState
	}{
		{name: "active session waits", wantWait: true, session: freshExportSession(now, "export_active", sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour))},
		{name: "stale session manual", wantState: operations.OperationStateOperatorInterventionRequired, session: sessionstate.ExportSession{ID: "export_stale", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadOnly, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
			store.exports = []sessionstate.ExportSession{tt.session}
			executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

			err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleDeleteRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if tt.wantWait {
				if err != nil || store.operation.ID != "" || store.releasedFenceID != "" {
					t.Fatalf("err/operation/release = %v/%#v/%q, want wait without mutation", err, store.operation, store.releasedFenceID)
				}
				return
			}
			if !errors.Is(err, recovery.ErrOperationManualIntervention) || store.operation.State != tt.wantState || store.releasedFenceID != "" {
				t.Fatalf("err/operation/release = %v/%#v/%q, want stale manual keep fence", err, store.operation, store.releasedFenceID)
			}
		})
	}
}

func TestLifecycleExecutorRejectsRepoPurgeRecovery(t *testing.T) {
	now := repoExecNow()
	executor := newTestLifecycleExecutor(t, newFakeStore(), &fakeJVSRunner{}, now)
	record := repoLifecycleLeasedRecord(now, operations.OperationRepoPurge, 1)

	if support := executor.SupportsOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); support.Supported {
		t.Fatalf("SupportsOperationRecovery = supported, want repo_purge unsupported")
	}
	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err == nil {
		t.Fatal("ExecuteOperationRecovery succeeded for repo_purge, want unsupported error")
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
	store.exports = []sessionstate.ExportSession{freshExportSession(now, "export_alpha", sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour))}
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.ID != "" || store.releasedFenceID != "" || len(store.auditEvents) != 0 {
		t.Fatalf("operation/release/audit = %#v/%q/%#v, want wait without mutation", store.operation, store.releasedFenceID, store.auditEvents)
	}
}

func TestLifecycleExecutorTerminalMountWithoutNonAccessingEvidenceRequiresIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusActive)
	store.mounts = []sessionstate.WorkloadMountBinding{{
		ID:                 "wmb_alpha",
		NamespaceID:        "ns_alpha01",
		RepoID:             "repo_alpha01",
		ReadOnly:           true,
		Status:             sessionstate.MountStatusReleased,
		LeaseExpiresAt:     now.Add(-time.Minute),
		TerminalObservedAt: repoExecTimePtr(now.Add(-time.Minute)),
		CreatedAt:          now.Add(-time.Hour),
		UpdatedAt:          now.Add(-time.Minute),
	}}
	executor := newTestLifecycleExecutor(t, store, &fakeJVSRunner{}, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoArchive, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.repo.Status != resources.RepoStatusActive || store.releasedFenceID != "" {
		t.Fatalf("operation/repo/release = %#v/%s/%q, want intervention without archive commit", store.operation, store.repo.Status, store.releasedFenceID)
	}
}

func TestLifecycleExecutorRestoreArchivedWithNonTerminalSessionRequiresInterventionBeforeDoctor(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleResource(now, resources.RepoStatusArchived)
	store.exports = []sessionstate.ExportSession{freshExportSession(now, "export_alpha", sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour))}
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

func freshExportSession(now time.Time, exportID string, mode sessionstate.AccessMode, status sessionstate.ExportStatus, expiresAt time.Time) sessionstate.ExportSession {
	observedAt := now.Add(-time.Second)
	heartbeatExpiresAt := now.Add(time.Minute)
	activeWriteCount := 0
	if mode == sessionstate.AccessModeReadWrite {
		activeWriteCount = 1
	}
	return sessionstate.ExportSession{
		ID:                        exportID,
		NamespaceID:               "ns_alpha01",
		RepoID:                    "repo_alpha01",
		Mode:                      mode,
		Status:                    status,
		ExpiresAt:                 expiresAt,
		ActiveRequestCount:        1,
		ActiveWriteCount:          activeWriteCount,
		LastObservedAt:            &observedAt,
		LastGatewayHeartbeatAt:    &observedAt,
		GatewayHeartbeatExpiresAt: &heartbeatExpiresAt,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
}

func repoLifecycleLeasedRecord(now time.Time, typ operations.OperationType, attempt int) operations.OperationRecord {
	record := repoCreateLeasedRecord(now, attempt)
	record.ID = "op_repo_lifecycle"
	record.Type = typ
	record.Phase = operations.OperationPhaseRepoLifecycleValidate
	record.IdempotencyScope = operations.NewIdempotencyScope("product-caller", "ns_alpha01", typ, "idem_lifecycle").String()
	record.InputSummary = map[string]any{"repo_id": "repo_alpha01", "reason_present": false}
	return record
}

func repoLifecycleDeleteRecord(now time.Time, attempt int) operations.OperationRecord {
	record := repoLifecycleLeasedRecord(now, operations.OperationRepoDelete, attempt)
	record.InputSummary = map[string]any{
		"repo_id":        "repo_alpha01",
		"reason_present": false,
		"lifecycle_policy_snapshot": map[string]any{
			"tombstone_retention_seconds": float64(604800),
		},
	}
	return record
}

func repoLifecycleRestoreTombstonedRecord(now time.Time, attempt int) operations.OperationRecord {
	return repoLifecycleLeasedRecord(now, operations.OperationRepoRestoreTombstoned, attempt)
}

func repoLifecycleResource(now time.Time, status resources.RepoStatus) resources.Repo {
	return resources.Repo{ID: "repo_alpha01", NamespaceID: "ns_alpha01", VolumeID: "vol_123", JVSRepoID: "jvs_repo_alpha", Kind: resources.RepoKindRepo, Status: status, ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control", PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload", Lifecycle: resources.RepoLifecycle{Status: status, LastLifecycleOperationID: "op_repo_create"}, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
}

func repoLifecycleTombstonedResource(now time.Time, preDelete resources.RepoStatus, retention time.Time) resources.Repo {
	repo := repoLifecycleResource(now, resources.RepoStatusTombstoned)
	repo.Lifecycle.RetentionExpiresAt = &retention
	repo.Lifecycle.PreDeleteStatus = preDelete
	repo.Lifecycle.LastLifecycleOperationID = "op_delete_current"
	repo.UpdatedAt = now.Add(-2 * time.Hour)
	return repo
}

func repoLifecycleFence(now time.Time, fenceID, operationID string, status fences.Status) fences.Fence {
	return fences.Fence{ID: fenceID, RepoID: "repo_alpha01", Kind: fences.KindLifecycle, HolderOperationID: operationID, Status: status, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}
