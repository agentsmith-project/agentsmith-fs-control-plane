package workerapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"
)

func TestNewRunOnceRunnerDisabledFailsBeforeOpeningStore(t *testing.T) {
	factoryCalls := 0
	runner, err := NewRunOnceRunner(Options{
		Source: config.MapSource{},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			factoryCalls++
			return StoreHandle{}, nil
		},
	})
	if err == nil {
		t.Fatal("NewRunOnceRunner succeeded, want disabled config error")
	}
	if runner != nil {
		t.Fatalf("runner = %#v, want nil", runner)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls)
	}
}

func TestRunOnceAuditOnlyRunsStaleRecoveryBeforeDelivery(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore()
	store.recoveredAudit = []audit.OutboxRecord{workerAppAuditRecord("audit-stale", []byte(`{"audit_event_id":"audit-stale"}`))}
	store.claimedAudit = []audit.OutboxRecord{workerAppAuditRecord("audit-due", []byte(`{"audit_event_id":"audit-due"}`))}
	runner := newWorkerAppRunner(t, store, workerAppAuditConfigSource(config.MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "false",
	}), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.AuditStaleRecovery.Recovered != 1 || result.AuditDelivery.Claimed != 1 || result.AuditDelivery.Delivered != 1 {
		t.Fatalf("result = %#v, want stale recovered and one delivered", result)
	}
	if got := strings.Join(store.auditCallOrder, ","); got != "recover_stale,claim_due,mark_delivered" {
		t.Fatalf("audit call order = %q, want stale before delivery", got)
	}
	if store.auditRecoverOwner != "audit-worker" || store.auditClaimOwner != "audit-worker" || store.auditClaimLimit != 10 || store.auditRecoverLimit != 10 {
		t.Fatalf("audit owner/limit = recover %q/%d claim %q/%d", store.auditRecoverOwner, store.auditRecoverLimit, store.auditClaimOwner, store.auditClaimLimit)
	}
}

func TestRunOnceAuditOnlyAcceptsAuditStoreWithoutOperationStore(t *testing.T) {
	now := workerAppNow()
	auditStore := &fakeWorkerAppAuditStore{
		claimedAudit: []audit.OutboxRecord{workerAppAuditRecord("audit-due", []byte(`{"audit_event_id":"audit-due"}`))},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppAuditConfigSource(config.MapSource{
			"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "false",
		}),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{AuditStore: auditStore}, nil
		},
		Clock: func() time.Time { return now },
		AuditDelivererFactory: func(config.WorkerAuditDeliveryConfig) (auditdelivery.Deliverer, error) {
			return fakeWorkerAppAuditDeliverer{deliverErr: nil}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.AuditDelivery.Delivered != 1 || result.OperationRecovery.Scanned != 0 {
		t.Fatalf("result = %#v, want audit-only delivery", result)
	}
}

func TestRunOnceOperationAndAuditCanRunTogether(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore(workerAppOperationRecord(now))
	store.claimedAudit = []audit.OutboxRecord{workerAppAuditRecord("audit-due", []byte(`{"audit_event_id":"audit-due"}`))}
	runner := newWorkerAppRunner(t, store, workerAppAuditConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.OperationRecovery.Claimed != 1 || result.AuditDelivery.Delivered != 1 {
		t.Fatalf("result = %#v, want operation and audit work", result)
	}
}

func TestRunOnceAuditDeliveryFailureRecordsRetryWithoutLeakingSecret(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore()
	store.claimedAudit = []audit.OutboxRecord{workerAppAuditRecord("audit-fail", []byte(`{"audit_event_id":"audit-fail"}`))}
	store.auditDeliverErr = errors.New("sink failed token=delivery-secret")
	runner := newWorkerAppRunner(t, store, workerAppAuditConfigSource(config.MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "false",
	}), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want audit delivery count error")
	}
	if result.AuditDelivery.DeliveryFailuresRecorded != 1 || len(store.auditFailed) != 1 {
		t.Fatalf("result/failed marks = %#v/%d, want recorded failure", result.AuditDelivery, len(store.auditFailed))
	}
	if !strings.Contains(err.Error(), "audit delivery incomplete") {
		t.Fatalf("error = %q, want audit delivery incomplete", err)
	}
	if strings.Contains(store.auditFailed[0].failure.LastError, "delivery-secret") || !strings.Contains(store.auditFailed[0].failure.LastError, "[REDACTED]") {
		t.Fatalf("failure LastError = %q, want redacted", store.auditFailed[0].failure.LastError)
	}
}

func TestNewRunOnceRunnerEnabledCallsStoreFactoryWithDSN(t *testing.T) {
	store := newWorkerAppStore(workerAppOperationRecord(workerAppNow()))
	var gotDSN string
	factoryCalls := 0
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppConfigSource(nil),
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			factoryCalls++
			gotDSN = dsn
			return StoreHandle{Store: store}, nil
		},
		Clock:        func() time.Time { return workerAppNow() },
		AuditEventID: func() string { return "evt_namespace" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}
	if runner == nil {
		t.Fatal("runner nil, want configured runner")
	}
	if factoryCalls != 1 || gotDSN != "postgres://worker:password@db/afscp" {
		t.Fatalf("factory calls/dsn = %d/%q", factoryCalls, gotDSN)
	}
}

func TestRunOnceClaimsQueuedNamespaceUpsertThroughDefaultRunner(t *testing.T) {
	now := workerAppNow()
	record := workerAppOperationRecord(now)
	store := newWorkerAppStore(record)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want claimed=1 failed=0 unsupported=0", summary)
	}
	if store.namespace.ID != "ns_alpha01" || store.operation.ID != record.ID || store.operation.State != operations.OperationStateSucceeded || len(store.auditEvents) != 1 {
		t.Fatalf("atomic commit = namespace %#v operation %#v audit %#v", store.namespace, store.operation, store.auditEvents)
	}
	if store.auditEvents[0].EventID == "" {
		t.Fatal("audit event id is empty")
	}
}

func TestRunOnceClaimsQueuedNamespaceUpsertAndBindingPutThroughDefaultRunner(t *testing.T) {
	now := workerAppNow()
	namespaceRecord := workerAppOperationRecord(now)
	bindingRecord := workerAppBindingOperationRecord("op_binding", now)
	bindingRecord.CreatedAt = namespaceRecord.CreatedAt.Add(time.Minute)
	store := newWorkerAppStore(namespaceRecord, bindingRecord)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 2 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want claimed=2 failed=0 unsupported=0 manual=0", summary)
	}
	if store.namespace.ID != "ns_alpha01" || store.binding.NamespaceID != "ns_alpha01" || store.binding.DefaultVolumeID != "vol_123" {
		t.Fatalf("committed namespace/binding = %#v/%#v", store.namespace, store.binding)
	}
	if len(store.auditEvents) != 2 {
		t.Fatalf("audit events = %#v, want namespace and binding events", store.auditEvents)
	}
}

func TestRunOnceClaimsQueuedVolumeEnsureThroughDefaultRunner(t *testing.T) {
	now := workerAppNow()
	volumeRecord := workerAppVolumeOperationRecord("op_volume", now)
	store := newWorkerAppStore(volumeRecord)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want claimed=1", summary)
	}
	if store.volume.ID != "vol_123" || store.operation.Type != operations.OperationVolumeEnsure || len(store.auditEvents) != 1 {
		t.Fatalf("committed volume/operation/audit = %#v/%#v/%#v", store.volume, store.operation, store.auditEvents)
	}
}

func TestRunOnceRepoCreateDisabledDoesNotListRepoCreate(t *testing.T) {
	now := workerAppNow()
	repoRecord := workerAppRepoCreateOperationRecord("op_repo", now)
	store := newWorkerAppStore(repoRecord)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 0 || summary.Claimed != 0 || strings.Join(store.acquireIDs, ",") != "" {
		t.Fatalf("summary/acquire = %#v/%#v, want repo_create ignored while gate disabled", summary, store.acquireIDs)
	}
}

func TestRunOnceRepoCreateEnabledClaimsThroughRepoExecutor(t *testing.T) {
	now := workerAppNow()
	repoRecord := workerAppRepoCreateOperationRecord("op_repo", now)
	store := newWorkerAppStore(repoRecord)
	jvs := &workerAppFakeJVSRunner{
		initSummary:   jvsrunner.InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"},
		doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return jvs, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_repo" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want repo_create claimed", summary)
	}
	if store.repo.ID != "repo_alpha01" || store.operation.Type != operations.OperationRepoCreate || store.operation.State != operations.OperationStateSucceeded || len(store.auditEvents) != 1 {
		t.Fatalf("repo/operation/audit = %#v/%#v/%#v", store.repo, store.operation, store.auditEvents)
	}
	if strings.Join(jvs.calls, ",") != "init,doctor" {
		t.Fatalf("jvs calls = %#v, want init,doctor", jvs.calls)
	}
}

