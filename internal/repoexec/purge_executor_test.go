package repoexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestPurgeExecutorPurgesTombstonedRepoWhenRetentionMet(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	purger := &fakeStoragePurger{state: RepoStoragePresent}
	executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, purger, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusPurged || store.repo.Lifecycle.Status != resources.RepoStatusPurged || store.repo.Lifecycle.RetentionExpiresAt != nil || store.repo.Lifecycle.PreDeleteStatus != resources.RepoStatusActive {
		t.Fatalf("repo lifecycle = %#v, want purged preserving pre-delete", store.repo.Lifecycle)
	}
	if purger.purgeCalls != 1 || store.operation.State != operations.OperationStateSucceeded || store.auditEvents[0].Type != audit.EventTypeRepoPurge || store.releasedFenceID == "" {
		t.Fatalf("purge/operation/audit/release = %d/%#v/%#v/%q", purger.purgeCalls, store.operation, store.auditEvents, store.releasedFenceID)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestPurgeExecutorAcceptsAPIInputSummaryShapeForProductConfirmation(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	purger := &fakeStoragePurger{state: RepoStorageAbsent}
	executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{}, purger, now)
	record := repoPurgeLeasedRecord(now, 2)
	record.InputSummary["product_confirmation_present"] = true
	delete(record.InputSummary["lifecycle_policy_snapshot"].(map[string]any), "product_confirmation_present")

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusPurged {
		t.Fatalf("repo status = %s, want purged", store.repo.Status)
	}
}

func TestPurgeExecutorBreakGlassOverrideSuccess(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusArchived, now.Add(time.Hour))
	purger := &fakeStoragePurger{state: RepoStoragePresent}
	executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, purger, now)
	record := repoPurgeLeasedRecord(now, 1)
	record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["retention_override_requested"] = true
	record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["operator_approval_present"] = true
	record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["break_glass_enabled"] = true
	record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)["break_glass_authorized"] = true

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusPurged || store.repo.Lifecycle.PreDeleteStatus != resources.RepoStatusArchived {
		t.Fatalf("repo lifecycle = %#v, want purged via break-glass", store.repo.Lifecycle)
	}
}

func TestPurgeExecutorPolicyAndStorageFailuresRequireManualIntervention(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name       string
		edit       func(*fakeRepoCreateStore, *fakeStoragePurger, operations.OperationRecord) operations.OperationRecord
		wantPurge  int
		wantDoctor bool
	}{
		{name: "retention not met", edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(time.Hour))
			return record
		}},
		{name: "missing snapshot", edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			record.InputSummary = map[string]any{"repo_id": record.RepoID}
			return record
		}},
		{name: "invalid snapshot", edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			record.InputSummary["product_confirmation_present"] = false
			return record
		}},
		{name: "old purge from previous tombstone cycle", edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			store.repo.UpdatedAt = now.Add(-30 * time.Minute)
			record.CreatedAt = now.Add(-time.Hour)
			return record
		}},
		{name: "stale session", edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			store.exports = []sessionstate.ExportSession{{ID: "export_stale", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadOnly, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now}}
			return record
		}},
		{name: "partial absent cleanup failure", wantPurge: 1, wantDoctor: false, edit: func(store *fakeRepoCreateStore, purger *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			purger.state = RepoStoragePartialAbsent
			purger.purgeErr = errors.New("secret partial cleanup failed")
			return record
		}},
		{name: "delete failure", wantPurge: 1, wantDoctor: true, edit: func(store *fakeRepoCreateStore, purger *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			purger.state = RepoStoragePresent
			purger.purgeErr = errors.New("secret /srv/afscp delete failed")
			return record
		}},
		{name: "doctor mismatch", wantDoctor: true, edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			return record
		}},
		{name: "older restore blocks purge", edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			store.blockingLifecycle = []operations.OperationRecord{repoPurgeBlockingRestore(now)}
			return record
		}},
		{name: "same timestamp earlier operation id blocks purge", edit: func(store *fakeRepoCreateStore, _ *fakeStoragePurger, record operations.OperationRecord) operations.OperationRecord {
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			blocker := repoPurgeBlockingRestore(now)
			blocker.ID = "op_a_restore"
			blocker.CreatedAt = record.CreatedAt
			store.blockingLifecycle = []operations.OperationRecord{blocker}
			return record
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			purger := &fakeStoragePurger{state: RepoStoragePresent}
			runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
			if tt.name == "doctor mismatch" {
				runner.doctorSummary.RepoID = "jvs_repo_other"
			}
			record := tt.edit(store, purger, repoPurgeLeasedRecord(now, 1))
			executor := newTestPurgeExecutor(t, store, runner, purger, now)

			err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" {
				t.Fatalf("operation/release = %#v/%q, want intervention keep fence", store.operation, store.releasedFenceID)
			}
			if purger.purgeCalls != tt.wantPurge {
				t.Fatalf("purge calls = %d, want %d", purger.purgeCalls, tt.wantPurge)
			}
			if gotDoctor := strings.Contains(strings.Join(runner.calls, ","), "doctor"); gotDoctor != tt.wantDoctor {
				t.Fatalf("doctor called = %v, want %v; calls=%#v", gotDoctor, tt.wantDoctor, runner.calls)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestPurgeExecutorCleansPartialAbsentStorageBeforeCommit(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	purger := &fakeStoragePurger{states: []RepoStorageState{RepoStoragePartialAbsent, RepoStorageAbsent}}
	runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_other", Healthy: false, Workspace: "main"}}
	executor := newTestPurgeExecutor(t, store, runner, purger, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusPurged || purger.purgeCalls != 1 || len(runner.calls) != 0 {
		t.Fatalf("repo/purge/jvs = %#v/%d/%#v, want partial cleanup without doctor", store.repo, purger.purgeCalls, runner.calls)
	}
}

func TestPurgeExecutorVerifiesStorageAbsentAfterDelete(t *testing.T) {
	now := repoExecNow()
	tests := []struct {
		name       string
		afterState RepoStorageState
	}{
		{name: "still present", afterState: RepoStoragePresent},
		{name: "partial", afterState: RepoStoragePartialAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
			purger := &fakeStoragePurger{state: RepoStoragePresent, states: []RepoStorageState{RepoStoragePresent, tt.afterState}}
			executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, purger, now)

			err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
			if !errors.Is(err, recovery.ErrOperationManualIntervention) {
				t.Fatalf("ExecuteOperationRecovery error = %v, want manual", err)
			}
			if store.repo.Status == resources.RepoStatusPurged || store.operation.State != operations.OperationStateOperatorInterventionRequired {
				t.Fatalf("repo/operation = %#v/%#v, want no success commit", store.repo, store.operation)
			}
		})
	}
}

func TestPurgeExecutorActiveSessionWaitsWithoutMutation(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	store.exports = []sessionstate.ExportSession{freshExportSession(now, "export_active", sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour))}
	purger := &fakeStoragePurger{state: RepoStoragePresent}
	executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, purger, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.ID != "" || store.releasedFenceID != "" || purger.purgeCalls != 0 {
		t.Fatalf("operation/release/purge = %#v/%q/%d, want wait without mutation", store.operation, store.releasedFenceID, purger.purgeCalls)
	}
}

