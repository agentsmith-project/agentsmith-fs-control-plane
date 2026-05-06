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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestExecutorFirstAttemptInitializesDoctorsAndCommitsRepo(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)
	record := repoCreateLeasedRecord(now, 1)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "init,doctor" {
		t.Fatalf("JVS calls = %#v, want init then doctor", runner.calls)
	}
	if !strings.HasSuffix(runner.payloadRoot, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload") || !strings.HasSuffix(runner.controlRoot, "/afscp/namespaces/ns_alpha01/repos/repo_alpha01/control") {
		t.Fatalf("roots = payload %q control %q", runner.payloadRoot, runner.controlRoot)
	}
	if store.repo.ID != "repo_alpha01" || store.repo.VolumeID != "vol_123" || store.repo.JVSRepoID != "jvs_repo_alpha" || store.repo.Status != resources.RepoStatusActive {
		t.Fatalf("repo commit = %#v", store.repo)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRepoCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded committed", store.operation)
	}
	if store.releasedFenceID != "fence_op_repo" {
		t.Fatalf("released fence = %q, want created fence", store.releasedFenceID)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want succeeded", store.auditEvents)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorRetryWithSameOperationFenceAdoptsHealthyDoctorWithoutInit(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.fences = []fences.Fence{repoCreateFence(now, "fence_existing", "op_repo")}
	runner := &fakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)
	record := repoCreateLeasedRecord(now, 2)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if strings.Join(runner.calls, ",") != "doctor" {
		t.Fatalf("JVS calls = %#v, want doctor-only adoption", runner.calls)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.VerificationResult.(map[string]any)["adopted"] != true {
		t.Fatalf("operation = %#v, want adopted success", store.operation)
	}
	if store.createFenceCalls != 0 || store.releasedFenceID != "fence_existing" {
		t.Fatalf("create/release fence = %d/%q, want reuse existing", store.createFenceCalls, store.releasedFenceID)
	}
}