func TestRunOnceRepoLifecycleDisabledDoesNotListLifecycle(t *testing.T) {
	now := workerAppNow()
	archiveRecord := workerAppRepoLifecycleOperationRecord("op_archive", operations.OperationRepoArchive, now)
	deleteRecord := workerAppRepoLifecycleOperationRecord("op_delete", operations.OperationRepoDelete, now)
	restoreRecord := workerAppRepoLifecycleOperationRecord("op_restore_tombstoned", operations.OperationRepoRestoreTombstoned, now)
	purgeRecord := workerAppRepoPurgeOperationRecord("op_purge", now)
	store := newWorkerAppStore(archiveRecord, deleteRecord, restoreRecord, purgeRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 0 || summary.Claimed != 0 || strings.Join(store.acquireIDs, ",") != "" {
		t.Fatalf("summary/acquire = %#v/%#v, want repo lifecycle ignored while gate disabled", summary, store.acquireIDs)
	}
}

func TestRunOnceRepoLifecycleEnabledArchivesThroughLifecycleExecutor(t *testing.T) {
	now := workerAppNow()
	lifecycleRecord := workerAppRepoLifecycleOperationRecord("op_archive", operations.OperationRepoArchive, now)
	store := newWorkerAppStore(lifecycleRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoLifecycleConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_lifecycle" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want lifecycle archive claimed", summary)
	}
	if store.repo.Status != resources.RepoStatusArchived || store.operation.Type != operations.OperationRepoArchive || store.operation.State != operations.OperationStateSucceeded || store.releasedFenceID == "" || len(store.auditEvents) != 1 {
		t.Fatalf("repo/operation/release/audit = %#v/%#v/%q/%#v", store.repo, store.operation, store.releasedFenceID, store.auditEvents)
	}
}

func TestRunOnceRepoLifecycleEnabledProcessesDeleteAndRestoreTombstoned(t *testing.T) {
	now := workerAppNow()
	tests := []struct {
		name       string
		typ        operations.OperationType
		repo       resources.Repo
		wantStatus resources.RepoStatus
		wantDoctor bool
	}{
		{name: "delete", typ: operations.OperationRepoDelete, repo: workerAppRepoLifecycleResource(now, resources.RepoStatusActive), wantStatus: resources.RepoStatusTombstoned},
		{name: "restore tombstoned", typ: operations.OperationRepoRestoreTombstoned, repo: workerAppRepoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(time.Hour)), wantStatus: resources.RepoStatusActive, wantDoctor: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := workerAppRepoLifecycleOperationRecord("op_lifecycle", tt.typ, now)
			store := newWorkerAppStore(record)
			store.repo = tt.repo
			jvs := &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}
			runner, err := NewRunOnceRunner(Options{
				Source: workerAppRepoLifecycleConfigSource(nil),
				StoreFactory: func(context.Context, string) (StoreHandle, error) {
					return StoreHandle{Store: store}, nil
				},
				JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
					return jvs, nil
				},
				Clock:        func() time.Time { return now },
				AuditEventID: func() string { return "evt_lifecycle" },
			})
			if err != nil {
				t.Fatalf("NewRunOnceRunner: %v", err)
			}

			result, err := runner.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			summary := result.Summary().Operation
			if summary.Claimed != 1 || summary.Manual != 0 || summary.Failed != 0 {
				t.Fatalf("summary = %#v, want claimed success", summary)
			}
			if store.repo.Status != tt.wantStatus || store.operation.Type != tt.typ || store.operation.State != operations.OperationStateSucceeded {
				t.Fatalf("repo/operation = %#v/%#v, want %s success", store.repo, store.operation, tt.wantStatus)
			}
			if gotDoctor := strings.Contains(strings.Join(jvs.calls, ","), "doctor"); gotDoctor != tt.wantDoctor {
				t.Fatalf("doctor called = %v, want %v; calls=%#v", gotDoctor, tt.wantDoctor, jvs.calls)
			}
		})
	}
}

func TestRunOnceRepoLifecycleFinalizeCancellationReleasesSameOperationFence(t *testing.T) {
	now := workerAppNow()
	cancelRecord := workerAppRepoLifecycleOperationRecord("op_archive", operations.OperationRepoArchive, now)
	cancelRecord.State = operations.OperationStateCancelRequested
	store := newWorkerAppStore(cancelRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	store.fences = []fences.Fence{{
		ID:                "fence_op_archive",
		RepoID:            "repo_alpha01",
		Kind:              fences.KindLifecycle,
		HolderOperationID: "op_archive",
		Status:            fences.StatusActive,
		ExpiresAt:         now.Add(time.Hour),
		CreatedAt:         now.Add(-time.Hour),
		UpdatedAt:         now.Add(-time.Hour),
	}}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoLifecycleConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_lifecycle" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Finalized != 1 || summary.Claimed != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want finalized cancellation", summary)
	}
	got := store.records[cancelRecord.ID]
	if got.State != operations.OperationStateCancelled || store.releasedFenceID != "fence_op_archive" {
		t.Fatalf("operation/release = %#v/%q, want cancelled with fence release", got, store.releasedFenceID)
	}
}

func TestRunOnceRepoLifecycleActiveSessionWaitExitsZeroWithoutClaimedSuccess(t *testing.T) {
	now := workerAppNow()
	lifecycleRecord := workerAppRepoLifecycleOperationRecord("op_delete", operations.OperationRepoDelete, now)
	store := newWorkerAppStore(lifecycleRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	store.exports = []sessionstate.ExportSession{{ID: "export_active", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadOnly, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoLifecycleConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_lifecycle" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Manual != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want claimed execution that waits cleanly", summary)
	}
	if store.operation.ID != "" || store.releasedFenceID != "" {
		t.Fatalf("operation/release = %#v/%q, want no mutation while session active", store.operation, store.releasedFenceID)
	}
}

func TestRunOnceRepoLifecycleDoesNotClaimPurge(t *testing.T) {
	now := workerAppNow()
	purgeRecord := workerAppRepoLifecycleOperationRecord("op_purge", operations.OperationRepoPurge, now)
	store := newWorkerAppStore(purgeRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusTombstoned)
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoLifecycleConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_lifecycle" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 0 || summary.Claimed != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("summary/acquire = %#v/%#v, want purge ignored by lifecycle runner", summary, store.acquireIDs)
	}
}

func TestRunOnceRepoPurgeRequiresIndependentGate(t *testing.T) {
	now := workerAppNow()
	purgeRecord := workerAppRepoPurgeOperationRecord("op_purge", now)
	store := newWorkerAppStore(purgeRecord)
	store.repo = workerAppRepoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoLifecycleConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		StoragePurgerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.StoragePurger, error) {
			return &workerAppFakeStoragePurger{state: repoexec.RepoStoragePresent}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_purge" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}
	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary := result.Summary().Operation; summary.Scanned != 0 || summary.Claimed != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("summary/acquire = %#v/%#v, want purge ignored by lifecycle-only gate", summary, store.acquireIDs)
	}
}

func TestRunOnceRepoPurgeEnabledProcessesPurge(t *testing.T) {
	now := workerAppNow()
	purgeRecord := workerAppRepoPurgeOperationRecord("op_purge", now)
	store := newWorkerAppStore(purgeRecord)
	store.repo = workerAppRepoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	purger := &workerAppFakeStoragePurger{state: repoexec.RepoStoragePresent}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoPurgeConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		StoragePurgerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.StoragePurger, error) {
			return purger, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_purge" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}
	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Manual != 0 || purger.purgeCalls != 1 || store.repo.Status != resources.RepoStatusPurged {
		t.Fatalf("summary/purge/repo = %#v/%d/%#v, want purge success", summary, purger.purgeCalls, store.repo)
	}
}

func TestRunOnceRepoPurgeEnabledWithRepoCreateButLifecycleDisabledStillClaimsPurge(t *testing.T) {
	now := workerAppNow()
	purgeRecord := workerAppRepoPurgeOperationRecord("op_purge", now)
	store := newWorkerAppStore(purgeRecord)
	store.repo = workerAppRepoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	purger := &workerAppFakeStoragePurger{state: repoexec.RepoStoragePresent}
	source := workerAppRepoPurgeConfigSource(config.MapSource{"AFSCP_REPO_CREATE_RECOVERY_ENABLED": "true"})
	runner, err := NewRunOnceRunner(Options{
		Source: source,
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		StoragePurgerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.StoragePurger, error) {
			return purger, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_purge" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}
	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary := result.Summary().Operation; summary.Claimed != 1 || purger.purgeCalls != 1 || store.repo.Status != resources.RepoStatusPurged {
		t.Fatalf("summary/purge/repo = %#v/%d/%#v, want purge claimed", summary, purger.purgeCalls, store.repo)
	}
}

func TestRunOnceRepoPurgeCancelRequestedIsNotFinalized(t *testing.T) {
	now := workerAppNow()
	purgeRecord := workerAppRepoPurgeOperationRecord("op_purge", now)
	purgeRecord.State = operations.OperationStateCancelRequested
	store := newWorkerAppStore(purgeRecord)
	store.repo = workerAppRepoLifecycleTombstonedResource(now, resources.RepoStatusActive, now.Add(-2*time.Hour))
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoPurgeConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}}, nil
		},
		StoragePurgerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.StoragePurger, error) {
			return &workerAppFakeStoragePurger{state: repoexec.RepoStoragePresent}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_purge" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}
	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary := result.Summary().Operation; summary.Finalized != 0 || summary.Claimed != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("summary/acquire = %#v/%#v, want cancel_requested purge not finalized", summary, store.acquireIDs)
	}
}