func TestPurgeExecutorAlreadyAbsentStorageCommitsSuccessWithoutDoctorOrDelete(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	purger := &fakeStoragePurger{state: RepoStorageAbsent}
	runner := &fakeJVSRunner{}
	executor := newTestPurgeExecutor(t, store, runner, purger, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusPurged || len(runner.calls) != 0 || purger.purgeCalls != 0 {
		t.Fatalf("repo/calls/purge = %#v/%#v/%d, want idempotent absent success", store.repo, runner.calls, purger.purgeCalls)
	}
}

func TestPurgeExecutorAlreadyAbsentStorageCommitsDespiteInactiveMetadataDrift(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	store.namespace.Status = resources.NamespaceStatusDisabled
	store.binding.Status = resources.NamespaceStatusDisabled
	store.volume.Status = resources.VolumeStatusDisabled
	purger := &fakeStoragePurger{state: RepoStorageAbsent}
	executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{}, purger, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.repo.Status != resources.RepoStatusPurged || purger.purgeCalls != 0 {
		t.Fatalf("repo/purge = %#v/%d, want absent storage terminal commit", store.repo, purger.purgeCalls)
	}
}

func TestPurgeExecutorRejectsUnsafeRootBeforeStorageMutation(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	purger := &fakeStoragePurger{state: RepoStoragePresent}
	executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{}, purger, now)
	executor.volumeRoots["vol_123"] = "/"

	err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if purger.inspectCalls != 0 || purger.purgeCalls != 0 {
		t.Fatalf("storage calls = %d/%d, want none for unsafe root", purger.inspectCalls, purger.purgeCalls)
	}
}

func TestPurgeExecutorRejectsStoredSubdirMismatchBeforeStorageMutation(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.repo = repoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	store.repo.ControlVolumeSubdir = "afscp/namespaces/ns_alpha01/repos/repo_other/control"
	purger := &fakeStoragePurger{state: RepoStoragePresent}
	executor := newTestPurgeExecutor(t, store, &fakeJVSRunner{}, purger, now)

	err := executor.ExecuteOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if !errors.Is(err, recovery.ErrOperationManualIntervention) {
		t.Fatalf("ExecuteOperationRecovery error = %v, want manual intervention", err)
	}
	if purger.inspectCalls != 0 || purger.purgeCalls != 0 {
		t.Fatalf("storage calls = %d/%d, want none for stored subdir mismatch", purger.inspectCalls, purger.purgeCalls)
	}
}