func TestExecutorFirstAttemptDoesNotAdoptOccupiedHealthyRoots(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{
		initErr:       errors.New("E_TARGET_ROOT_OCCUPIED /srv/afscp/secret"),
		doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"},
	}
	executor := newTestExecutor(t, store, runner, now)
	record := repoCreateLeasedRecord(now, 1)

	if err := executor.ExecuteOperationRecovery(context.Background(), record, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
	}
	if store.releasedFenceID != "" {
		t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorPreJVSValidationFailureFailsWithoutJVSOrFenceLeak(t *testing.T) {
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
		{name: "inactive binding", edit: func(store *fakeRepoCreateStore) { store.binding.Status = resources.NamespaceStatusDisabled }},
		{name: "inactive volume", edit: func(store *fakeRepoCreateStore) { store.volume.Status = resources.VolumeStatusDisabled }},
		{name: "missing capability", edit: func(store *fakeRepoCreateStore) { store.volume.Capabilities["jvs_external_control_root"] = false }},
		{name: "missing volume root", edit: func(store *fakeRepoCreateStore) { store.volumeRoots = map[string]string{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			tt.edit(store)
			runner := &fakeJVSRunner{}
			executor := newTestExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if len(runner.calls) != 0 || store.createFenceCalls != 0 {
				t.Fatalf("JVS/fence calls = %#v/%d, want none", runner.calls, store.createFenceCalls)
			}
			if store.operation.State != operations.OperationStateFailed {
				t.Fatalf("operation state = %q, want failed", store.operation.State)
			}
			assertNoRepoExecLeak(t, store.operation, store.auditEvents)
		})
	}
}

func TestExecutorMetadataFailureWithSameOperationFenceRequiresIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.fences = []fences.Fence{repoCreateFence(now, "fence_existing", "op_repo")}
	store.volume.Status = resources.VolumeStatusDisabled
	runner := &fakeJVSRunner{}
	executor := newTestExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if len(runner.calls) != 0 || store.createFenceCalls != 0 {
		t.Fatalf("JVS/fence calls = %#v/%d, want none", runner.calls, store.createFenceCalls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
	}
	if store.releasedFenceID != "" {
		t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorNonActiveSameOperationFenceRequiresInterventionWithoutJVS(t *testing.T) {
	now := repoExecNow()
	for _, status := range []fences.Status{fences.StatusExpired, fences.StatusRecoveryRequired} {
		t.Run(string(status), func(t *testing.T) {
			store := newFakeStore()
			fence := repoCreateFence(now, "fence_existing", "op_repo")
			fence.Status = status
			store.fences = []fences.Fence{fence}
			runner := &fakeJVSRunner{}
			executor := newTestExecutor(t, store, runner, now)

			if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
				t.Fatalf("ExecuteOperationRecovery: %v", err)
			}
			if len(runner.calls) != 0 || store.createFenceCalls != 0 {
				t.Fatalf("JVS/fence calls = %#v/%d, want none", runner.calls, store.createFenceCalls)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired {
				t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
			}
			if store.releasedFenceID != "" {
				t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
			}
		})
	}
}

func TestExecutorRetryAfterSuccessCommitFailureAndMetadataInvalidRequiresIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.successErr = errors.New("commit failed")
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil {
		t.Fatal("ExecuteOperationRecovery succeeded, want commit error")
	}
	if store.createFenceCalls != 1 || len(store.fences) != 1 {
		t.Fatalf("fences = calls %d list %#v, want created same-op fence", store.createFenceCalls, store.fences)
	}

	store.successErr = nil
	store.volume.Status = resources.VolumeStatusDisabled
	runner.calls = nil
	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 2), recovery.RecoveryPlan{Action: recovery.RecoveryActionRetry}); err != nil {
		t.Fatalf("retry ExecuteOperationRecovery: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("JVS calls on retry = %#v, want none", runner.calls)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation state = %q, want operator intervention", store.operation.State)
	}
	if store.releasedFenceID != "" {
		t.Fatalf("released fence = %q, want kept fence", store.releasedFenceID)
	}
}

func TestExecutorInitDoctorRepoIDMismatchRequiresIntervention(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_other", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)

	if err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		t.Fatalf("ExecuteOperationRecovery: %v", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" {
		t.Fatalf("operation/release = %#v/%q, want intervention keeping fence", store.operation, store.releasedFenceID)
	}
	assertNoRepoExecLeak(t, store.operation, store.auditEvents)
}

func TestExecutorPropagatesCommitErrorsSafely(t *testing.T) {
	now := repoExecNow()
	store := newFakeStore()
	store.successErr = errors.New("postgres password=secret failed")
	runner := &fakeJVSRunner{initSummary: jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}, doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
	executor := newTestExecutor(t, store, runner, now)

	err := executor.ExecuteOperationRecovery(context.Background(), repoCreateLeasedRecord(now, 1), recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable})
	if err == nil {
		t.Fatal("ExecuteOperationRecovery succeeded, want commit error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/srv/afscp") {
		t.Fatalf("error leaked sensitive detail: %v", err)
	}
}

func newTestExecutor(t *testing.T, store *fakeRepoCreateStore, runner *fakeJVSRunner, now time.Time) *Executor {
	t.Helper()
	executor, err := NewExecutor(Config{
		Store:        store,
		JVSRunner:    runner,
		Owner:        "worker-a",
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "audit_repo" },
		VolumeRoots:  store.volumeRoots,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor
}

func repoCreateLeasedRecord(now time.Time, attempt int) operations.OperationRecord {
	leaseExpiresAt := now.Add(time.Minute)
	startedAt := now.Add(-time.Minute)
	return operations.OperationRecord{
		ID:                  "op_repo",
		Type:                operations.OperationRepoCreate,
		State:               operations.OperationStateRunning,
		Phase:               operations.OperationPhaseRepoCreateValidate,
		Attempt:             attempt,
		LeaseOwner:          "worker-a",
		LeaseExpiresAt:      &leaseExpiresAt,
		IdempotencyScope:    operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRepoCreate, "idem_repo").String(),
		IdempotencyKey:      "idem_repo",
		RequestHash:         "sha256:repo",
		CorrelationID:       "corr-alpha",
		CallerService:       "agentsmith-api",
		AuthorizedActor:     operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:         "ns_alpha01",
		RepoID:              "repo_alpha01",
		ExternalResourceIDs: map[string]string{},
		InputSummary:        map[string]any{"namespace_id": "ns_alpha01", "target_repo_id": "repo_alpha01"},
		CreatedAt:           now.Add(-time.Hour),
		StartedAt:           &startedAt,
	}
}

func repoExecNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func newFakeStore() *fakeRepoCreateStore {
	now := repoExecNow()
	return &fakeRepoCreateStore{
		namespace: resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now},
		binding: resources.NamespaceVolumeBinding{
			NamespaceID:       "ns_alpha01",
			DefaultVolumeID:   "vol_123",
			AllowedCallers:    []resources.AllowedCaller{{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}}},
			QuotaBytesDefault: 4096,
			ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
			LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
			MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
			TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
			Status:            resources.NamespaceStatusActive,
			CreatedAt:         now.Add(-24 * time.Hour),
			UpdatedAt:         now,
		},
		volume: resources.Volume{
			ID:             "vol_123",
			Backend:        resources.VolumeBackendJuiceFS,
			IsolationClass: resources.VolumeIsolationShared,
			Status:         resources.VolumeStatusActive,
			Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
			CreatedAt:      now.Add(-24 * time.Hour),
			UpdatedAt:      now,
		},
		volumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	}
}