func TestRunOnceSavePointCreateDisabledDoesNotListOrClaim(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary := result.Summary().Operation; summary.Scanned != 0 || summary.Claimed != 0 || store.savePointListCalls != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("summary/list/acquire = %#v/%d/%#v, want save_point_create ignored while gate disabled", summary, store.savePointListCalls, store.acquireIDs)
	}
}

func TestRunOnceSavePointCreateEnabledClaimsThroughSavePointExecutor(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		historySummary: jvsrunner.HistorySummary{
			Workspace:         "main",
			NewestSavePointID: "sp_before",
			SavePoints:        []jvsrunner.SavePointSummary{{SavePointID: "sp_before", Message: "before", CreatedAt: "2026-05-05T11:00:00Z"}},
		},
		saveSummary: jvsrunner.SaveSummary{SavePointID: "sp_after", NewestSavePointID: "sp_after", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppSavePointConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return jvs, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_savepoint" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want save_point_create claimed", summary)
	}
	if store.savePointListCalls != 1 || strings.Join(store.acquireIDs, ",") != "op_savepoint" {
		t.Fatalf("list/acquire = %d/%#v, want save_point_create listed and acquired", store.savePointListCalls, store.acquireIDs)
	}
	if strings.Join(jvs.calls, ",") != "history,save" {
		t.Fatalf("jvs calls = %#v, want history,save", jvs.calls)
	}
	if store.operation.ID != record.ID || store.operation.Type != operations.OperationSavePointCreate || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseSavePointCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded save_point_create_committed", store.operation)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeSavePointCreate || store.auditEvents[0].Outcome != audit.OutcomeSucceeded || store.auditEvents[0].Reason != "save_point_create_committed" {
		t.Fatalf("audit events = %#v, want succeeded save_point_create_committed event", store.auditEvents)
	}
}

func TestRunOnceRestorePreviewDisabledDoesNotListOrClaim(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestorePreviewOperationRecord("op_preview", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary := result.Summary().Operation; summary.Scanned != 0 || summary.Claimed != 0 || store.restorePreviewListCalls != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("summary/list/acquire = %#v/%d/%#v, want restore_preview ignored while gate disabled", summary, store.restorePreviewListCalls, store.acquireIDs)
	}
}

func TestRunOnceRestorePreviewEnabledClaimsThroughRestorePreviewExecutor(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestorePreviewOperationRecord("op_preview", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "idle", Workspace: "main"},
		restorePreviewSummary: jvsrunner.RestorePreviewSummary{PlanID: "plan_001", SourceSavePointID: "sp_001", Workspace: "main", RunCommandPresent: true},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRestorePreviewConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return jvs, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_restore_preview" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want restore_preview claimed", summary)
	}
	if store.restorePreviewListCalls != 1 || strings.Join(store.acquireIDs, ",") != "op_preview" {
		t.Fatalf("list/acquire = %d/%#v, want restore_preview listed and acquired", store.restorePreviewListCalls, store.acquireIDs)
	}
	if strings.Join(jvs.calls, ",") != "recovery_status,restore_preview" {
		t.Fatalf("jvs calls = %#v, want recovery_status,restore_preview", jvs.calls)
	}
	if store.restorePlan.ID != "plan_001" || store.operation.Type != operations.OperationRestorePreview || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestorePreviewCommitted {
		t.Fatalf("plan/operation = %#v/%#v, want pending restore plan and succeeded preview operation", store.restorePlan, store.operation)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestorePreview || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want restore preview success", store.auditEvents)
	}
}

func TestRunOnceRestorePreviewDiscardDisabledDoesNotListOrClaim(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestorePreviewDiscardOperationRecord("op_discard", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary := result.Summary().Operation; summary.Scanned != 0 || summary.Claimed != 0 || store.restorePreviewDiscardListCalls != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("summary/list/acquire = %#v/%d/%#v, want restore_preview_discard ignored while gate disabled", summary, store.restorePreviewDiscardListCalls, store.acquireIDs)
	}
}

func TestRunOnceRestorePreviewDiscardEnabledClaimsThroughDiscardExecutor(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestorePreviewDiscardOperationRecord("op_discard", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	store.previewOperation = workerAppRestorePreviewSucceededOperationRecord("op_preview01", now)
	store.restorePlan = workerAppRestorePreviewPendingPlan(now)
	jvs := &workerAppFakeJVSRunner{
		recoveryStatusSummary: jvsrunner.RecoveryStatusSummary{RestoreState: "pending_restore_preview", ActivePlanID: "plan_001", Blocking: true, Workspace: "main"},
		restoreDiscardSummary: jvsrunner.RestoreDiscardSummary{PlanID: "plan_001", PlanDiscarded: true, Workspace: "main"},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRestorePreviewDiscardConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return jvs, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_restore_preview_discard" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want restore_preview_discard claimed", summary)
	}
	if store.restorePreviewDiscardListCalls != 1 || strings.Join(store.acquireIDs, ",") != "op_discard" {
		t.Fatalf("list/acquire = %d/%#v, want restore_preview_discard listed and acquired", store.restorePreviewDiscardListCalls, store.acquireIDs)
	}
	if strings.Join(jvs.calls, ",") != "recovery_status,restore_discard" {
		t.Fatalf("jvs calls = %#v, want recovery_status,restore_discard", jvs.calls)
	}
	if store.restorePlan.ID != "plan_001" || store.restorePlan.Status != restoreplan.StatusDiscarded || store.operation.Type != operations.OperationRestorePreviewDiscard || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestorePreviewDiscardCommitted {
		t.Fatalf("plan/operation = %#v/%#v, want discarded restore plan and succeeded discard operation", store.restorePlan, store.operation)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestorePreviewDiscard || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want restore preview discard success", store.auditEvents)
	}
}

func TestRunOnceRepoLifecycleOperatorInterventionReturnsManualError(t *testing.T) {
	now := workerAppNow()
	lifecycleRecord := workerAppRepoLifecycleOperationRecord("op_restore", operations.OperationRepoRestoreArchived, now)
	store := newWorkerAppStore(lifecycleRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusArchived)
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoLifecycleConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return &workerAppFakeJVSRunner{doctorSummary: jvsrunner.DoctorSummary{RepoID: "jvs_repo_other", Healthy: true, Workspace: "main"}}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_lifecycle" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want manual intervention error")
	}
	summary := result.Summary().Operation
	if summary.Manual != 1 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want manual=1 failed=0 unsupported=0", summary)
	}
	if !strings.Contains(err.Error(), "manual=1") {
		t.Fatalf("error = %v, want manual count", err)
	}
	if store.operation.State != operations.OperationStateOperatorInterventionRequired {
		t.Fatalf("operation = %#v, want operator intervention", store.operation)
	}
}

func TestRunOnceRepoLifecycleEnabledMissingJVSConfigFailsClosed(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore(workerAppRepoLifecycleOperationRecord("op_archive", operations.OperationRepoArchive, now))

	_, err := NewRunOnceRunner(Options{
		Source: workerAppConfigSource(config.MapSource{"AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED": "true"}),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		Clock: func() time.Time { return now },
	})
	if err == nil {
		t.Fatal("NewRunOnceRunner succeeded, want lifecycle JVS config error")
	}
	if strings.Contains(err.Error(), "postgres://") || strings.Contains(err.Error(), "/srv/") {
		t.Fatalf("error leaked config detail: %v", err)
	}
}

func TestRunOnceScopedStoreDoesNotStarveVolumeBehindNonScopedRecords(t *testing.T) {
	now := workerAppNow()
	repoQueued := workerAppRepoOperationRecord("op_repo_queued", operations.OperationStateQueued, now)
	volumeRecord := workerAppVolumeOperationRecord("op_volume", now)
	volumeRecord.CreatedAt = repoQueued.CreatedAt.Add(time.Minute)
	store := newWorkerAppStore(repoQueued, volumeRecord)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(config.MapSource{"AFSCP_OPERATION_RECOVERY_LIMIT": "1"}), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 1 || summary.Claimed != 1 || summary.Finalized != 0 || summary.Unsupported != 0 || summary.Manual != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want only volume claim", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != volumeRecord.ID {
		t.Fatalf("acquire IDs = %q, want %s", got, volumeRecord.ID)
	}
	if store.records[repoQueued.ID].State != operations.OperationStateQueued {
		t.Fatalf("repo state = %q, want unchanged queued", store.records[repoQueued.ID].State)
	}
}