func TestFilesystemStoragePurgerRejectsSymlinkRoot(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(tmp, "repo_link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	purger := FilesystemStoragePurger{}

	state, err := purger.InspectRepoStorage(context.Background(), RepoStoragePaths{VolumeRootPath: tmp, ContainerRootPath: link, ControlRootPath: filepath.Join(link, "control"), PayloadRootPath: filepath.Join(link, "payload")})
	if err == nil || state != "" {
		t.Fatalf("InspectRepoStorage state/error = %q/%v, want symlink fail closed", state, err)
	}
	if err := purger.PurgeRepoStorage(context.Background(), RepoStoragePaths{VolumeRootPath: tmp, ContainerRootPath: link, ControlRootPath: filepath.Join(link, "control"), PayloadRootPath: filepath.Join(link, "payload")}); err == nil {
		t.Fatal("PurgeRepoStorage succeeded for symlink root, want fail closed")
	}
}

func TestFilesystemStoragePurgerRejectsAncestorSymlinkBelowVolumeRoot(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.MkdirAll(filepath.Join(target, "repo_alpha01", "control"), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(tmp, "namespaces")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	container := filepath.Join(link, "repo_alpha01")
	purger := FilesystemStoragePurger{}

	state, err := purger.InspectRepoStorage(context.Background(), RepoStoragePaths{VolumeRootPath: tmp, ContainerRootPath: container, ControlRootPath: filepath.Join(container, "control"), PayloadRootPath: filepath.Join(container, "payload")})
	if err == nil || state != "" {
		t.Fatalf("InspectRepoStorage state/error = %q/%v, want ancestor symlink fail closed", state, err)
	}
}

func TestFilesystemStoragePurgerRejectsSymlinkVolumeRoot(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	rootLink := filepath.Join(tmp, "volume_link")
	if err := os.Symlink(target, rootLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	container := filepath.Join(rootLink, "afscp", "namespaces", "ns_alpha01", "repos", "repo_alpha01")
	purger := FilesystemStoragePurger{}

	state, err := purger.InspectRepoStorage(context.Background(), RepoStoragePaths{VolumeRootPath: rootLink, ContainerRootPath: container, ControlRootPath: filepath.Join(container, "control"), PayloadRootPath: filepath.Join(container, "payload")})
	if err == nil || state != "" {
		t.Fatalf("InspectRepoStorage state/error = %q/%v, want symlink volume root fail closed", state, err)
	}
}

func TestPurgeExecutorSupportExcludesLifecycleTypesAndCancelAction(t *testing.T) {
	now := repoExecNow()
	executor := newTestPurgeExecutor(t, newFakeStore(), &fakeJVSRunner{}, &fakeStoragePurger{}, now)
	if support := executor.SupportsOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); !support.Supported {
		t.Fatalf("purge support = %#v, want supported", support)
	}
	if support := executor.SupportsOperationRecovery(context.Background(), repoLifecycleLeasedRecord(now, operations.OperationRepoDelete, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); support.Supported {
		t.Fatalf("delete support = %#v, want unsupported", support)
	}
	if support := executor.SupportsOperationRecovery(context.Background(), repoPurgeLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionFinalizeCancellation}); support.Supported {
		t.Fatalf("cancel support = %#v, want unsupported", support)
	}
}

func newTestPurgeExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, purger StoragePurger, now time.Time) *PurgeExecutor {
	t.Helper()
	executor, err := NewPurgeExecutor(PurgeConfig{Store: store, JVSRunner: runner, StoragePurger: purger, Owner: "worker-a", Clock: func() time.Time { return now }, AuditEventID: func() string { return "audit_purge" }, VolumeRoots: store.volumeRoots})
	if err != nil {
		t.Fatalf("NewPurgeExecutor: %v", err)
	}
	return executor
}

func repoPurgeLeasedRecord(now time.Time, attempt int) operations.OperationRecord {
	record := repoLifecycleLeasedRecord(now, operations.OperationRepoPurge, attempt)
	record.InputSummary = map[string]any{
		"repo_id":                      "repo_alpha01",
		"reason_present":               true,
		"product_confirmation_present": true,
		"operator_approval_present":    false,
		"retention_override_requested": false,
		"lifecycle_policy_snapshot": map[string]any{
			"retention_override_requested": false,
			"operator_approval_present":    false,
			"break_glass_enabled":          false,
			"break_glass_authorized":       false,
			"product_confirmation_present": true,
		},
	}
	return record
}

func repoPurgeBlockingRestore(now time.Time) operations.OperationRecord {
	record := repoLifecycleLeasedRecord(now, operations.OperationRepoRestoreTombstoned, 1)
	record.ID = "op_restore_older"
	record.CreatedAt = now.Add(-2 * time.Hour)
	record.State = operations.OperationStateQueued
	return record
}

type fakeStoragePurger struct {
	state        RepoStorageState
	states       []RepoStorageState
	inspectErr   error
	purgeErr     error
	inspectCalls int
	purgeCalls   int
}

func (purger *fakeStoragePurger) InspectRepoStorage(context.Context, RepoStoragePaths) (RepoStorageState, error) {
	purger.inspectCalls++
	if len(purger.states) > 0 {
		state := purger.states[0]
		purger.states = purger.states[1:]
		return state, purger.inspectErr
	}
	return purger.state, purger.inspectErr
}

func (purger *fakeStoragePurger) PurgeRepoStorage(context.Context, RepoStoragePaths) error {
	purger.purgeCalls++
	if purger.purgeErr == nil && len(purger.states) == 0 {
		purger.state = RepoStorageAbsent
	}
	return purger.purgeErr
}