type fakeRepoCreateStore struct {
	namespace         resources.Namespace
	binding           resources.NamespaceVolumeBinding
	volume            resources.Volume
	volumeRoots       map[string]string
	fences            []fences.Fence
	repo              resources.Repo
	operation         operations.OperationRecord
	auditEvents       []audit.Event
	exports           []sessionstate.ExportSession
	mounts            []sessionstate.WorkloadMountBinding
	createFenceCalls  int
	releasedFenceID   string
	successErr        error
	blockingLifecycle []operations.OperationRecord
}

func (store *fakeRepoCreateStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return store.namespace, nil
}
func (store *fakeRepoCreateStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	return store.binding, nil
}
func (store *fakeRepoCreateStore) GetVolume(context.Context, string) (resources.Volume, error) {
	return store.volume, nil
}
func (store *fakeRepoCreateStore) GetRepoInNamespace(context.Context, string, string) (resources.Repo, error) {
	return store.repo, nil
}
func (store *fakeRepoCreateStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return append([]fences.Fence(nil), store.fences...), nil
}
func (store *fakeRepoCreateStore) CreateRepoFence(_ context.Context, fence fences.Fence) error {
	store.createFenceCalls++
	store.fences = append(store.fences, fence)
	return nil
}
func (store *fakeRepoCreateStore) ReleaseRepoFence(context.Context, string, string) error { return nil }
func (store *fakeRepoCreateStore) CommitRepoCreateSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	if store.successErr != nil {
		return resources.Repo{}, operations.OperationRecord{}, store.successErr
	}
	store.repo = repo
	store.operation = record.Record()
	store.releasedFenceID = fenceID
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.releasedFenceID = releaseFenceID
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}
func (store *fakeRepoCreateStore) ListExportSessionsByRepo(context.Context, string) ([]sessionstate.ExportSession, error) {
	return append([]sessionstate.ExportSession(nil), store.exports...), nil
}
func (store *fakeRepoCreateStore) ListWorkloadMountBindingsByRepo(context.Context, string) ([]sessionstate.WorkloadMountBinding, error) {
	return append([]sessionstate.WorkloadMountBinding(nil), store.mounts...), nil
}
func (store *fakeRepoCreateStore) ListEarlierNonTerminalRepoLifecycleOperations(context.Context, string, string, time.Time) ([]operations.OperationRecord, error) {
	return append([]operations.OperationRecord(nil), store.blockingLifecycle...), nil
}
func (store *fakeRepoCreateStore) CommitRepoLifecycleSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	store.repo = repo
	store.operation = record.Record()
	store.releasedFenceID = fenceID
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoLifecycleFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.releasedFenceID = releaseFenceID
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoPurgeSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	store.repo = repo
	store.operation = record.Record()
	store.releasedFenceID = fenceID
	store.auditEvents = append(store.auditEvents, event)
	return repo, store.operation, nil
}
func (store *fakeRepoCreateStore) CommitRepoPurgeFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	store.operation = record.Record()
	store.releasedFenceID = releaseFenceID
	store.auditEvents = append(store.auditEvents, event)
	return store.operation, nil
}

type fakeJVSRunner struct {
	calls         []string
	payloadRoot   string
	controlRoot   string
	initSummary   jvsrunner.InitSummary
	doctorSummary jvsrunner.DoctorSummary
	initErr       error
	doctorErr     error
}

func (runner *fakeJVSRunner) Init(_ context.Context, payloadRoot, controlRoot string) (jvsrunner.InitSummary, error) {
	runner.calls = append(runner.calls, "init")
	runner.payloadRoot = payloadRoot
	runner.controlRoot = controlRoot
	return runner.initSummary, runner.initErr
}
func (runner *fakeJVSRunner) DoctorStrict(_ context.Context, controlRoot string) (jvsrunner.DoctorSummary, error) {
	runner.calls = append(runner.calls, "doctor")
	runner.controlRoot = controlRoot
	return runner.doctorSummary, runner.doctorErr
}

func repoCreateFence(now time.Time, fenceID, operationID string) fences.Fence {
	return fences.Fence{ID: fenceID, RepoID: "repo_alpha01", Kind: fences.KindLifecycle, HolderOperationID: operationID, Status: fences.StatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
}

func assertNoRepoExecLeak(t *testing.T, operation operations.OperationRecord, events []audit.Event) {
	t.Helper()
	rendered := strings.ToLower(strings.Join([]string{operation.ID, operation.Phase, operation.CorrelationID, operation.CallerService, fmt.Sprint(operation.Error), fmt.Sprint(operation.JVSJSONOutput), fmt.Sprint(operation.VerificationResult)}, " "))
	rendered += strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(strings.Join(func() []string {
		out := []string{}
		for _, event := range events {
			out = append(out, event.Reason)
		}
		return out
	}(), " ")), "\n", " "), "\t", " "))
	for _, leaked := range []string{"/srv/afscp", "secret", "password", "payload_root", "control_root"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("repoexec leaked %q in operation/events: %#v %#v", leaked, operation, events)
		}
	}
}