func TestRunOnceScopedStoreDoesNotStarveNamespaceOrBindingBehindNonScopedRecords(t *testing.T) {
	now := workerAppNow()
	repoQueued := workerAppRepoOperationRecord("op_repo_queued", operations.OperationStateQueued, now)
	namespaceRecord := workerAppOperationRecord(now)
	namespaceRecord.CreatedAt = repoQueued.CreatedAt.Add(time.Minute)
	bindingRecord := workerAppBindingOperationRecord("op_binding", now)
	bindingRecord.CreatedAt = repoQueued.CreatedAt.Add(2 * time.Minute)
	store := newWorkerAppStore(repoQueued, namespaceRecord, bindingRecord)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(config.MapSource{"AFSCP_OPERATION_RECOVERY_LIMIT": "2"}), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 2 || summary.Claimed != 2 || summary.Finalized != 0 || summary.Unsupported != 0 || summary.Manual != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want namespace and binding claimed despite earlier repo", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != namespaceRecord.ID+","+bindingRecord.ID {
		t.Fatalf("acquire IDs = %q, want namespace then binding", got)
	}
	if store.records[repoQueued.ID].State != operations.OperationStateQueued {
		t.Fatalf("repo state = %q, want unchanged queued", store.records[repoQueued.ID].State)
	}
}

func TestNewRunOnceRunnerStoreFactoryErrorsFailClosed(t *testing.T) {
	_, err := NewRunOnceRunner(Options{
		Source: workerAppConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{}, errors.New("postgres://worker:password@db/afscp ping failed")
		},
	})
	if err == nil {
		t.Fatal("NewRunOnceRunner succeeded, want store error")
	}
	if !strings.Contains(err.Error(), "open worker store") {
		t.Fatalf("error = %q, want store context", err)
	}
}

func TestOpenPostgresOperationRecoveryStorePingErrorsFailClosed(t *testing.T) {
	handle, err := OpenPostgresOperationRecoveryStore(context.Background(), "postgres://%")
	if err == nil {
		if handle.Close != nil {
			_ = handle.Close()
		}
		t.Fatal("OpenPostgresOperationRecoveryStore succeeded, want invalid DSN error")
	}
	if handle.Store != nil || handle.Close != nil {
		t.Fatalf("handle = %#v, want empty handle on ping/open error", handle)
	}
}

func TestRunOnceAppliesConfiguredTimeout(t *testing.T) {
	store := newWorkerAppStore(workerAppOperationRecord(workerAppNow()))
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(config.MapSource{"AFSCP_WORKER_RUN_ONCE_TIMEOUT": "123ms"}), workerAppNow(), nil)

	_, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if store.listDeadline.IsZero() {
		t.Fatal("store ListOperationsForRecovery saw no context deadline")
	}
	remaining := time.Until(store.listDeadline)
	if remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %v, want bounded timeout", remaining)
	}
}

func TestRunOnceReturnsCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	store := newWorkerAppStore(workerAppOperationRecord(workerAppNow()))
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), workerAppNow(), closeErr)

	result, err := runner.RunOnce(context.Background())
	if !errors.Is(err, closeErr) {
		t.Fatalf("RunOnce error = %v, want close error", err)
	}
	if result.Summary().Operation.Claimed != 1 {
		t.Fatalf("result = %#v, want operation still ran before close error", result)
	}
	if store.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", store.closeCalls)
	}
}

func TestNewJVSRunnerFromConfigRedactsBinaryReadErrors(t *testing.T) {
	rawPath := "/tmp/afscp-secret-missing-jvs-binary"
	_, err := NewJVSRunnerFromConfig(config.WorkerRepoCreateRecoveryConfig{
		Enabled:         true,
		JVSBinaryPath:   rawPath,
		JVSBinarySHA256: strings.Repeat("a", 64),
		JVSCWD:          "/var/lib/afscp/jvs-cwd",
		VolumeRoots:     map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	if err == nil {
		t.Fatal("NewJVSRunnerFromConfig succeeded, want read/checksum error")
	}
	if strings.Contains(err.Error(), rawPath) || strings.Contains(strings.ToLower(err.Error()), "secret") {
		t.Fatalf("error leaked raw binary path: %v", err)
	}
}

func TestRunOnceReturnsErrorForNonSuccessfulOperationRecoveryCounts(t *testing.T) {
	tests := []struct {
		name   string
		result recovery.OperationBatchResult
	}{
		{name: "unsupported", result: recovery.OperationBatchResult{Unsupported: 1}},
		{name: "manual", result: recovery.OperationBatchResult{Manual: 1}},
		{name: "failed", result: recovery.OperationBatchResult{Failed: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &RunOnceRunner{
				runner: worker.New(worker.Config{OperationRecovery: &fakeOperationRecoveryRunner{result: tt.result}}),
			}

			result, err := runner.RunOnce(context.Background())
			if err == nil {
				t.Fatal("RunOnce succeeded, want operation recovery count error")
			}
			if result.OperationRecovery.Unsupported != tt.result.Unsupported || result.OperationRecovery.Manual != tt.result.Manual || result.OperationRecovery.Failed != tt.result.Failed {
				t.Fatalf("result = %#v, want %#v", result.OperationRecovery, tt.result)
			}
			if !strings.Contains(err.Error(), "operation recovery incomplete") {
				t.Fatalf("error = %q, want operation recovery incomplete", err)
			}
		})
	}
}

func TestRunOnceJoinsOperationRecoveryCountErrorWithCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	runner := &RunOnceRunner{
		runner: worker.New(worker.Config{OperationRecovery: &fakeOperationRecoveryRunner{result: recovery.OperationBatchResult{Manual: 1}}}),
		close:  func() error { return closeErr },
	}

	_, err := runner.RunOnce(context.Background())
	if !errors.Is(err, closeErr) {
		t.Fatalf("RunOnce error = %v, want joined close error", err)
	}
	if !strings.Contains(err.Error(), "operation recovery incomplete") {
		t.Fatalf("error = %q, want operation recovery incomplete", err)
	}
}

func TestRunOnceScopedStoreIgnoresNonNamespaceAndCancelRequestedRecords(t *testing.T) {
	now := workerAppNow()
	namespaceRecord := workerAppOperationRecord(now)
	repoQueued := workerAppRepoOperationRecord("op_repo_queued", operations.OperationStateQueued, now)
	repoCancel := workerAppRepoOperationRecord("op_repo_cancel", operations.OperationStateCancelRequested, now)
	store := newWorkerAppStore(repoQueued, repoCancel, namespaceRecord)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(config.MapSource{"AFSCP_OPERATION_RECOVERY_LIMIT": "1"}), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 1 || summary.Claimed != 1 || summary.Finalized != 0 || summary.Unsupported != 0 || summary.Manual != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want only namespace claim", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != namespaceRecord.ID {
		t.Fatalf("acquire IDs = %q, want only %s", got, namespaceRecord.ID)
	}
	if got := store.acquirePolicies[repoCancel.ID]; got != "" {
		t.Fatalf("repo cancel acquire policy = %q, want no acquire/finalize", got)
	}
	if store.records[repoCancel.ID].State != operations.OperationStateCancelRequested {
		t.Fatalf("repo cancel state = %q, want unchanged cancel_requested", store.records[repoCancel.ID].State)
	}
}

func TestRunOnceFinalizesNamespaceUpsertCancellationWithinScope(t *testing.T) {
	now := workerAppNow()
	record := workerAppOperationRecord(now)
	record.ID = "op_namespace_cancel"
	record.State = operations.OperationStateCancelRequested
	store := newWorkerAppStore(record)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Finalized != 1 || summary.Claimed != 0 || summary.Failed != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want finalized=1 only", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != record.ID {
		t.Fatalf("acquire IDs = %q, want %s", got, record.ID)
	}
	if got := store.acquirePolicies[record.ID]; got != operations.LeaseCancelPolicyFinalize {
		t.Fatalf("cancel policy = %q, want finalize", got)
	}
	if got := store.records[record.ID]; got.State != operations.OperationStateCancelled || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("record = %#v, want cancelled without lease", got)
	}
}

func TestRunOnceFinalizesNamespaceVolumeBindingPutCancellationWithinScope(t *testing.T) {
	now := workerAppNow()
	record := workerAppBindingOperationRecord("op_binding_cancel", now)
	record.State = operations.OperationStateCancelRequested
	store := newWorkerAppStore(record)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Finalized != 1 || summary.Claimed != 0 || summary.Failed != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want finalized=1 only", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != record.ID {
		t.Fatalf("acquire IDs = %q, want %s", got, record.ID)
	}
	if got := store.acquirePolicies[record.ID]; got != operations.LeaseCancelPolicyFinalize {
		t.Fatalf("cancel policy = %q, want finalize", got)
	}
	if got := store.records[record.ID]; got.State != operations.OperationStateCancelled || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("record = %#v, want cancelled without lease", got)
	}
}

func TestRunOnceFinalizesVolumeEnsureCancellationWithinScope(t *testing.T) {
	now := workerAppNow()
	record := workerAppVolumeOperationRecord("op_volume_cancel", now)
	record.State = operations.OperationStateCancelRequested
	store := newWorkerAppStore(record)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Finalized != 1 || summary.Claimed != 0 || summary.Failed != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want finalized=1 only", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != record.ID {
		t.Fatalf("acquire IDs = %q, want %s", got, record.ID)
	}
	if got := store.acquirePolicies[record.ID]; got != operations.LeaseCancelPolicyFinalize {
		t.Fatalf("cancel policy = %q, want finalize", got)
	}
	if got := store.records[record.ID]; got.State != operations.OperationStateCancelled || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("record = %#v, want cancelled without lease", got)
	}
}

func TestRunOnceNamespaceVolumeBindingPutBadStateCandidatesBecomeManualError(t *testing.T) {
	now := workerAppNow()
	runningInvalid := workerAppBindingOperationRecord("op_binding_running_invalid", now)
	runningInvalid.State = operations.OperationStateRunning
	runningInvalid.LeaseOwner = ""
	runningInvalid.LeaseExpiresAt = nil
	operator := workerAppBindingOperationRecord("op_binding_operator", now)
	operator.State = operations.OperationStateOperatorInterventionRequired
	store := newWorkerAppStore(runningInvalid, operator)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want manual count error")
	}
	summary := result.Summary().Operation
	if summary.Scanned != 2 || summary.Manual != 2 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want scanned=2 manual=2", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != "" {
		t.Fatalf("acquire IDs = %q, want no acquire for manual candidates", got)
	}
}

func TestRunOnceVolumeEnsureBadStateCandidatesBecomeManualError(t *testing.T) {
	now := workerAppNow()
	runningInvalid := workerAppVolumeOperationRecord("op_volume_running_invalid", now)
	runningInvalid.State = operations.OperationStateRunning
	runningInvalid.LeaseOwner = ""
	runningInvalid.LeaseExpiresAt = nil
	operator := workerAppVolumeOperationRecord("op_volume_operator", now)
	operator.State = operations.OperationStateOperatorInterventionRequired
	store := newWorkerAppStore(runningInvalid, operator)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want manual count error")
	}
	summary := result.Summary().Operation
	if summary.Scanned != 2 || summary.Manual != 2 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want scanned=2 manual=2", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != "" {
		t.Fatalf("acquire IDs = %q, want no acquire for manual candidates", got)
	}
}

func TestRunOnceNamespaceUpsertBadStateCandidatesBecomeManualError(t *testing.T) {
	now := workerAppNow()
	runningInvalid := workerAppOperationRecord(now)
	runningInvalid.ID = "op_namespace_running_invalid"
	runningInvalid.State = operations.OperationStateRunning
	runningInvalid.LeaseOwner = ""
	runningInvalid.LeaseExpiresAt = nil
	operator := workerAppOperationRecord(now)
	operator.ID = "op_namespace_operator"
	operator.State = operations.OperationStateOperatorInterventionRequired
	store := newWorkerAppStore(runningInvalid, operator)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want manual count error")
	}
	summary := result.Summary().Operation
	if summary.Scanned != 2 || summary.Manual != 2 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want scanned=2 manual=2", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != "" {
		t.Fatalf("acquire IDs = %q, want no acquire for manual candidates", got)
	}
	if !strings.Contains(err.Error(), "operation recovery incomplete") {
		t.Fatalf("error = %q, want operation recovery incomplete", err)
	}
}

func TestNamespaceUpsertRecoveryStoreRejectsMismatchedAcquireWithoutMutation(t *testing.T) {
	now := workerAppNow()
	wrongPhase := workerAppOperationRecord(now)
	wrongPhase.ID = "op_namespace_wrong_phase"
	wrongPhase.Phase = "other_phase"
	tests := []operations.OperationRecord{
		workerAppRepoOperationRecord("op_repo", operations.OperationStateQueued, now),
		wrongPhase,
	}

	for _, record := range tests {
		t.Run(record.ID, func(t *testing.T) {
			store := newWorkerAppStore(record)
			scoped := operationRecoveryStore{store: store}

			_, err := scoped.AcquireOperationLease(context.Background(), record.ID, operations.LeaseRequest{
				Owner:    "worker-a",
				Duration: time.Minute,
				Now:      now,
			})
			if !errors.Is(err, operations.ErrLeaseUnavailable) {
				t.Fatalf("AcquireOperationLease error = %v, want ErrLeaseUnavailable", err)
			}
			if got := strings.Join(store.acquireIDs, ","); got != "" {
				t.Fatalf("underlying acquire IDs = %q, want no mutation for mismatched operation", got)
			}
			if got := store.records[record.ID]; got.State != record.State || got.Attempt != record.Attempt || got.LeaseOwner != record.LeaseOwner || got.LeaseExpiresAt != record.LeaseExpiresAt {
				t.Fatalf("record mutated = %#v, want unchanged %#v", got, record)
			}
		})
	}
}

func newWorkerAppRunner(t *testing.T, store *fakeWorkerAppStore, source config.MapSource, now time.Time, closeErr error) *RunOnceRunner {
	t.Helper()
	runner, err := NewRunOnceRunner(Options{
		Source: source,
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{
				Store: store,
				Close: func() error {
					store.closeCalls++
					return closeErr
				},
			}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_namespace" },
		AuditDelivererFactory: func(config.WorkerAuditDeliveryConfig) (auditdelivery.Deliverer, error) {
			return fakeWorkerAppAuditDeliverer{store: store}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}
	return runner
}

func workerAppConfigSource(overrides config.MapSource) config.MapSource {
	source := config.MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://worker:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
	}
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppAuditConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_WORKER_AUDIT_DELIVERY_ENABLED": "true",
		"AFSCP_WORKER_AUDIT_DELIVERY_OWNER":   "audit-worker",
		"AFSCP_AUDIT_DELIVERY_SINK_KIND":      "http_json",
		"AFSCP_AUDIT_DELIVERY_ENDPOINT":       "https://audit.example/sink",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRepoConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_REPO_CREATE_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":              "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":            strings.Repeat("a", 64),
		"AFSCP_JVS_CWD":                      "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                 "vol_123=/srv/afscp/volumes/vol_123",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRepoLifecycleConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":                 "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":               strings.Repeat("a", 64),
		"AFSCP_JVS_CWD":                         "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                    "vol_123=/srv/afscp/volumes/vol_123",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRepoPurgeConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppRepoLifecycleConfigSource(overrides)
	source["AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED"] = "false"
	source["AFSCP_REPO_PURGE_RECOVERY_ENABLED"] = "true"
	return source
}

func workerAppSavePointConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_SAVE_POINT_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":             "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":           strings.Repeat("a", 64),
		"AFSCP_JVS_CWD":                     "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                "vol_123=/srv/afscp/volumes/vol_123",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRestorePreviewConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_RESTORE_PREVIEW_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":                  "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                strings.Repeat("a", 64),
		"AFSCP_JVS_CWD":                          "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                     "vol_123=/srv/afscp/volumes/vol_123",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRestorePreviewDiscardConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_RESTORE_PREVIEW_DISCARD_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":                          "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                        strings.Repeat("a", 64),
		"AFSCP_JVS_CWD":                                  "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                             "vol_123=/srv/afscp/volumes/vol_123",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppOperationRecord(now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               "op_namespace",
		Type:             operations.OperationNamespaceUpsert,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseNamespaceUpsertValidate,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationNamespaceUpsert, "idem_namespace").String(),
		IdempotencyKey:   "idem_namespace",
		RequestHash:      operations.RequestHash("sha256:namespace"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"},
		NamespaceID:      "ns_alpha01",
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppRepoOperationRecord(operationID string, state operations.OperationState, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationRepoCreate,
		State:            state,
		Phase:            "allocate_repo_path",
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRepoCreate, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:repo"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha",
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppRepoCreateOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	record := workerAppRepoOperationRecord(operationID, operations.OperationStateQueued, now)
	record.Phase = operations.OperationPhaseRepoCreateValidate
	record.RepoID = "repo_alpha01"
	record.Resource = operations.ResourceRef{Type: "repo", ID: "repo_alpha01"}
	record.InputSummary = map[string]any{"namespace_id": "ns_alpha01", "target_repo_id": "repo_alpha01"}
	return record
}

func workerAppRepoLifecycleOperationRecord(operationID string, typ operations.OperationType, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             typ,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseRepoLifecycleValidate,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", typ, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:lifecycle"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		InputSummary: map[string]any{
			"repo_id":                   "repo_alpha01",
			"reason_present":            false,
			"lifecycle_policy_snapshot": map[string]any{"tombstone_retention_seconds": float64(604800)},
		},
		CreatedAt: now.Add(-time.Hour),
	}
}

func workerAppRepoPurgeOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	record := workerAppRepoLifecycleOperationRecord(operationID, operations.OperationRepoPurge, now)
	record.InputSummary = map[string]any{
		"repo_id":                      "repo_alpha01",
		"reason_present":               true,
		"product_confirmation_present": true,
		"lifecycle_policy_snapshot": map[string]any{
			"product_confirmation_present": true,
			"retention_override_requested": false,
			"operator_approval_present":    false,
			"break_glass_enabled":          false,
			"break_glass_authorized":       false,
		},
	}
	return record
}

func workerAppSavePointCreateOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationSavePointCreate,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseSavePointCreateValidate,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationSavePointCreate, "idem_savepoint").String(),
		IdempotencyKey:   "idem_savepoint",
		RequestHash:      operations.RequestHash("sha256:savepoint"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		InputSummary:     map[string]any{"message": "checkpoint"},
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppRestorePreviewOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationRestorePreview,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseRestorePreviewValidate,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreview, "idem_preview").String(),
		IdempotencyKey:   "idem_preview",
		RequestHash:      operations.RequestHash("sha256:restore-preview"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		InputSummary:     map[string]any{"save_point_id": "sp_001"},
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppRestorePreviewDiscardOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	record := workerAppRestorePreviewOperationRecord(operationID, now)
	record.Type = operations.OperationRestorePreviewDiscard
	record.Phase = operations.OperationPhaseRestorePreviewDiscardValidate
	record.IdempotencyScope = operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationRestorePreviewDiscard, "idem_discard").String()
	record.IdempotencyKey = "idem_discard"
	record.RequestHash = operations.RequestHash("sha256:restore-preview-discard")
	record.InputSummary = map[string]any{"preview_operation_id": "op_preview01"}
	return record
}

func workerAppRestorePreviewSucceededOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	record := workerAppRestorePreviewOperationRecord(operationID, now)
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseRestorePreviewCommitted
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": "plan_001"}
	record.VerificationResult = map[string]any{"restore_plan_id": "plan_001", "source_save_point_id": "sp_001", "restore_plan_status": "pending"}
	record.FinishedAt = &now
	return record
}

func workerAppRestorePreviewPendingPlan(now time.Time) restoreplan.Plan {
	return restoreplan.Plan{ID: "plan_001", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", PreviewOperationID: "op_preview01", SourceSavePointID: "sp_001", Status: restoreplan.StatusPending, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}
}

func workerAppRepoLifecycleResource(now time.Time, status resources.RepoStatus) resources.Repo {
	return resources.Repo{
		ID:                  "repo_alpha01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_repo_alpha",
		Kind:                resources.RepoKindRepo,
		Status:              status,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: status, LastLifecycleOperationID: "op_repo_create"},
		CreatedAt:           now.Add(-time.Hour),
		UpdatedAt:           now,
	}
}

func workerAppRepoLifecycleTombstonedResource(now time.Time, preDelete resources.RepoStatus, retention time.Time) resources.Repo {
	repo := workerAppRepoLifecycleResource(now, resources.RepoStatusTombstoned)
	repo.Lifecycle.RetentionExpiresAt = &retention
	repo.Lifecycle.PreDeleteStatus = preDelete
	repo.Lifecycle.LastLifecycleOperationID = "op_delete_current"
	repo.UpdatedAt = now.Add(-2 * time.Hour)
	return repo
}

func workerAppVolumeOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationVolumeEnsure,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseVolumeEnsureValidate,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "", operations.OperationVolumeEnsure, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:volume"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "volume", ID: "vol_123"},
		InputSummary: map[string]any{
			"volume_id":       "vol_123",
			"backend":         "juicefs",
			"isolation_class": "shared",
			"status":          "active",
			"capabilities":    map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		},
		CreatedAt: now.Add(-time.Hour),
	}
}

func workerAppBindingOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationNamespaceVolumeBindingPut,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseNamespaceVolumeBindingPutValidate,
		IdempotencyScope: operations.NewIdempotencyScope("agentsmith-api", "ns_alpha01", operations.OperationNamespaceVolumeBindingPut, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:binding"),
		CorrelationID:    "corr-alpha",
		CallerService:    "agentsmith-api",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "namespace_volume_binding", ID: "ns_alpha01"},
		NamespaceID:      "ns_alpha01",
		InputSummary:     workerAppBindingInputSummary("ns_alpha01"),
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppBindingInputSummary(namespaceID string) map[string]any {
	return map[string]any{
		"namespace_id":        namespaceID,
		"default_volume_id":   "vol_123",
		"allowed_callers":     []any{map[string]any{"caller_service": "agentsmith-api", "roles": []any{"repo_admin", "operation_inspector"}}},
		"quota_bytes_default": float64(4096),
		"export_policy":       map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		"lifecycle_policy":    map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		"mount_policy":        map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		"template_policy":     map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		"status":              "active",
	}
}

func workerAppNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

type fakeWorkerAppStore struct {
	records                        map[string]operations.OperationRecord
	order                          []string
	volume                         resources.Volume
	repo                           resources.Repo
	namespace                      resources.Namespace
	binding                        resources.NamespaceVolumeBinding
	exports                        []sessionstate.ExportSession
	mounts                         []sessionstate.WorkloadMountBinding
	operation                      operations.OperationRecord
	previewOperation               operations.OperationRecord
	restorePlan                    restoreplan.Plan
	auditEvents                    []audit.Event
	fences                         []fences.Fence
	releasedFenceID                string
	listDeadline                   time.Time
	closeCalls                     int
	acquireIDs                     []string
	acquirePolicies                map[string]operations.LeaseCancelPolicy
	recoveredAudit                 []audit.OutboxRecord
	claimedAudit                   []audit.OutboxRecord
	auditCallOrder                 []string
	auditRecoverOwner              string
	auditRecoverLimit              int
	auditClaimOwner                string
	auditClaimLimit                int
	savePointListCalls             int
	restorePreviewListCalls        int
	restorePreviewDiscardListCalls int
	auditDelivered                 []string
	auditFailed                    []workerAppAuditFailedCall
	auditDeliverErr                error
}

type workerAppAuditFailedCall struct {
	eventID string
	failure audit.DeliveryFailure
}

func newWorkerAppStore(records ...operations.OperationRecord) *fakeWorkerAppStore {
	store := &fakeWorkerAppStore{records: map[string]operations.OperationRecord{}, acquirePolicies: map[string]operations.LeaseCancelPolicy{}}
	for _, record := range records {
		store.records[record.ID] = record
		store.order = append(store.order, record.ID)
	}
	return store
}

func (store *fakeWorkerAppStore) ListDueAuditOutboxRecords(context.Context, time.Time, int) ([]audit.OutboxRecord, error) {
	return nil, errors.New("ListDueAuditOutboxRecords must not be used by workerapp audit delivery")
}

func (store *fakeWorkerAppStore) RecoverStaleAuditOutboxRecords(_ context.Context, owner string, _ time.Duration, limit int, _ audit.DeliveryFailure) ([]audit.OutboxRecord, error) {
	store.auditCallOrder = append(store.auditCallOrder, "recover_stale")
	store.auditRecoverOwner = owner
	store.auditRecoverLimit = limit
	out := make([]audit.OutboxRecord, len(store.recoveredAudit))
	copy(out, store.recoveredAudit)
	return out, nil
}

func (store *fakeWorkerAppStore) ClaimDueAuditOutboxRecords(_ context.Context, owner string, _ time.Time, limit int) ([]audit.OutboxRecord, error) {
	store.auditCallOrder = append(store.auditCallOrder, "claim_due")
	store.auditClaimOwner = owner
	store.auditClaimLimit = limit
	out := make([]audit.OutboxRecord, len(store.claimedAudit))
	copy(out, store.claimedAudit)
	return out, nil
}

func (store *fakeWorkerAppStore) MarkAuditOutboxDelivered(_ context.Context, eventID string, _ time.Time) error {
	store.auditCallOrder = append(store.auditCallOrder, "mark_delivered")
	store.auditDelivered = append(store.auditDelivered, eventID)
	return nil
}

func (store *fakeWorkerAppStore) MarkAuditOutboxDeliveryFailed(_ context.Context, eventID string, failure audit.DeliveryFailure) error {
	store.auditCallOrder = append(store.auditCallOrder, "mark_failed")
	store.auditFailed = append(store.auditFailed, workerAppAuditFailedCall{eventID: eventID, failure: failure})
	return nil
}

func (store *fakeWorkerAppStore) ListNamespaceUpsertOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if deadline, ok := ctx.Deadline(); ok {
		store.listDeadline = deadline
	}
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationNamespaceUpsert || record.Phase != operations.OperationPhaseNamespaceUpsertValidate {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
			continue
		case operations.OperationStateRunning, operations.OperationStateCancelRequested:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) ListVolumeEnsureOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if deadline, ok := ctx.Deadline(); ok {
		store.listDeadline = deadline
	}
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationVolumeEnsure || record.Phase != operations.OperationPhaseVolumeEnsureValidate || strings.TrimSpace(record.NamespaceID) != "" {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
		case operations.OperationStateRunning, operations.OperationStateCancelRequested:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) ListNamespaceVolumeBindingPutOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if deadline, ok := ctx.Deadline(); ok {
		store.listDeadline = deadline
	}
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationNamespaceVolumeBindingPut || record.Phase != operations.OperationPhaseNamespaceVolumeBindingPutValidate {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
			continue
		case operations.OperationStateRunning, operations.OperationStateCancelRequested:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func namespaceRecoveryLeaseDue(record operations.OperationRecord, now time.Time) bool {
	owner := strings.TrimSpace(record.LeaseOwner)
	hasOwner := owner != ""
	hasExpiry := record.LeaseExpiresAt != nil
	if !hasOwner && !hasExpiry {
		return true
	}
	if hasOwner != hasExpiry || (record.LeaseOwner != "" && !hasOwner) {
		return true
	}
	return !record.LeaseExpiresAt.After(now)
}

func (store *fakeWorkerAppStore) AcquireNamespaceUpsertOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationNamespaceUpsert || record.Phase != operations.OperationPhaseNamespaceUpsertValidate {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) AcquireVolumeEnsureOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationVolumeEnsure || record.Phase != operations.OperationPhaseVolumeEnsureValidate || strings.TrimSpace(record.NamespaceID) != "" {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) AcquireNamespaceVolumeBindingPutOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationNamespaceVolumeBindingPut || record.Phase != operations.OperationPhaseNamespaceVolumeBindingPutValidate {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) ListRepoCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if deadline, ok := ctx.Deadline(); ok {
		store.listDeadline = deadline
	}
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationRepoCreate || record.Phase != operations.OperationPhaseRepoCreateValidate {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
		case operations.OperationStateRunning, operations.OperationStateCancelRequested:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) ListRepoLifecycleOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if deadline, ok := ctx.Deadline(); ok {
		store.listDeadline = deadline
	}
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if !workerAppRepoLifecycleSupportedType(record.Type) || record.Phase != operations.OperationPhaseRepoLifecycleValidate {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
		case operations.OperationStateRunning, operations.OperationStateCancelRequested:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) ListRepoPurgeOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationRepoPurge || record.Phase != operations.OperationPhaseRepoLifecycleValidate {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
		case operations.OperationStateRunning:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) ListSavePointCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	store.savePointListCalls++
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationSavePointCreate || (record.Phase != operations.OperationPhaseSavePointCreateValidate && record.Phase != operations.OperationPhaseSavePointCreatePrepared) {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
		case operations.OperationStateRunning:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) ListRestorePreviewOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	store.restorePreviewListCalls++
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationRestorePreview || (record.Phase != operations.OperationPhaseRestorePreviewValidate && record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle) {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
		case operations.OperationStateRunning:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		case operations.OperationStateCancelRequested:
			if record.Phase == operations.OperationPhaseRestorePreviewValidate && namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) ListRestorePreviewDiscardOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	store.restorePreviewDiscardListCalls++
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationRestorePreviewDiscard || (record.Phase != operations.OperationPhaseRestorePreviewDiscardValidate && record.Phase != operations.OperationPhaseRestorePreviewDiscarding) {
			continue
		}
		switch record.State {
		case operations.OperationStateQueued, operations.OperationStateOperatorInterventionRequired:
			out = append(out, record)
		case operations.OperationStateRunning:
			if namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		case operations.OperationStateCancelRequested:
			if record.Phase == operations.OperationPhaseRestorePreviewDiscardValidate && namespaceRecoveryLeaseDue(record, now) {
				out = append(out, record)
			}
		}
	}
	return out, nil
}

func (store *fakeWorkerAppStore) AcquireRepoCreateOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationRepoCreate || record.Phase != operations.OperationPhaseRepoCreateValidate {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	if decision.Action == operations.LeaseActionFinalizeCancellation {
		for idx, fence := range store.fences {
			if fence.RepoID == record.RepoID && fence.Kind == fences.KindLifecycle && fence.HolderOperationID == record.ID && fence.Status == fences.StatusActive && fence.ReleasedAt == nil && fence.RecoveredAt == nil {
				releasedAt := request.Now
				store.fences[idx].Status = fences.StatusReleased
				store.fences[idx].ReleasedAt = &releasedAt
				store.fences[idx].UpdatedAt = releasedAt
				store.releasedFenceID = fence.ID
				break
			}
		}
	}
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) AcquireRepoLifecycleOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if !workerAppRepoLifecycleSupportedType(record.Type) || record.Phase != operations.OperationPhaseRepoLifecycleValidate {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	if decision.Action == operations.LeaseActionFinalizeCancellation {
		for idx, fence := range store.fences {
			if fence.RepoID == record.RepoID && fence.Kind == fences.KindLifecycle && fence.HolderOperationID == record.ID && fence.Status == fences.StatusActive && fence.ReleasedAt == nil && fence.RecoveredAt == nil {
				releasedAt := request.Now
				store.fences[idx].Status = fences.StatusReleased
				store.fences[idx].ReleasedAt = &releasedAt
				store.fences[idx].UpdatedAt = releasedAt
				store.releasedFenceID = fence.ID
				break
			}
		}
	}
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) AcquireRepoPurgeOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationRepoPurge || record.Phase != operations.OperationPhaseRepoLifecycleValidate || request.CancelPolicy != operations.LeaseCancelPolicyNone {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) AcquireSavePointCreateOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationSavePointCreate || (record.Phase != operations.OperationPhaseSavePointCreateValidate && record.Phase != operations.OperationPhaseSavePointCreatePrepared) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) AcquireRestorePreviewOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationRestorePreview || (record.Phase != operations.OperationPhaseRestorePreviewValidate && record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if request.CancelPolicy == operations.LeaseCancelPolicyFinalize && record.Phase != operations.OperationPhaseRestorePreviewValidate {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) AcquireRestorePreviewDiscardOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationRestorePreviewDiscard || (record.Phase != operations.OperationPhaseRestorePreviewDiscardValidate && record.Phase != operations.OperationPhaseRestorePreviewDiscarding) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if request.CancelPolicy == operations.LeaseCancelPolicyFinalize && record.Phase != operations.OperationPhaseRestorePreviewDiscardValidate {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	store.acquireIDs = append(store.acquireIDs, operationID)
	store.acquirePolicies[operationID] = request.CancelPolicy
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func workerAppRepoLifecycleSupportedType(typ operations.OperationType) bool {
	switch typ {
	case operations.OperationRepoArchive, operations.OperationRepoRestoreArchived, operations.OperationRepoDelete, operations.OperationRepoRestoreTombstoned:
		return true
	default:
		return false
	}
}

func (store *fakeWorkerAppStore) CommitNamespaceUpsertWithLease(_ context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.namespace = namespace
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return namespace, operation, nil
}

func (store *fakeWorkerAppStore) CommitVolumeEnsureWithLease(_ context.Context, volume resources.Volume, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Volume, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.volume = volume
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return volume, operation, nil
}

func (store *fakeWorkerAppStore) CommitNamespaceVolumeBindingPutWithLease(_ context.Context, binding resources.NamespaceVolumeBinding, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.NamespaceVolumeBinding, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.binding = binding
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return binding, operation, nil
}

func (store *fakeWorkerAppStore) CommitRepoCreateSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.repo = repo
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	store.releasedFenceID = fenceID
	return repo, operation, nil
}

func (store *fakeWorkerAppStore) CommitRepoCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	store.releasedFenceID = releaseFenceID
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitRepoLifecycleSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.repo = repo
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	store.releasedFenceID = fenceID
	return repo, operation, nil
}

func (store *fakeWorkerAppStore) CommitRepoLifecycleFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	store.releasedFenceID = releaseFenceID
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitRepoPurgeSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.repo = repo
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	store.releasedFenceID = fenceID
	return repo, operation, nil
}

func (store *fakeWorkerAppStore) CommitRepoPurgeFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	store.releasedFenceID = releaseFenceID
	return operation, nil
}

func (store *fakeWorkerAppStore) UpdateSavePointCreateProgressWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time) (operations.OperationRecord, error) {
	operation := record.Record()
	store.records[operation.ID] = operation
	store.operation = operation
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitSavePointCreateSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitSavePointCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
}

func (store *fakeWorkerAppStore) UpdateRestorePreviewPreflightWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time) (operations.OperationRecord, error) {
	operation := record.Record()
	store.records[operation.ID] = operation
	store.operation = operation
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitRestorePreviewSucceededWithLease(_ context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.restorePlan = plan
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return plan, operation, nil
}

func (store *fakeWorkerAppStore) CommitRestorePreviewFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
}

func (store *fakeWorkerAppStore) MarkRestorePreviewDiscardingWithLease(_ context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, _ string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	operation := record.Record()
	store.records[operation.ID] = operation
	plan.Status = restoreplan.StatusDiscarding
	plan.UpdatedAt = now
	store.restorePlan = plan
	store.operation = operation
	return plan, operation, nil
}

func (store *fakeWorkerAppStore) CommitRestorePreviewDiscardSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.restorePlan.Status = restoreplan.StatusDiscarded
	store.restorePlan.UpdatedAt = now
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return store.restorePlan, operation, nil
}

func (store *fakeWorkerAppStore) CommitRestorePreviewDiscardFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	if operation.Phase == operations.OperationPhaseRestorePreviewDiscarding {
		store.restorePlan.Status = restoreplan.StatusOperatorInterventionRequired
		store.restorePlan.UpdatedAt = now
	}
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
}

func (store *fakeWorkerAppStore) GetOperation(_ context.Context, operationID string) (operations.OperationRecord, error) {
	if store.previewOperation.ID == operationID {
		return store.previewOperation, nil
	}
	if record, ok := store.records[operationID]; ok {
		return record, nil
	}
	return operations.OperationRecord{}, errors.New("operation not found")
}

func (store *fakeWorkerAppStore) GetRestorePlanByPreviewOperation(_ context.Context, previewOperationID string) (restoreplan.Plan, error) {
	if store.restorePlan.PreviewOperationID == previewOperationID {
		return store.restorePlan, nil
	}
	return restoreplan.Plan{}, errors.New("restore plan not found")
}

func (store *fakeWorkerAppStore) GetActiveRestorePlanByRepo(context.Context, string) (restoreplan.Plan, error) {
	return store.restorePlan, nil
}

func (store *fakeWorkerAppStore) CreatePendingRestorePlan(_ context.Context, plan restoreplan.Plan) error {
	store.restorePlan = plan
	return nil
}

func (store *fakeWorkerAppStore) TransitionRestorePlanStatus(_ context.Context, _ string, _, to restoreplan.Status, now time.Time) (restoreplan.Plan, error) {
	store.restorePlan.Status = to
	store.restorePlan.UpdatedAt = now
	return store.restorePlan, nil
}

func (store *fakeWorkerAppStore) GetRepoInNamespace(context.Context, string, string) (resources.Repo, error) {
	if store.repo.ID == "" {
		return resources.Repo{}, errors.New("repo not found")
	}
	return store.repo, nil
}

func (store *fakeWorkerAppStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return resources.Namespace{ID: "ns_alpha01", Status: resources.NamespaceStatusActive, CreatedAt: workerAppNow().Add(-time.Hour), UpdatedAt: workerAppNow()}, nil
}

func (store *fakeWorkerAppStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	return resources.NamespaceVolumeBinding{
		NamespaceID:       "ns_alpha01",
		DefaultVolumeID:   "vol_123",
		AllowedCallers:    []resources.AllowedCaller{{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}}},
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         workerAppNow().Add(-time.Hour),
		UpdatedAt:         workerAppNow(),
	}, nil
}

func (store *fakeWorkerAppStore) GetVolume(context.Context, string) (resources.Volume, error) {
	return resources.Volume{ID: "vol_123", Backend: resources.VolumeBackendJuiceFS, IsolationClass: resources.VolumeIsolationShared, Status: resources.VolumeStatusActive, Capabilities: map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false}, CreatedAt: workerAppNow().Add(-time.Hour), UpdatedAt: workerAppNow()}, nil
}

func (store *fakeWorkerAppStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return store.fences, nil
}

func (store *fakeWorkerAppStore) CreateRepoFence(_ context.Context, fence fences.Fence) error {
	store.fences = append(store.fences, fence)
	return nil
}

func (store *fakeWorkerAppStore) ListExportSessionsByRepo(context.Context, string) ([]sessionstate.ExportSession, error) {
	return store.exports, nil
}

func (store *fakeWorkerAppStore) ListWorkloadMountBindingsByRepo(context.Context, string) ([]sessionstate.WorkloadMountBinding, error) {
	return store.mounts, nil
}

func (store *fakeWorkerAppStore) ListEarlierNonTerminalRepoLifecycleOperations(context.Context, string, string, time.Time) ([]operations.OperationRecord, error) {
	return nil, nil
}

type fakeOperationRecoveryRunner struct {
	result recovery.OperationBatchResult
	err    error
}

func (runner *fakeOperationRecoveryRunner) RunOnce(context.Context) (recovery.OperationBatchResult, error) {
	return runner.result, runner.err
}

type fakeWorkerAppAuditDeliverer struct {
	store      *fakeWorkerAppStore
	deliverErr error
}

func (deliverer fakeWorkerAppAuditDeliverer) DeliverAuditOutboxRecord(context.Context, audit.OutboxRecord) error {
	if deliverer.store == nil {
		return deliverer.deliverErr
	}
	return deliverer.store.auditDeliverErr
}

type fakeWorkerAppAuditStore struct {
	recoveredAudit []audit.OutboxRecord
	claimedAudit   []audit.OutboxRecord
	delivered      []string
}

func (store *fakeWorkerAppAuditStore) ListDueAuditOutboxRecords(context.Context, time.Time, int) ([]audit.OutboxRecord, error) {
	return nil, errors.New("ListDueAuditOutboxRecords must not be used by workerapp audit delivery")
}

func (store *fakeWorkerAppAuditStore) RecoverStaleAuditOutboxRecords(context.Context, string, time.Duration, int, audit.DeliveryFailure) ([]audit.OutboxRecord, error) {
	out := make([]audit.OutboxRecord, len(store.recoveredAudit))
	copy(out, store.recoveredAudit)
	return out, nil
}

func (store *fakeWorkerAppAuditStore) ClaimDueAuditOutboxRecords(context.Context, string, time.Time, int) ([]audit.OutboxRecord, error) {
	out := make([]audit.OutboxRecord, len(store.claimedAudit))
	copy(out, store.claimedAudit)
	return out, nil
}

func (store *fakeWorkerAppAuditStore) MarkAuditOutboxDelivered(_ context.Context, eventID string, _ time.Time) error {
	store.delivered = append(store.delivered, eventID)
	return nil
}

func (store *fakeWorkerAppAuditStore) MarkAuditOutboxDeliveryFailed(context.Context, string, audit.DeliveryFailure) error {
	return nil
}

type workerAppFakeJVSRunner struct {
	calls                 []string
	initSummary           jvsrunner.InitSummary
	doctorSummary         jvsrunner.DoctorSummary
	saveSummary           jvsrunner.SaveSummary
	historySummary        jvsrunner.HistorySummary
	recoveryStatusSummary jvsrunner.RecoveryStatusSummary
	restorePreviewSummary jvsrunner.RestorePreviewSummary
	restoreDiscardSummary jvsrunner.RestoreDiscardSummary
}

type workerAppFakeStoragePurger struct {
	state      repoexec.RepoStorageState
	purgeCalls int
}

func (purger *workerAppFakeStoragePurger) InspectRepoStorage(context.Context, repoexec.RepoStoragePaths) (repoexec.RepoStorageState, error) {
	return purger.state, nil
}

func (purger *workerAppFakeStoragePurger) PurgeRepoStorage(context.Context, repoexec.RepoStoragePaths) error {
	purger.purgeCalls++
	purger.state = repoexec.RepoStorageAbsent
	return nil
}

func (runner *workerAppFakeJVSRunner) Init(context.Context, string, string) (jvsrunner.InitSummary, error) {
	runner.calls = append(runner.calls, "init")
	return runner.initSummary, nil
}

func (runner *workerAppFakeJVSRunner) DoctorStrict(context.Context, string) (jvsrunner.DoctorSummary, error) {
	runner.calls = append(runner.calls, "doctor")
	return runner.doctorSummary, nil
}

func (runner *workerAppFakeJVSRunner) Save(context.Context, string, string) (jvsrunner.SaveSummary, error) {
	runner.calls = append(runner.calls, "save")
	if runner.saveSummary.SavePointID == "" {
		runner.saveSummary = jvsrunner.SaveSummary{SavePointID: "sp_001", NewestSavePointID: "sp_001", Workspace: "main", CreatedAt: "2026-05-05T12:00:00Z"}
	}
	return runner.saveSummary, nil
}

func (runner *workerAppFakeJVSRunner) History(context.Context, string) (jvsrunner.HistorySummary, error) {
	runner.calls = append(runner.calls, "history")
	return runner.historySummary, nil
}

func (runner *workerAppFakeJVSRunner) RecoveryStatus(context.Context, string) (jvsrunner.RecoveryStatusSummary, error) {
	runner.calls = append(runner.calls, "recovery_status")
	return runner.recoveryStatusSummary, nil
}

func (runner *workerAppFakeJVSRunner) RestorePreview(context.Context, string, string) (jvsrunner.RestorePreviewSummary, error) {
	runner.calls = append(runner.calls, "restore_preview")
	return runner.restorePreviewSummary, nil
}

func (runner *workerAppFakeJVSRunner) RestoreDiscard(context.Context, string, string) (jvsrunner.RestoreDiscardSummary, error) {
	runner.calls = append(runner.calls, "restore_discard")
	return runner.restoreDiscardSummary, nil
}

func workerAppAuditRecord(eventID string, payload []byte) audit.OutboxRecord {
	now := workerAppNow()
	return audit.OutboxRecord{
		EventID:         eventID,
		EventType:       audit.EventTypeRepoCreate,
		EventTime:       now.Add(-time.Minute),
		PayloadJSON:     append([]byte(nil), payload...),
		Status:          audit.OutboxStatusDelivering,
		DeliveryAttempt: 1,
		CreatedAt:       now.Add(-time.Minute),
		UpdatedAt:       now,
	}
}
