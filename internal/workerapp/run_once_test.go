package workerapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/capability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restorereconcile"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

const (
	acceptedJVSBinarySHA256             = "fa4ada8e3353f85679d13870ea53307caafbd8217b04ba576b185105d9178cef"
	directRestoreReleaseJVSBinaryPath   = "/usr/local/bin/jvs"
	directRestoreReleaseJVSBinarySHA256 = "fa4ada8e3353f85679d13870ea53307caafbd8217b04ba576b185105d9178cef"
	directRestoreReleaseJVSSourceRef    = "jvs@v0.4.10:6a0f762bc436f0d3dc7c7c1d60847992c3a82718"
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

func TestWorkerCapabilityMatrixExecutorRegistryMatchesDecisionRows(t *testing.T) {
	registered := workerCapabilityMatrixExecutionOperationTypes()
	for _, row := range capability.DecisionRowsForSurface(capability.SurfaceWorkerExecution) {
		if row.OperationType == "" || !row.Supported || workerAppSeparateWorkerSurface(row.OperationType) {
			continue
		}
		if !registered[row.OperationType] {
			t.Fatalf("%s worker-execution decision row has no worker runtime registration: %#v", row.OperationType, row)
		}
	}
	if registered[operations.OperationMigrationCutover] {
		t.Fatalf("migration_cutover must stay conditional unsupported, not registered as executable tooling")
	}
}

func TestWorkerCapabilityMatrixRecoveryRegistryMatchesDecisionRows(t *testing.T) {
	scanned := workerCapabilityMatrixRecoveryOperationTypes()
	for _, row := range capability.DecisionRowsForSurface(capability.SurfaceWorkerRecovery) {
		if row.OperationType == "" || !row.Supported || workerAppSeparateWorkerSurface(row.OperationType) {
			continue
		}
		if !scanned[row.OperationType] {
			t.Fatalf("%s worker-recovery decision row has no historical recovery scan registration: %#v", row.OperationType, row)
		}
	}
	if scanned[operations.OperationMigrationCutover] {
		t.Fatalf("migration_cutover must stay conditional unsupported/recovery-only until migration tooling exists")
	}
	if scanned[operations.OperationExportSessionReconcile] {
		t.Fatalf("export_session_reconcile uses the export reconcile worker surface, not operation recovery registry")
	}
}

func TestWorkerCapabilityMatrixUnsupportedTerminalizationRegistryIncludesDisabledOperations(t *testing.T) {
	terminalized := workerCapabilityMatrixUnsupportedTerminalizationOperationTypes()
	for _, operationType := range []operations.OperationType{
		operations.OperationRepoCreate,
		operations.OperationRepoPurge,
		operations.OperationTemplateCreate,
		operations.OperationTemplateClone,
		operations.OperationRestore,
	} {
		if !terminalized[operationType] {
			t.Fatalf("%s disabled or missing handler recovery must persist operator intervention and audit through matrix terminalization registry", operationType)
		}
	}
}

func workerAppSeparateWorkerSurface(operationType operations.OperationType) bool {
	return operationType == operations.OperationExportSessionReconcile
}

func TestRunOnceWorkloadMountBindingCreateClaimsThroughMountBindingExecutor(t *testing.T) {
	assertRunOnceWorkloadMountBindingOperationClaimsThroughExecutor(t, operations.OperationMountBindingCreate, audit.EventTypeMountBindingCreate)
}

func TestRunOnceWorkloadMountBindingStatusUpdateClaimsThroughMountBindingExecutor(t *testing.T) {
	assertRunOnceWorkloadMountBindingOperationClaimsThroughExecutor(t, operations.OperationMountBindingStatusUpdate, audit.EventTypeMountBindingStatusUpdate)
}

func TestRunOnceWorkloadMountBindingHeartbeatClaimsThroughMountBindingExecutor(t *testing.T) {
	assertRunOnceWorkloadMountBindingOperationClaimsThroughExecutor(t, operations.OperationMountBindingHeartbeat, audit.EventTypeMountBindingHeartbeat)
}

func TestRunOnceWorkloadMountBindingReleaseClaimsThroughMountBindingExecutor(t *testing.T) {
	assertRunOnceWorkloadMountBindingOperationClaimsThroughExecutor(t, operations.OperationMountBindingRelease, audit.EventTypeMountBindingRelease)
}

func TestRunOnceWorkloadMountBindingRevokeClaimsThroughMountBindingExecutor(t *testing.T) {
	assertRunOnceWorkloadMountBindingOperationClaimsThroughExecutor(t, operations.OperationMountBindingRevoke, audit.EventTypeMountBindingRevoke)
}

func assertRunOnceWorkloadMountBindingOperationClaimsThroughExecutor(t *testing.T, operationType operations.OperationType, eventType audit.EventType) {
	t.Helper()

	now := workerAppNow()
	record := workerAppWorkloadMountBindingOperationRecord("op_"+operationType.String(), operationType, now)
	store := newWorkerAppStore(record)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 1 || summary.Claimed != 1 || summary.Finalized != 0 || summary.Unsupported != 0 || summary.Manual != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want workload mount binding claim", summary)
	}
	if store.workloadMountBindingListCalls != 1 || strings.Join(store.acquireIDs, ",") != record.ID {
		t.Fatalf("list/acquire = %d/%#v, want workload mount binding list and acquire %s", store.workloadMountBindingListCalls, store.acquireIDs, record.ID)
	}
	got := store.records[record.ID]
	if got.State != operations.OperationStateSucceeded || got.Phase != workerAppWorkloadMountBindingCommittedPhase(operationType) {
		t.Fatalf("operation = %#v, want succeeded committed workload mount binding operation", got)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != eventType || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want succeeded %s audit", store.auditEvents, eventType)
	}
}

func TestRunOnceExportSessionReconcileOnlyRunsWhenExplicitlyEnabled(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore()
	store.exportReconcileCandidates = []exportaccess.Session{workerAppExportAccessSession(now, "export_drain01", sessionstate.ExportStatusRevoking, now.Add(time.Hour))}
	runner, err := NewRunOnceRunner(Options{
		Source: config.MapSource{
			"AFSCP_EXPORT_SESSION_RECONCILE_ENABLED": "true",
			"AFSCP_POSTGRES_DSN":                     "postgres://worker:password@db/afscp",
			"AFSCP_EXPORT_SESSION_RECONCILE_OWNER":   "export-worker",
		},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{ExportReconcileStore: store}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_export_reconcile" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary()
	if summary.ExportSessionReconcile.Scanned != 1 || summary.ExportSessionReconcile.Terminalized != 1 || summary.Operation.Scanned != 0 {
		t.Fatalf("summary = %#v, want export-only terminalize", summary)
	}
	if len(store.exportReconciles) != 1 || store.exportReconciles[0].Operation.CallerService != "export-worker" {
		t.Fatalf("export reconciles = %#v, want owner-built request", store.exportReconciles)
	}
}

func TestRunOnceExportSessionReconcileRunsBeforeOperationRecovery(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore(workerAppOperationRecord(now))
	store.exportReconcileCandidates = []exportaccess.Session{workerAppExportAccessSession(now, "export_drain01", sessionstate.ExportStatusRevoking, now.Add(time.Hour))}
	runner := newWorkerAppRunner(t, store, workerAppExportReconcileConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.ExportSessionReconcile.Terminalized != 1 || result.OperationRecovery.Claimed != 1 {
		t.Fatalf("result = %#v, want export reconcile and operation recovery", result)
	}
	if got := strings.Join(store.workerCallOrder[:2], ","); got != "export_reconcile,operation_recovery" {
		t.Fatalf("worker call order = %#v, want export reconcile before operation recovery", store.workerCallOrder)
	}
}

func TestRunOnceRestoreReconciliationOnlyRunsWhenExplicitlyEnabled(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore()
	store.restoreReconciliationRun = restorereconcile.Run{ID: "rrun_123", Mode: restorereconcile.ModeReconciling}
	store.restoreReconciliationTargets, store.restoreReconciliationObservations = workerAppRestoreReconciliationCleanTargetAndObservation()
	runner, err := NewRunOnceRunner(Options{
		Source: config.MapSource{
			"AFSCP_RESTORE_RECONCILIATION_ENABLED": "true",
			"AFSCP_POSTGRES_DSN":                   "postgres://worker:password@db/afscp",
			"AFSCP_RESTORE_RECONCILIATION_OWNER":   "restore-worker",
		},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{RestoreReconcile: store}, nil
		},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Summary().RestoreReconciliation.Completed != 1 || store.restoreReconciliationActiveRunCalls != 1 {
		t.Fatalf("summary/calls = %#v/%d, want explicit restore reconciliation run", result.Summary().RestoreReconciliation, store.restoreReconciliationActiveRunCalls)
	}
}

func TestRunOnceRestoreReconciliationRunsBeforeOperationRecovery(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore(workerAppOperationRecord(now))
	store.restoreReconciliationRun = restorereconcile.Run{ID: "rrun_123", Mode: restorereconcile.ModeReconciling}
	store.restoreReconciliationTargets, store.restoreReconciliationObservations = workerAppRestoreReconciliationCleanTargetAndObservation()
	runner := newWorkerAppRunner(t, store, workerAppRestoreReconciliationConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Summary().RestoreReconciliation.Completed != 1 || result.Summary().Operation.Claimed != 1 {
		t.Fatalf("summary = %#v, want reconciliation and operation recovery", result.Summary())
	}
	if got := strings.Join(store.workerCallOrder[:2], ","); got != "restore_reconciliation,operation_recovery" {
		t.Fatalf("worker call order = %#v, want restore reconciliation before operation recovery", store.workerCallOrder)
	}
}

func TestRunOnceRestoreReconciliationBlockedSkipsOperationRecovery(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore(workerAppOperationRecord(now))
	store.restoreReconciliationRun = restorereconcile.Run{ID: "rrun_123", Mode: restorereconcile.ModeBlockedOperatorIntervention}
	runner := newWorkerAppRunner(t, store, workerAppRestoreReconciliationConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Summary().RestoreReconciliation.Blocked != 1 || result.Summary().Operation != (worker.OperationSummary{}) {
		t.Fatalf("summary = %#v, want blocked restore reconciliation to skip operation recovery", result.Summary())
	}
	if got := strings.Join(store.workerCallOrder, ","); got != "restore_reconciliation" {
		t.Fatalf("worker call order = %#v, want no operation recovery after blocked reconciliation", store.workerCallOrder)
	}
}

func TestRunOnceWorkloadMountStaleLeaseScanRunsAndReportsKeptBlocked(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore()
	store.staleWorkloadMountBindings = []workloadmount.Binding{{ID: "wmb_stale01"}}
	runner, err := NewRunOnceRunner(Options{
		Source: config.MapSource{
			"AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_ENABLED": "true",
			"AFSCP_POSTGRES_DSN": "postgres://worker:password@db/afscp",
			"AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_LIMIT": "7",
		},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{WorkloadMountStale: store}, nil
		},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().WorkloadMountStale
	if summary.Scanned != 1 || summary.KeptBlocked != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want scan-only kept-blocked signal", summary)
	}
	if store.workloadMountStaleLimit != 7 || !store.workloadMountStaleNow.Equal(now) {
		t.Fatalf("scan args now/limit = %v/%d, want %v/7", store.workloadMountStaleNow, store.workloadMountStaleLimit, now)
	}
}

func TestRunOnceWorkloadMountStaleLeaseScanRequiresStoreWhenEnabled(t *testing.T) {
	_, err := NewRunOnceRunner(Options{
		Source: config.MapSource{
			"AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_ENABLED": "true",
			"AFSCP_POSTGRES_DSN": "postgres://worker:password@db/afscp",
		},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{}, nil
		},
		Clock: func() time.Time { return workerAppNow() },
	})
	if err == nil || !strings.Contains(err.Error(), "workload mount stale lease store") {
		t.Fatalf("NewRunOnceRunner error = %v, want missing workload mount stale lease store", err)
	}
}

func TestRunOnceExportSessionReconcileRequiresStoreWhenEnabled(t *testing.T) {
	_, err := NewRunOnceRunner(Options{
		Source: config.MapSource{
			"AFSCP_EXPORT_SESSION_RECONCILE_ENABLED": "true",
			"AFSCP_POSTGRES_DSN":                     "postgres://worker:password@db/afscp",
			"AFSCP_EXPORT_SESSION_RECONCILE_OWNER":   "export-worker",
		},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{}, nil
		},
		Clock: func() time.Time { return workerAppNow() },
	})
	if err == nil || !strings.Contains(err.Error(), "export session reconcile store") {
		t.Fatalf("NewRunOnceRunner error = %v, want missing export store", err)
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

func TestRunOnceClaimsQueuedNamespaceDisableThroughDefaultRunner(t *testing.T) {
	now := workerAppNow()
	record := workerAppNamespaceDisableOperationRecord(now)
	store := newWorkerAppStore(record)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want claimed namespace disable", summary)
	}
	if store.namespace.ID != "ns_alpha01" || store.namespace.Status != resources.NamespaceStatusDisabled || store.namespace.DisabledAt == nil || store.namespace.DisabledReason != "security hold" {
		t.Fatalf("namespace = %#v, want disabled security hold", store.namespace)
	}
	if store.operation.ID != record.ID || store.operation.Type != operations.OperationNamespaceDisable || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseNamespaceDisableCommitted {
		t.Fatalf("operation = %#v, want committed namespace disable", store.operation)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeNamespaceDisable || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want namespace disable succeeded", store.auditEvents)
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

func TestRunOnceClaimsNamespaceAndBindingWhenJVSSavePointRuntimeUnavailable(t *testing.T) {
	now := workerAppNow()
	namespaceRecord := workerAppOperationRecord(now)
	bindingRecord := workerAppBindingOperationRecord("op_binding", now)
	bindingRecord.CreatedAt = namespaceRecord.CreatedAt.Add(time.Minute)
	store := newWorkerAppStore(namespaceRecord, bindingRecord)
	factoryCalls := 0
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppSavePointConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			factoryCalls++
			return nil, fmt.Errorf("%w: test runtime missing", ErrJVSRuntimeUnavailable)
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_namespace" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("JVS factory calls = %d, want one save-point initialization attempt", factoryCalls)
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

func TestRunOnceSavePointEnabledGenericJVSErrorFailsClosed(t *testing.T) {
	now := workerAppNow()
	namespaceRecord := workerAppOperationRecord(now)
	bindingRecord := workerAppBindingOperationRecord("op_binding", now)
	store := newWorkerAppStore(namespaceRecord, bindingRecord)

	_, err := NewRunOnceRunner(Options{
		Source: workerAppSavePointConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return nil, errors.New("generic jvs factory error")
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_namespace" },
	})
	if err == nil {
		t.Fatal("NewRunOnceRunner succeeded, want generic JVS error")
	}
	if !strings.Contains(err.Error(), "generic jvs factory error") {
		t.Fatalf("NewRunOnceRunner error = %q, want generic JVS factory error", err)
	}
	if store.genericUpdateCalls != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("store generic/acquire = %d/%#v, want fail-closed before recovery", store.genericUpdateCalls, store.acquireIDs)
	}
}

func TestRunOnceClaimsBindingPutMissingDefaultVolumeAsFailedTerminalOperation(t *testing.T) {
	now := workerAppNow()
	record := workerAppBindingOperationRecord("op_binding", now)
	store := newWorkerAppStore(record)
	store.volumeReadErr = sql.ErrNoRows
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	summary := result.Summary().Operation
	if summary.Claimed != 1 || summary.Failed != 0 || summary.Unsupported != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want claimed terminal failure without recovery error", summary)
	}
	got := store.records[record.ID]
	if got.State != operations.OperationStateFailed || got.Phase != operations.OperationPhaseNamespaceVolumeBindingPutValidate || got.Error == nil || got.Error.Code != "NAMESPACE_VOLUME_BINDING_VOLUME_NOT_ACTIVE" {
		t.Fatalf("binding operation = %#v, want typed failed missing default volume", got)
	}
	if got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("binding operation lease = %q/%v, want cleared", got.LeaseOwner, got.LeaseExpiresAt)
	}
	if store.binding.NamespaceID != "" {
		t.Fatalf("binding side effect = %#v, want no namespace_volume_bindings write", store.binding)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Outcome != audit.OutcomeFailed || store.auditEvents[0].Reason != "namespace_volume_binding_put_failed" {
		t.Fatalf("audit events = %#v, want failed binding audit", store.auditEvents)
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

func TestRunOnceRepoCreateDisabledScansAndPersistsUnsupportedIntervention(t *testing.T) {
	now := workerAppNow()
	repoRecord := workerAppRepoCreateOperationRecord("op_repo", now)
	store := newWorkerAppStore(repoRecord)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	if !strings.Contains(err.Error(), "operation recovery incomplete") {
		t.Fatalf("RunOnce error = %q, want operation recovery incomplete", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || summary.Failed != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want disabled repo_create scanned into unsupported recovery", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != repoRecord.ID {
		t.Fatalf("acquire IDs = %q, want disabled repo_create leased for intervention", got)
	}
	got := store.records[repoRecord.ID]
	if got.State != operations.OperationStateOperatorInterventionRequired || got.Error == nil || got.Error.Code != "OPERATION_RECOVERY_REQUIRED" {
		t.Fatalf("repo_create record = %#v, want persisted unsupported intervention", got)
	}
	if got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("repo_create lease = %q/%v, want cleared after intervention", got.LeaseOwner, got.LeaseExpiresAt)
	}
	if store.genericUpdateCalls != 0 {
		t.Fatalf("generic update calls = %d, want unsupported intervention committed through audit boundary", store.genericUpdateCalls)
	}
	assertWorkerAppUnsupportedAudit(t, store, repoRecord, audit.EventTypeRepoCreate)
	if store.releasedFenceID != "" {
		t.Fatalf("released fence = %q, want no executor side effects or fence release", store.releasedFenceID)
	}
}

func TestRunOnceRepoCreateEnabledButJVSUnavailableScansAndPersistsUnsupportedIntervention(t *testing.T) {
	now := workerAppNow()
	repoRecord := workerAppRepoCreateOperationRecord("op_repo", now)
	store := newWorkerAppStore(repoRecord)
	factoryCalls := 0
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			factoryCalls++
			return nil, fmt.Errorf("%w: test runtime missing", ErrJVSRuntimeUnavailable)
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_repo" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	if !strings.Contains(err.Error(), "operation recovery incomplete") {
		t.Fatalf("RunOnce error = %q, want operation recovery incomplete", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("JVS factory calls = %d, want one initialization attempt", factoryCalls)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || summary.Failed != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want repo_create scanned into unsupported recovery when JVS unavailable", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != repoRecord.ID {
		t.Fatalf("acquire IDs = %q, want repo_create leased for intervention", got)
	}
	got := store.records[repoRecord.ID]
	if got.State != operations.OperationStateOperatorInterventionRequired || got.Error == nil || got.Error.Code != "OPERATION_RECOVERY_REQUIRED" {
		t.Fatalf("repo_create record = %#v, want persisted unsupported intervention", got)
	}
	if got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("repo_create lease = %q/%v, want cleared after intervention", got.LeaseOwner, got.LeaseExpiresAt)
	}
	if store.genericUpdateCalls != 0 {
		t.Fatalf("generic update calls = %d, want unsupported intervention committed through audit boundary", store.genericUpdateCalls)
	}
	assertWorkerAppUnsupportedAudit(t, store, repoRecord, audit.EventTypeRepoCreate)
	if store.repo.ID != "" || store.releasedFenceID != "" {
		t.Fatalf("repo/released fence = %#v/%q, want no repo side effects or fence release", store.repo, store.releasedFenceID)
	}
}

func TestRunOnceRepoCreateEnabledProductionJVSUnavailableScansAndPersistsUnsupportedIntervention(t *testing.T) {
	now := workerAppNow()
	repoRecord := workerAppRepoCreateOperationRecord("op_repo", now)
	store := newWorkerAppStore(repoRecord)
	missingJVSPath := filepath.Join(t.TempDir(), "missing-jvs")
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRepoConfigSource(config.MapSource{
			"AFSCP_JVS_BINARY_PATH": missingJVSPath,
		}),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_repo" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	if !strings.Contains(err.Error(), "operation recovery incomplete") {
		t.Fatalf("RunOnce error = %q, want operation recovery incomplete", err)
	}
	summary := result.Summary().Operation
	if summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || summary.Failed != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want repo_create scanned into unsupported recovery when production JVS is unavailable", summary)
	}
	if got := strings.Join(store.acquireIDs, ","); got != repoRecord.ID {
		t.Fatalf("acquire IDs = %q, want repo_create leased for intervention", got)
	}
	got := store.records[repoRecord.ID]
	if got.State != operations.OperationStateOperatorInterventionRequired || got.Error == nil || got.Error.Code != "OPERATION_RECOVERY_REQUIRED" {
		t.Fatalf("repo_create record = %#v, want persisted unsupported intervention", got)
	}
	if got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("repo_create lease = %q/%v, want cleared after intervention", got.LeaseOwner, got.LeaseExpiresAt)
	}
	if store.genericUpdateCalls != 0 {
		t.Fatalf("generic update calls = %d, want unsupported intervention committed through audit boundary", store.genericUpdateCalls)
	}
	assertWorkerAppUnsupportedAudit(t, store, repoRecord, audit.EventTypeRepoCreate)
	if store.repo.ID != "" || store.operation.Type != operations.OperationRepoCreate || store.releasedFenceID != "" {
		t.Fatalf("repo/operation/released fence = %#v/%#v/%q, want failed audit only and no repo/JVS/fence side effect", store.repo, store.operation, store.releasedFenceID)
	}
}

func TestRunOnceRepoCreateEnabledChecksumMismatchFailsFast(t *testing.T) {
	now := workerAppNow()
	path := filepath.Join(t.TempDir(), "jvs")
	if err := os.WriteFile(path, []byte("not the accepted jvs release binary"), 0o755); err != nil {
		t.Fatalf("write fake jvs binary: %v", err)
	}
	store := newWorkerAppStore(workerAppRepoCreateOperationRecord("op_repo", now))
	_, err := NewRunOnceRunner(Options{
		Source: workerAppRepoConfigSource(config.MapSource{
			"AFSCP_JVS_BINARY_PATH": path,
		}),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_repo" },
	})
	if err == nil {
		t.Fatal("NewRunOnceRunner succeeded, want JVS checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("NewRunOnceRunner error = %q, want checksum mismatch", err)
	}
	if errors.Is(err, ErrJVSRuntimeUnavailable) {
		t.Fatalf("NewRunOnceRunner error = %v, want checksum mismatch to stay fail-fast and not runtime-unavailable recovery", err)
	}
	if store.genericUpdateCalls != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("store generic/acquire = %d/%#v, want fail-fast before recovery", store.genericUpdateCalls, store.acquireIDs)
	}
}

func TestRunOnceRepoCreateEnabledRepoExecutorConstructorErrorFailsFast(t *testing.T) {
	now := workerAppNow()
	tests := []struct {
		name    string
		factory JVSRunnerFactory
		want    string
	}{
		{
			name: "generic factory error",
			factory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
				return nil, errors.New("generic jvs factory error")
			},
			want: "generic jvs factory error",
		},
		{
			name: "nil runner",
			factory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
				return nil, nil
			},
			want: "repo create jvs runner is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newWorkerAppStore(workerAppRepoCreateOperationRecord("op_repo", now))
			_, err := NewRunOnceRunner(Options{
				Source: workerAppRepoConfigSource(nil),
				StoreFactory: func(context.Context, string) (StoreHandle, error) {
					return StoreHandle{Store: store}, nil
				},
				JVSRunnerFactory: tt.factory,
				Clock:            func() time.Time { return now },
				AuditEventID:     func() string { return "evt_repo" },
			})
			if err == nil {
				t.Fatal("NewRunOnceRunner succeeded, want fail-fast error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewRunOnceRunner error = %q, want %q", err, tt.want)
			}
			if store.genericUpdateCalls != 0 || len(store.acquireIDs) != 0 {
				t.Fatalf("store generic/acquire = %d/%#v, want fail-fast before recovery", store.genericUpdateCalls, store.acquireIDs)
			}
		})
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
	if strings.Join(jvs.calls, ",") != "init,direct_doctor" {
		t.Fatalf("jvs calls = %#v, want init,direct_doctor", jvs.calls)
	}
}

func TestRunOnceRepoLifecycleDisabledScansAndPersistsUnsupportedInterventions(t *testing.T) {
	now := workerAppNow()
	archiveRecord := workerAppRepoLifecycleOperationRecord("op_archive", operations.OperationRepoArchive, now)
	deleteRecord := workerAppRepoLifecycleOperationRecord("op_delete", operations.OperationRepoDelete, now)
	restoreRecord := workerAppRepoLifecycleOperationRecord("op_restore_tombstoned", operations.OperationRepoRestoreTombstoned, now)
	purgeRecord := workerAppRepoPurgeOperationRecord("op_purge", now)
	store := newWorkerAppStore(archiveRecord, deleteRecord, restoreRecord, purgeRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	summary := result.Summary().Operation
	if summary.Scanned != 4 || summary.Unsupported != 4 || summary.Claimed != 0 || summary.Failed != 0 || summary.Manual != 0 {
		t.Fatalf("summary = %#v, want disabled repo lifecycle/purge scanned into unsupported recovery", summary)
	}
	if len(store.acquireIDs) != 4 || store.genericUpdateCalls != 0 {
		t.Fatalf("acquire/update = %#v/%d, want four audited unsupported commits without generic update", store.acquireIDs, store.genericUpdateCalls)
	}
	if len(store.auditEvents) != 4 {
		t.Fatalf("audit events = %#v, want one failed unsupported event per lifecycle/purge operation", store.auditEvents)
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
	store.exports = []sessionstate.ExportSession{workerAppFreshExportSession(now, "export_active", sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive, now.Add(time.Hour))}
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

func TestRunOnceRepoLifecycleStaleSessionReturnsManualError(t *testing.T) {
	now := workerAppNow()
	lifecycleRecord := workerAppRepoLifecycleOperationRecord("op_delete", operations.OperationRepoDelete, now)
	store := newWorkerAppStore(lifecycleRecord)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	store.exports = []sessionstate.ExportSession{{ID: "export_stale", NamespaceID: "ns_alpha01", RepoID: "repo_alpha01", Mode: sessionstate.AccessModeReadOnly, Status: sessionstate.ExportStatusActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}}
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
	if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.releasedFenceID != "" {
		t.Fatalf("operation/release = %#v/%q, want operator intervention and retained fence", store.operation, store.releasedFenceID)
	}
}

func TestRunOnceRepoLifecycleGateScansPurgeAsUnsupportedWhenPurgeExecutorDisabled(t *testing.T) {
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
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported purge recovery count error")
	}
	summary := result.Summary().Operation
	if summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || len(store.acquireIDs) != 1 {
		t.Fatalf("summary/acquire = %#v/%#v, want purge audited unsupported, not claimed by lifecycle executor", summary, store.acquireIDs)
	}
	assertWorkerAppUnsupportedAudit(t, store, purgeRecord, audit.EventTypeRepoPurge)
}

func TestRunOnceRepoPurgeDisabledScansAndPersistsUnsupportedIntervention(t *testing.T) {
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
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported purge recovery count error")
	}
	if summary := result.Summary().Operation; summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || strings.Join(store.acquireIDs, ",") != purgeRecord.ID {
		t.Fatalf("summary/acquire = %#v/%#v, want disabled purge audited unsupported intervention", summary, store.acquireIDs)
	}
	assertWorkerAppUnsupportedAudit(t, store, purgeRecord, audit.EventTypeRepoPurge)
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

func TestRunOnceSavePointCreateDisabledScansAndPersistsUnsupportedIntervention(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	if summary := result.Summary().Operation; summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || store.savePointListCalls != 1 || strings.Join(store.acquireIDs, ",") != record.ID {
		t.Fatalf("summary/list/acquire = %#v/%d/%#v, want disabled save_point_create audited unsupported intervention", summary, store.savePointListCalls, store.acquireIDs)
	}
	if store.genericUpdateCalls != 0 {
		t.Fatalf("generic update calls = %d, want unsupported intervention committed through audit boundary", store.genericUpdateCalls)
	}
	assertWorkerAppUnsupportedAudit(t, store, record, audit.EventTypeSavePointCreate)
}

func TestRunOnceSavePointCreateEnabledClaimsThroughSavePointExecutor(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{
			SavePointID:   "sp_after",
			HistoryHeadID: "sp_after",
			CreatedAt:     "2026-05-05T12:00:00Z",
		},
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
	if store.savePointCapabilityOwner != "worker-a" || !store.savePointCapabilityObservedAt.Equal(now) || !store.savePointCapabilityExpiresAt.After(now) {
		t.Fatalf("save point capability heartbeat = owner %q observed %s expires %s, want live worker-a heartbeat", store.savePointCapabilityOwner, store.savePointCapabilityObservedAt, store.savePointCapabilityExpiresAt)
	}
	if strings.Join(jvs.calls, ",") != "direct_save,direct_list" {
		t.Fatalf("jvs calls = %#v, want direct_save,direct_list", jvs.calls)
	}
	if store.operation.ID != record.ID || store.operation.Type != operations.OperationSavePointCreate || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseSavePointCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded save_point_create_committed", store.operation)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeSavePointCreate || store.auditEvents[0].Outcome != audit.OutcomeSucceeded || store.auditEvents[0].Reason != "save_point_create_committed" {
		t.Fatalf("audit events = %#v, want succeeded save_point_create_committed event", store.auditEvents)
	}
}

func TestRunOnceSavePointCreateWriterDrainPendingStaysRunning(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	store.mounts = []sessionstate.WorkloadMountBinding{{
		ID:             "wmb_writer",
		NamespaceID:    record.NamespaceID,
		RepoID:         record.RepoID,
		Status:         sessionstate.MountStatusReleasing,
		ReadOnly:       false,
		LeaseExpiresAt: now.Add(time.Hour),
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now,
	}}
	jvs := &workerAppFakeJVSRunner{}
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
	if summary.Claimed != 1 || summary.Reclaimed != 0 || summary.Manual != 0 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want claimed pending writer drain", summary)
	}
	if len(jvs.calls) != 0 {
		t.Fatalf("jvs calls = %#v, want writer drain gate before DirectSave", jvs.calls)
	}
	got := store.records[record.ID]
	if got.State != operations.OperationStateRunning || got.Phase != operations.OperationPhaseSavePointCreateValidate || got.Error == nil || got.Error.Code != "SAVE_POINT_WRITER_DRAIN_PENDING" || !got.Error.Retryable {
		t.Fatalf("operation = %#v, want running retryable writer drain pending", got)
	}
	if got.FinishedAt != nil || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(now) {
		t.Fatalf("operation lease/finish = %v/%v, want expired running lease and no finish", got.LeaseExpiresAt, got.FinishedAt)
	}
	if len(store.auditEvents) != 0 {
		t.Fatalf("audit events = %#v, want no terminal audit while pending", store.auditEvents)
	}
}

func TestRunOnceSavePointCreateReclaimsWriterDrainPendingAfterDrain(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	record.State = operations.OperationStateRunning
	record.Attempt = 1
	record.LeaseOwner = "worker-old"
	expired := now.Add(-time.Second)
	record.LeaseExpiresAt = &expired
	started := now.Add(-time.Minute)
	record.StartedAt = &started
	record.Error = &operations.OperationError{Code: "SAVE_POINT_WRITER_DRAIN_PENDING", Message: "save point writer drain is pending", Retryable: true, CorrelationID: record.CorrelationID, OperationID: record.ID}
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{
			SavePointID:   "sp_after",
			HistoryHeadID: "sp_after",
			CreatedAt:     "2026-05-05T12:00:00Z",
		},
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
	if summary.Reclaimed != 1 || summary.Claimed != 0 || summary.Manual != 0 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want reclaimed save_point_create", summary)
	}
	if strings.Join(jvs.calls, ",") != "direct_save,direct_list" {
		t.Fatalf("jvs calls = %#v, want direct_save,direct_list", jvs.calls)
	}
	got := store.records[record.ID]
	if got.State != operations.OperationStateSucceeded || got.Phase != operations.OperationPhaseSavePointCreateCommitted {
		t.Fatalf("operation = %#v, want succeeded save_point_create_committed", got)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Reason != "save_point_create_committed" {
		t.Fatalf("audit events = %#v, want committed save point audit", store.auditEvents)
	}
}

func TestRunOnceSavePointCreateHeartbeatFailureDoesNotRunDirectSave(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	store.savePointCapabilityErr = errors.New("heartbeat store unavailable")
	jvs := &workerAppFakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{
			SavePointID:   "sp_after",
			HistoryHeadID: "sp_after",
			CreatedAt:     "2026-05-05T12:00:00Z",
		},
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
	if err == nil {
		t.Fatal("RunOnce succeeded, want heartbeat failure")
	}
	if summary := result.Summary().Operation; summary.Scanned != 0 || summary.Claimed != 0 {
		t.Fatalf("summary = %#v, want no recovery after heartbeat failure", summary)
	}
	if len(jvs.calls) != 0 || store.savePointListCalls != 0 || len(store.acquireIDs) != 0 {
		t.Fatalf("jvs/list/acquire = %#v/%d/%#v, want direct save skipped", jvs.calls, store.savePointListCalls, store.acquireIDs)
	}
}

func TestRunOnceSavePointCreateDirectSaveInvisibleTerminalizesTypedFailure(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		directSaveSummary: jvsrunner.DirectSaveSummary{
			SavePointID:   "sp_after",
			HistoryHeadID: "sp_after",
			Message:       "checkpoint",
			CreatedAt:     "2026-05-05T12:00:00Z",
		},
		directListSummary: jvsrunner.DirectListSummary{
			HistoryHeadID: "sp_before",
			SavePoints: []jvsrunner.DirectSavePointSummary{{
				SavePointID: "sp_before",
				Message:     "before",
				CreatedAt:   "2026-05-05T11:00:00Z",
				HistoryHead: true,
			}},
		},
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
	if summary.Claimed != 1 || summary.Manual != 0 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want claimed typed terminal failure", summary)
	}
	if strings.Join(jvs.calls, ",") != "direct_save,direct_list" {
		t.Fatalf("jvs calls = %#v, want direct_save,direct_list", jvs.calls)
	}
	got := store.records[record.ID]
	if got.State != operations.OperationStateFailed || got.Error == nil || got.Error.Code != "SAVE_POINT_NOT_VISIBLE" || got.Error.Retryable {
		t.Fatalf("operation = %#v, want non-retryable failed SAVE_POINT_NOT_VISIBLE", got)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeSavePointCreate || store.auditEvents[0].Reason != "save_point_create_failed" {
		t.Fatalf("audit events = %#v, want failed save_point_create event", store.auditEvents)
	}
}

func TestRunOnceSavePointCreateDirectCommandFailureTerminalizesWithoutManual(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		directSaveErr: &jvsrunner.CommandError{Command: "afscp save", ExitCode: 1, Code: "E_PERMISSION_DENIED"},
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
	if summary.Claimed != 1 || summary.Manual != 0 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want claimed terminal failure without manual", summary)
	}
	if strings.Join(jvs.calls, ",") != "direct_save" {
		t.Fatalf("jvs calls = %#v, want direct_save", jvs.calls)
	}
	got := store.records[record.ID]
	if got.State != operations.OperationStateFailed || got.Phase != operations.OperationPhaseSavePointCreateValidate || got.Error == nil || got.Error.Code != "JVS_COMMAND_FAILED" || got.Error.Retryable {
		t.Fatalf("operation = %#v, want non-retryable failed JVS_COMMAND_FAILED", got)
	}
	if got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("operation lease = %q/%v, want released terminal lease", got.LeaseOwner, got.LeaseExpiresAt)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeSavePointCreate || store.auditEvents[0].Reason != "save_point_create_failed" {
		t.Fatalf("audit events = %#v, want failed save_point_create event", store.auditEvents)
	}
}

func TestRunOnceSavePointCreateDirectSaveRepoBusyStaysPending(t *testing.T) {
	now := workerAppNow()
	record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		directSaveErr: &jvsrunner.CommandError{Command: "afscp save", ExitCode: 1, Code: "E_REPO_BUSY"},
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
	if summary.Claimed != 1 || summary.Manual != 0 || summary.Failed != 0 || summary.Unsupported != 0 {
		t.Fatalf("summary = %#v, want claimed pending without terminal failure", summary)
	}
	if strings.Join(jvs.calls, ",") != "direct_save" {
		t.Fatalf("jvs calls = %#v, want direct_save", jvs.calls)
	}
	got := store.records[record.ID]
	if got.State != operations.OperationStateRunning || got.Phase != operations.OperationPhaseSavePointCreateValidate || got.Error == nil || got.Error.Code != "SAVE_POINT_WRITER_DRAIN_PENDING" || !got.Error.Retryable {
		t.Fatalf("operation = %#v, want running retryable writer-drain pending", got)
	}
	if got.FinishedAt != nil || got.LeaseOwner != "worker-a" || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(now) {
		t.Fatalf("operation finish/lease = %v/%q/%v, want unfinished expired worker lease", got.FinishedAt, got.LeaseOwner, got.LeaseExpiresAt)
	}
	details := got.VerificationResult.(map[string]any)
	if details["writer_drain_reason"] != "jvs_repo_busy" || details["jvs_error_code"] != "E_REPO_BUSY" {
		t.Fatalf("verification = %#v, want jvs repo-busy writer drain marker", details)
	}
	if len(store.auditEvents) != 0 {
		t.Fatalf("audit events = %#v, want no terminal save_point_create event", store.auditEvents)
	}
}

func TestRunOnceSavePointCreateAmbiguousFailuresRemainManual(t *testing.T) {
	now := workerAppNow()
	tests := []struct {
		name      string
		edit      func(*operations.OperationRecord, *workerAppFakeJVSRunner)
		wantCalls string
		wantCode  string
	}{
		{name: "plain direct save error", wantCalls: "direct_save", wantCode: "JVS_COMMAND_FAILED", edit: func(_ *operations.OperationRecord, jvs *workerAppFakeJVSRunner) {
			jvs.directSaveErr = errors.New("direct save process failed after possible write")
		}},
		{name: "prepared recovery", wantCalls: "", wantCode: "SAVE_POINT_RECOVERY_UNCERTAIN", edit: func(record *operations.OperationRecord, _ *workerAppFakeJVSRunner) {
			record.Phase = operations.OperationPhaseSavePointCreatePrepared
			record.VerificationResult = map[string]any{"pre_save_history_captured": true, "pre_save_newest_save_point_id": "sp_before"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := workerAppSavePointCreateOperationRecord("op_savepoint", now)
			jvs := &workerAppFakeJVSRunner{}
			tt.edit(&record, jvs)
			store := newWorkerAppStore(record)
			store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
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
			if err == nil {
				t.Fatal("RunOnce succeeded, want manual intervention recovery count error")
			}
			summary := result.Summary().Operation
			if summary.Manual != 1 || summary.Claimed != 0 || summary.Failed != 0 {
				t.Fatalf("summary = %#v, want one manual recovery result", summary)
			}
			if gotCalls := strings.Join(jvs.calls, ","); gotCalls != tt.wantCalls {
				t.Fatalf("jvs calls = %#v, want %q", jvs.calls, tt.wantCalls)
			}
			got := store.records[record.ID]
			if got.State != operations.OperationStateOperatorInterventionRequired || got.Error == nil || got.Error.Code != tt.wantCode {
				t.Fatalf("operation = %#v, want operator intervention %s", got, tt.wantCode)
			}
			if len(store.auditEvents) != 1 || store.auditEvents[0].Reason != "save_point_create_operator_intervention_required" {
				t.Fatalf("audit events = %#v, want operator intervention audit", store.auditEvents)
			}
		})
	}
}

func TestRunOnceTemplateCreateDisabledScansAndPersistsUnsupportedIntervention(t *testing.T) {
	now := workerAppNow()
	record := workerAppTemplateCreateOperationRecord("op_template_create", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	if summary := result.Summary().Operation; summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || store.templateCreateListCalls != 1 || strings.Join(store.acquireIDs, ",") != record.ID {
		t.Fatalf("summary/list/acquire = %#v/%d/%#v, want disabled template_create audited unsupported intervention", summary, store.templateCreateListCalls, store.acquireIDs)
	}
	if store.genericUpdateCalls != 0 {
		t.Fatalf("generic update calls = %d, want unsupported intervention committed through audit boundary", store.genericUpdateCalls)
	}
	assertWorkerAppUnsupportedAudit(t, store, record, audit.EventTypeTemplateCreate)
}

func TestRunOnceTemplateCloneDisabledScansAndPersistsUnsupportedIntervention(t *testing.T) {
	now := workerAppNow()
	record := workerAppTemplateCloneOperationRecord("op_template_clone", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	if summary := result.Summary().Operation; summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || store.templateCloneListCalls != 1 || strings.Join(store.acquireIDs, ",") != record.ID {
		t.Fatalf("summary/list/acquire = %#v/%d/%#v, want disabled template_clone audited unsupported intervention", summary, store.templateCloneListCalls, store.acquireIDs)
	}
	if store.genericUpdateCalls != 0 {
		t.Fatalf("generic update calls = %d, want unsupported intervention committed through audit boundary", store.genericUpdateCalls)
	}
	assertWorkerAppUnsupportedAudit(t, store, record, audit.EventTypeTemplateClone)
}

func TestRunOnceRestoreDisabledScansAndPersistsUnsupportedIntervention(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestoreOperationRecord("op_restore", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	runner := newWorkerAppRunner(t, store, workerAppConfigSource(nil), now, nil)

	result, err := runner.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want unsupported operation recovery count error")
	}
	if summary := result.Summary().Operation; summary.Scanned != 1 || summary.Unsupported != 1 || summary.Claimed != 0 || store.restoreListCalls != 1 || strings.Join(store.acquireIDs, ",") != record.ID {
		t.Fatalf("summary/list/acquire = %#v/%d/%#v, want disabled restore audited unsupported intervention", summary, store.restoreListCalls, store.acquireIDs)
	}
	assertWorkerAppUnsupportedAudit(t, store, record, audit.EventTypeRestore)
}

func TestRunOnceRestoreEnabledClaimsThroughDirectRestoreExecutor(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestoreOperationRecord("op_restore", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		directRestoreSummary: jvsrunner.DirectRestoreSummary{
			RestoredSavePointID: "sp_001",
			PreviousHeadID:      "sp_before",
			NewHeadID:           "sp_001",
		},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRestoreConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return jvs, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_restore" },
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
		t.Fatalf("summary = %#v, want restore claimed", summary)
	}
	if store.restoreListCalls != 1 || strings.Join(store.acquireIDs, ",") != "op_restore" {
		t.Fatalf("list/acquire = %d/%#v, want restore listed and acquired", store.restoreListCalls, store.acquireIDs)
	}
	if strings.Join(jvs.calls, ",") != "direct_restore,direct_status" {
		t.Fatalf("jvs calls = %#v, want direct_restore,direct_status", jvs.calls)
	}
	if store.operation.Type != operations.OperationRestore || store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestoreCommitted {
		t.Fatalf("operation = %#v, want succeeded direct restore", store.operation)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestore || store.auditEvents[0].Outcome != audit.OutcomeSucceeded {
		t.Fatalf("audit events = %#v, want direct restore success", store.auditEvents)
	}
}

func TestRunOnceRestoreEnabledAllowsNonBlockingCleanupStatus(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestoreOperationRecord("op_restore", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	jvs := &workerAppFakeJVSRunner{
		directRestoreSummary: jvsrunner.DirectRestoreSummary{RestoredSavePointID: "sp_001", PreviousHeadID: "sp_before", NewHeadID: "sp_001"},
		directStatusSummary:  jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "none", Recovery: "cleanup_pending"},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRestoreConfigSource(nil),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return jvs, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_restore" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if summary := result.Summary().Operation; summary.Claimed != 1 || summary.Manual != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want restore success despite non-blocking cleanup evidence", summary)
	}
	if strings.Join(jvs.calls, ",") != "direct_restore,direct_status" {
		t.Fatalf("jvs calls = %#v, want direct_restore,direct_status", jvs.calls)
	}
	if store.operation.State != operations.OperationStateSucceeded || store.operation.Phase != operations.OperationPhaseRestoreCommitted {
		t.Fatalf("operation = %#v, want succeeded direct restore", store.operation)
	}
}

func TestRunOnceRestoreEnabledStatusRecoveryRetainsGate(t *testing.T) {
	now := workerAppNow()
	tests := []struct {
		name   string
		status jvsrunner.DirectStatusSummary
	}{
		{name: "journal recovery", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "none", Recovery: "journal_recovery_required"}},
		{name: "operator intervention", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "none", Recovery: "operator_intervention_required"}},
		{name: "blocking cleanup", status: jvsrunner.DirectStatusSummary{HistoryHeadID: "sp_001", MetadataState: "ready", ActiveOperation: "cleanup_blocking", Recovery: "none"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := workerAppRestoreOperationRecord("op_restore", now)
			store := newWorkerAppStore(record)
			store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
			jvs := &workerAppFakeJVSRunner{
				directRestoreSummary: jvsrunner.DirectRestoreSummary{RestoredSavePointID: "sp_001", PreviousHeadID: "sp_before", NewHeadID: "sp_001"},
				directStatusSummary:  tt.status,
			}
			runner, err := NewRunOnceRunner(Options{
				Source: workerAppRestoreConfigSource(nil),
				StoreFactory: func(context.Context, string) (StoreHandle, error) {
					return StoreHandle{Store: store}, nil
				},
				JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
					return jvs, nil
				},
				Clock:        func() time.Time { return now },
				AuditEventID: func() string { return "evt_restore" },
			})
			if err != nil {
				t.Fatalf("NewRunOnceRunner: %v", err)
			}

			result, err := runner.RunOnce(context.Background())
			if err == nil {
				t.Fatalf("RunOnce succeeded result=%#v, want manual intervention count error", result)
			}
			if summary := result.Summary().Operation; summary.Manual != 1 || summary.Failed != 0 {
				t.Fatalf("summary = %#v, want manual recovery required", summary)
			}
			if strings.Join(jvs.calls, ",") != "direct_restore,direct_status" {
				t.Fatalf("jvs calls = %#v, want direct_restore,direct_status", jvs.calls)
			}
			if store.operation.State != operations.OperationStateOperatorInterventionRequired || store.operation.Error == nil || store.operation.Error.Code != "JVS_RESTORE_RECOVERY_REQUIRED" {
				t.Fatalf("operation = %#v, want JVS_RESTORE_RECOVERY_REQUIRED operator intervention", store.operation)
			}
			if store.releasedFenceID != "" || activeWorkerAppWriterFenceCount(store.fences, "op_restore") != 1 {
				t.Fatalf("released/active writer fence = %q/%#v, want retained writer fence", store.releasedFenceID, store.fences)
			}
			rendered := fmt.Sprint(store.operation.JVSJSONOutput, store.operation.VerificationResult, store.operation.Error, store.auditEvents)
			for _, leaked := range []string{jvs.controlRoot, jvs.payloadRoot, "/srv/afscp", "control_root", "payload_root"} {
				if leaked != "" && strings.Contains(rendered, leaked) {
					t.Fatalf("restore status projection leaked internal path/detail %q: %s", leaked, rendered)
				}
			}
		})
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

func TestNewRunOnceRunnerAppliesConfiguredTimeoutOnlyToStoreOpen(t *testing.T) {
	now := workerAppNow()
	store := newWorkerAppStore(workerAppOperationRecord(now))
	var openDeadline time.Time
	_, err := NewRunOnceRunner(Options{
		Source: workerAppConfigSource(config.MapSource{"AFSCP_WORKER_RUN_ONCE_TIMEOUT": "123ms"}),
		StoreFactory: func(ctx context.Context, _ string) (StoreHandle, error) {
			if deadline, ok := ctx.Deadline(); ok {
				openDeadline = deadline
			}
			return StoreHandle{Store: store}, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_namespace" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}
	if openDeadline.IsZero() {
		t.Fatal("store factory saw no context deadline")
	}
	remaining := time.Until(openDeadline)
	if remaining <= 0 || remaining > time.Second {
		t.Fatalf("open deadline remaining = %v, want bounded startup timeout", remaining)
	}
}

func TestRunOnceAppliesConfiguredTimeoutToLeasedRestoreExecution(t *testing.T) {
	now := workerAppNow()
	record := workerAppRestoreOperationRecord("op_restore", now)
	store := newWorkerAppStore(record)
	store.repo = workerAppRepoLifecycleResource(now, resources.RepoStatusActive)
	restoreStarted := make(chan struct{})
	jvs := &workerAppFakeJVSRunner{
		beforeDirectRestore: func(ctx context.Context) error {
			close(restoreStarted)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	runner, err := NewRunOnceRunner(Options{
		Source: workerAppRestoreConfigSource(config.MapSource{
			"AFSCP_WORKER_RUN_ONCE_TIMEOUT": "100ms",
		}),
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store}, nil
		},
		JVSRunnerFactory: func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
			return jvs, nil
		},
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_restore" },
	})
	if err != nil {
		t.Fatalf("NewRunOnceRunner: %v", err)
	}

	result, err := runner.RunOnce(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunOnce error = %v, want context deadline exceeded", err)
	}
	select {
	case <-restoreStarted:
	default:
		t.Fatal("direct restore did not start")
	}
	if summary := result.Summary().Operation; summary.Scanned != 1 || summary.Claimed != 1 || summary.Manual != 0 || summary.Failed != 0 {
		t.Fatalf("summary = %#v, want restore claimed and terminalized by timeout failure", summary)
	}
	if strings.Join(jvs.calls, ",") != "direct_restore" {
		t.Fatalf("jvs calls = %#v, want direct_restore only", jvs.calls)
	}
	if store.operation.Type != operations.OperationRestore || store.operation.State != operations.OperationStateFailed || store.operation.Error == nil || store.operation.Error.Code != "JVS_RESTORE_FAILED" {
		t.Fatalf("operation = %#v, want failed restore operation after run-once timeout", store.operation)
	}
	if store.operation.Phase != operations.OperationPhaseRestoreWriterFenced || store.operation.LeaseOwner != "" || store.operation.LeaseExpiresAt != nil {
		t.Fatalf("operation lease/phase = %#v, want committed terminal restore writer-fenced operation", store.operation)
	}
	if store.releasedFenceID != "fence_op_restore" || activeWorkerAppWriterFenceCount(store.fences, "op_restore") != 0 {
		t.Fatalf("released/active writer fence = %q/%#v, want released restore writer fence", store.releasedFenceID, store.fences)
	}
	if len(store.auditEvents) != 1 || store.auditEvents[0].Type != audit.EventTypeRestore || store.auditEvents[0].Outcome != audit.OutcomeFailed {
		t.Fatalf("audit events = %#v, want failed restore audit", store.auditEvents)
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
	readFailurePath := filepath.Join(t.TempDir(), "afscp-secret-jvs-dir")
	if err := os.Mkdir(readFailurePath, 0o755); err != nil {
		t.Fatalf("mkdir read failure fixture: %v", err)
	}
	tests := []struct {
		name    string
		rawPath string
	}{
		{name: "missing", rawPath: "/tmp/afscp-secret-missing-jvs-binary"},
		{name: "read failure", rawPath: readFailurePath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewJVSRunnerFromConfig(config.WorkerRepoCreateRecoveryConfig{
				Enabled:         true,
				JVSBinaryPath:   tt.rawPath,
				JVSBinarySHA256: acceptedJVSBinarySHA256,
				JVSCWD:          "/var/lib/afscp/jvs-cwd",
				VolumeRoots:     map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
			})
			if err == nil {
				t.Fatal("NewJVSRunnerFromConfig succeeded, want read/checksum error")
			}
			if !errors.Is(err, ErrJVSRuntimeUnavailable) {
				t.Fatalf("NewJVSRunnerFromConfig error = %v, want ErrJVSRuntimeUnavailable", err)
			}
			if strings.Contains(err.Error(), tt.rawPath) || strings.Contains(strings.ToLower(err.Error()), "secret") {
				t.Fatalf("error leaked raw binary path: %v", err)
			}
		})
	}
}

func TestNewJVSRunnerFromConfigVerifiesFileAgainstAcceptedPin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jvs")
	content := []byte("not the accepted jvs release binary")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("write fake jvs binary: %v", err)
	}
	sum := sha256.Sum256(content)

	_, err := NewJVSRunnerFromConfig(config.WorkerRepoCreateRecoveryConfig{
		Enabled:         true,
		JVSBinaryPath:   path,
		JVSBinarySHA256: hex.EncodeToString(sum[:]),
		JVSCWD:          "/var/lib/afscp/jvs-cwd",
		VolumeRoots:     map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	if err == nil {
		t.Fatal("NewJVSRunnerFromConfig succeeded with non-pinned binary hash, want checksum error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %q, want checksum mismatch", err)
	}
}

func TestNewJVSRunnerFromConfigDirectRestorePreflightsCLIHelp(t *testing.T) {
	path, sha := writeWorkerAppJVSHelpFixture(t, "Usage:\n  jvs afscp --control-root <control> --home <home> save --message <message> --purpose <purpose> --json\n  jvs afscp --control-root <control> --home <home> list --json\n  jvs afscp --control-root <control> --home <home> restore --save-point <id> --json\n  jvs afscp --control-root <control> --home <home> clone --target-control-root <target-control> --target-home <target-home> --json\n  jvs afscp --control-root <control> --home <home> status --json\n  jvs afscp --control-root <control> --home <home> doctor --json\n")

	_, err := NewJVSRunnerFromConfig(config.WorkerRepoCreateRecoveryConfig{
		Enabled:                   true,
		JVSBinaryPath:             path,
		JVSBinarySHA256:           sha,
		JVSCWD:                    filepath.Dir(path),
		JVSDirectRestoreRequired:  true,
		JVSDirectRestoreSourceRef: directRestoreReleaseJVSSourceRef,
		VolumeRoots:               map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	if err != nil {
		t.Fatalf("NewJVSRunnerFromConfig returned error for published direct-capable JVS artifact: %v", err)
	}
}

func TestNewJVSRunnerFromConfigDirectRestoreRejectsMissingCLIFlag(t *testing.T) {
	path, sha := writeWorkerAppJVSHelpFixture(t, "Usage:\n  jvs afscp --control-root <control> list --json\n  jvs afscp --control-root <control> status --json\n")

	_, err := NewJVSRunnerFromConfig(config.WorkerRepoCreateRecoveryConfig{
		Enabled:                   true,
		JVSBinaryPath:             path,
		JVSBinarySHA256:           sha,
		JVSCWD:                    filepath.Dir(path),
		JVSDirectRestoreRequired:  true,
		JVSDirectRestoreSourceRef: directRestoreReleaseJVSSourceRef,
		VolumeRoots:               map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	if err == nil {
		t.Fatal("NewJVSRunnerFromConfig succeeded without afscp direct help, want fail-closed preflight error")
	}
	if !errors.Is(err, ErrJVSRuntimeUnavailable) || !strings.Contains(err.Error(), "afscp direct preflight") {
		t.Fatalf("error = %v, want runtime-unavailable afscp direct preflight failure", err)
	}
}

func writeWorkerAppJVSHelpFixture(t *testing.T, help string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "jvs")
	content := []byte("#!/bin/sh\nif [ \"$1\" = \"afscp\" ]; then\n  case \"$2:$3\" in\n    \"--help:\"|\"save:--help\"|\"list:--help\"|\"restore:--help\"|\"clone:--help\"|\"status:--help\"|\"doctor:--help\")\n      cat <<'EOF'\n" + help + "EOF\n      exit 0\n      ;;\n  esac\nfi\nexit 7\n")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("write fake jvs binary: %v", err)
	}
	sum := sha256.Sum256(content)
	return path, hex.EncodeToString(sum[:])
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

func assertWorkerAppUnsupportedAudit(t *testing.T, store *fakeWorkerAppStore, record operations.OperationRecord, eventType audit.EventType) {
	t.Helper()
	got := store.records[record.ID]
	if got.State != operations.OperationStateOperatorInterventionRequired || got.Error == nil || got.Error.Code != "OPERATION_RECOVERY_REQUIRED" || got.Error.Retryable {
		t.Fatalf("operation = %#v, want OPERATION_RECOVERY_REQUIRED operator intervention", got)
	}
	if got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("operation lease = %q/%v, want cleared by commit", got.LeaseOwner, got.LeaseExpiresAt)
	}
	if got.Error.Details["reason"] == nil || got.Error.Details["evidence"] == nil {
		t.Fatalf("operation error details = %#v, want reason/evidence", got.Error.Details)
	}
	if len(store.auditEvents) != 1 {
		t.Fatalf("audit events = %#v, want one unsupported recovery event", store.auditEvents)
	}
	event := store.auditEvents[0]
	if event.Type != eventType || event.Outcome != audit.OutcomeFailed || event.Reason != "unsupported_operation_recovery" || event.OperationID != record.ID {
		t.Fatalf("audit event = %#v, want failed unsupported %s event", event, eventType)
	}
	if event.Details["reason"] != got.Error.Details["reason"] || event.Details["evidence"] == nil {
		t.Fatalf("audit details = %#v, want reason/evidence matching operation", event.Details)
	}
}

func activeWorkerAppWriterFenceCount(fencesList []fences.Fence, operationID string) int {
	count := 0
	for _, fence := range fencesList {
		if fence.Kind == fences.KindWriterSession && fence.HolderOperationID == operationID && fence.Status == fences.StatusActive && fence.ReleasedAt == nil && fence.RecoveredAt == nil {
			count++
		}
	}
	return count
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

func workerAppExportReconcileConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_EXPORT_SESSION_RECONCILE_ENABLED": "true",
		"AFSCP_EXPORT_SESSION_RECONCILE_OWNER":   "export-worker",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRestoreReconciliationConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_RESTORE_RECONCILIATION_ENABLED": "true",
		"AFSCP_RESTORE_RECONCILIATION_OWNER":   "restore-worker",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRestoreReconciliationCleanTargetAndObservation() ([]restorereconcile.Target, []restorereconcile.Observation) {
	target := restorereconcile.Target{
		RunID:                     "rrun_123",
		RepoID:                    "repo_123",
		NamespaceID:               "ns_123",
		ExpectedRepoStatus:        restorereconcile.RepoStatusActive,
		ExpectedStorageGeneration: "gen-1",
		ExpectedSnapshotID:        "snapshot-1",
		ExpectedTombstoneMarker:   "none",
		ExpectedPurgeMarker:       "none",
	}
	observation := restorereconcile.Observation{
		RunID:                   target.RunID,
		RepoID:                  target.RepoID,
		NamespaceID:             target.NamespaceID,
		ExpectedRepoStatus:      target.ExpectedRepoStatus,
		ObservedStoragePresent:  true,
		ObservedGeneration:      target.ExpectedStorageGeneration,
		ObservedSnapshotID:      target.ExpectedSnapshotID,
		ObservedTombstoneMarker: target.ExpectedTombstoneMarker,
		ObservedPurgeMarker:     target.ExpectedPurgeMarker,
		Result:                  restorereconcile.ObservationResultClean,
		EvidenceRef:             "restore-reconciliation://run/rrun_123/repo/repo_123",
	}
	return []restorereconcile.Target{target}, []restorereconcile.Observation{observation}
}

func workerAppRepoConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_REPO_CREATE_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":              "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":            acceptedJVSBinarySHA256,
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
		"AFSCP_JVS_BINARY_SHA256":               acceptedJVSBinarySHA256,
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
		"AFSCP_JVS_BINARY_SHA256":           acceptedJVSBinarySHA256,
		"AFSCP_JVS_CWD":                     "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                "vol_123=/srv/afscp/volumes/vol_123",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppTemplateCreateConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_TEMPLATE_CREATE_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":                  "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                acceptedJVSBinarySHA256,
		"AFSCP_JVS_CWD":                          "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                     "vol_123=/srv/afscp/volumes/vol_123",
	})
	for key, value := range overrides {
		source[key] = value
	}
	return source
}

func workerAppRestoreConfigSource(overrides config.MapSource) config.MapSource {
	source := workerAppConfigSource(config.MapSource{
		"AFSCP_RESTORE_RECOVERY_ENABLED":         "true",
		"AFSCP_JVS_BINARY_PATH":                  directRestoreReleaseJVSBinaryPath,
		"AFSCP_JVS_BINARY_SHA256":                directRestoreReleaseJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256": directRestoreReleaseJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF":    directRestoreReleaseJVSSourceRef,
		"AFSCP_JVS_CWD":                          "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                     "vol_123=/srv/afscp/volumes/vol_123",
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
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationNamespaceUpsert, "idem_namespace").String(),
		IdempotencyKey:   "idem_namespace",
		RequestHash:      operations.RequestHash("sha256:namespace"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "namespace", ID: "ns_alpha01"},
		NamespaceID:      "ns_alpha01",
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppNamespaceDisableOperationRecord(now time.Time) operations.OperationRecord {
	record := workerAppOperationRecord(now)
	record.ID = "op_namespace_disable"
	record.Type = operations.OperationNamespaceDisable
	record.Phase = operations.OperationPhaseNamespaceDisableValidate
	record.IdempotencyScope = operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationNamespaceDisable, "idem_namespace_disable").String()
	record.IdempotencyKey = "idem_namespace_disable"
	record.InputSummary = map[string]any{"namespace_id": "ns_alpha01", "reason": "security hold"}
	return record
}

func workerAppRepoOperationRecord(operationID string, state operations.OperationState, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationRepoCreate,
		State:            state,
		Phase:            "allocate_repo_path",
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRepoCreate, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:repo"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
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
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", typ, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:lifecycle"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
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
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationSavePointCreate, "idem_savepoint").String(),
		IdempotencyKey:   "idem_savepoint",
		RequestHash:      operations.RequestHash("sha256:savepoint"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		InputSummary:     map[string]any{"message": "checkpoint"},
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppTemplateCreateOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationTemplateCreate,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseTemplateCreateValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationTemplateCreate, "idem_template").String(),
		IdempotencyKey:   "idem_template",
		RequestHash:      operations.RequestHash("sha256:template-create"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo_template", ID: "tmpl_base01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		TemplateID:       "tmpl_base01",
		InputSummary:     map[string]any{"source_repo_id": "repo_alpha01", "target_template_id": "tmpl_base01", "clone_history_mode": "main"},
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppTemplateCloneOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationTemplateClone,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseTemplateCloneValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationTemplateClone, "idem_template_clone").String(),
		IdempotencyKey:   "idem_template_clone",
		RequestHash:      operations.RequestHash("sha256:template-clone"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_clone01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_clone01",
		TemplateID:       "tmpl_base01",
		InputSummary:     map[string]any{"template_id": "tmpl_base01", "target_repo_id": "repo_clone01", "clone_history_mode": "main"},
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppRestoreOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationRestore,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseRestoreValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationRestore, "idem_restore").String(),
		IdempotencyKey:   "idem_restore",
		RequestHash:      operations.RequestHash("sha256:restore"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		InputSummary:     map[string]any{"save_point_id": "sp_001"},
		CreatedAt:        now.Add(-time.Hour),
	}
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

func workerAppFreshExportSession(now time.Time, exportID string, mode sessionstate.AccessMode, status sessionstate.ExportStatus, expiresAt time.Time) sessionstate.ExportSession {
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

func workerAppExportAccessSession(now time.Time, exportID string, status sessionstate.ExportStatus, expiresAt time.Time) exportaccess.Session {
	return exportaccess.Session{
		ID:                     exportID,
		NamespaceID:            "ns_alpha01",
		RepoID:                 "repo_alpha01",
		Protocol:               exportaccess.ProtocolWebDAV,
		Mode:                   sessionstate.AccessModeReadWrite,
		Status:                 status,
		ExpiresAt:              expiresAt,
		CreatedByCallerService: "product-caller",
		CreatedByActor:         exportaccess.Actor{Type: "user", ID: "user_alpha"},
		CreatedAt:              now.Add(-time.Hour),
		UpdatedAt:              now.Add(-time.Minute),
	}
}

func workerAppVolumeOperationRecord(operationID string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operations.OperationVolumeEnsure,
		State:            operations.OperationStateQueued,
		Phase:            operations.OperationPhaseVolumeEnsureValidate,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "", operations.OperationVolumeEnsure, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:volume"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
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
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operations.OperationNamespaceVolumeBindingPut, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:binding"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "namespace_volume_binding", ID: "ns_alpha01"},
		NamespaceID:      "ns_alpha01",
		InputSummary:     workerAppBindingInputSummary("ns_alpha01"),
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppWorkloadMountBindingOperationRecord(operationID string, operationType operations.OperationType, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:               operationID,
		Type:             operationType,
		State:            operations.OperationStateQueued,
		Phase:            workerAppWorkloadMountBindingValidatePhase(operationType),
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha01", operationType, operationID).String(),
		IdempotencyKey:   operationID,
		RequestHash:      operations.RequestHash("sha256:workload-mount"),
		CorrelationID:    "corr-alpha",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:         operations.ResourceRef{Type: "workload_mount_binding", ID: "wmb_alpha01"},
		NamespaceID:      "ns_alpha01",
		RepoID:           "repo_alpha01",
		MountBindingID:   "wmb_alpha01",
		InputSummary:     workerAppWorkloadMountBindingInputSummary(operationType, now),
		CreatedAt:        now.Add(-time.Hour),
	}
}

func workerAppWorkloadMountBindingInputSummary(operationType operations.OperationType, now time.Time) map[string]any {
	switch operationType {
	case operations.OperationMountBindingCreate:
		return map[string]any{
			"mount_binding_id": "wmb_alpha01",
			"namespace_id":     "ns_alpha01",
			"repo_id":          "repo_alpha01",
			"volume_id":        "vol_123",
			"mount_path":       "/mnt/repo",
			"read_only":        true,
			"lease_seconds":    float64(120),
		}
	case operations.OperationMountBindingStatusUpdate:
		return map[string]any{
			"status":           string(sessionstate.MountStatusActive),
			"reason":           "mounted",
			"observed_at":      now.Format(time.RFC3339),
			"lease_expires_at": now.Add(2 * time.Minute).Format(time.RFC3339),
		}
	default:
		return map[string]any{"mount_binding_id": "wmb_alpha01"}
	}
}

func workerAppWorkloadMountBindingValidatePhase(operationType operations.OperationType) string {
	switch operationType {
	case operations.OperationMountBindingCreate:
		return operations.OperationPhaseMountBindingCreateValidate
	case operations.OperationMountBindingStatusUpdate:
		return operations.OperationPhaseMountBindingStatusValidate
	case operations.OperationMountBindingHeartbeat:
		return operations.OperationPhaseMountBindingHeartbeatValidate
	case operations.OperationMountBindingRelease:
		return operations.OperationPhaseMountBindingReleaseValidate
	case operations.OperationMountBindingRevoke:
		return operations.OperationPhaseMountBindingRevokeValidate
	default:
		return ""
	}
}

func workerAppWorkloadMountBindingCommittedPhase(operationType operations.OperationType) string {
	switch operationType {
	case operations.OperationMountBindingCreate:
		return operations.OperationPhaseMountBindingCreateCommitted
	case operations.OperationMountBindingStatusUpdate:
		return operations.OperationPhaseMountBindingStatusCommitted
	case operations.OperationMountBindingHeartbeat:
		return operations.OperationPhaseMountBindingHeartbeatCommitted
	case operations.OperationMountBindingRelease:
		return operations.OperationPhaseMountBindingReleaseCommitted
	case operations.OperationMountBindingRevoke:
		return operations.OperationPhaseMountBindingRevokeCommitted
	default:
		return ""
	}
}

func workerAppBindingInputSummary(namespaceID string) map[string]any {
	return map[string]any{
		"namespace_id":        namespaceID,
		"default_volume_id":   "vol_123",
		"allowed_callers":     []any{map[string]any{"caller_service": "product-caller", "roles": []any{"repo_admin", "operation_inspector"}}},
		"quota_bytes_default": float64(4096),
		"export_policy":       map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		"lifecycle_policy":    map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		"mount_policy":        map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
		"template_policy":     map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		"status":              "active",
	}
}

func workerAppNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

type fakeWorkerAppStore struct {
	records                              map[string]operations.OperationRecord
	order                                []string
	volume                               resources.Volume
	repo                                 resources.Repo
	namespace                            resources.Namespace
	binding                              resources.NamespaceVolumeBinding
	exports                              []sessionstate.ExportSession
	mounts                               []sessionstate.WorkloadMountBinding
	operation                            operations.OperationRecord
	auditEvents                          []audit.Event
	fences                               []fences.Fence
	releasedFenceID                      string
	listDeadline                         time.Time
	closeCalls                           int
	acquireIDs                           []string
	acquirePolicies                      map[string]operations.LeaseCancelPolicy
	renewCalls                           []workerAppRenewCall
	renewCh                              chan workerAppRenewCall
	renewErr                             error
	recoveredAudit                       []audit.OutboxRecord
	claimedAudit                         []audit.OutboxRecord
	auditCallOrder                       []string
	auditRecoverOwner                    string
	auditRecoverLimit                    int
	auditClaimOwner                      string
	auditClaimLimit                      int
	savePointListCalls                   int
	templateCreateListCalls              int
	templateCloneListCalls               int
	restoreListCalls                     int
	workloadMountBindingListCalls        int
	auditDelivered                       []string
	auditFailed                          []workerAppAuditFailedCall
	auditDeliverErr                      error
	workerCallOrder                      []string
	exportReconcileCandidates            []exportaccess.Session
	exportReconciles                     []exportaccess.ReconcileRequest
	staleWorkloadMountBindings           []workloadmount.Binding
	workloadMountStaleNow                time.Time
	workloadMountStaleLimit              int
	restoreReconciliationRun             restorereconcile.Run
	restoreReconciliationTargets         []restorereconcile.Target
	restoreReconciliationObservations    []restorereconcile.Observation
	restoreReconciliationActiveRunCalls  int
	restoreReconciliationCompletedRunID  string
	restoreReconciliationMismatchCommits []restorereconcile.MismatchCommit
	genericUpdateCalls                   int
	volumeReadErr                        error
	savePointCapabilityOwner             string
	savePointCapabilityObservedAt        time.Time
	savePointCapabilityExpiresAt         time.Time
	savePointCapabilityErr               error
}

type workerAppAuditFailedCall struct {
	eventID string
	failure audit.DeliveryFailure
}

type workerAppRenewCall struct {
	operationID string
	request     operations.LeaseRequest
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

func (store *fakeWorkerAppStore) ListNamespaceDisableOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if deadline, ok := ctx.Deadline(); ok {
		store.listDeadline = deadline
	}
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationNamespaceDisable || record.Phase != operations.OperationPhaseNamespaceDisableValidate {
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
	store.workerCallOrder = append(store.workerCallOrder, "operation_recovery")
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

func (store *fakeWorkerAppStore) ListExportSessionsForTerminalReconcile(context.Context, time.Time, int) ([]exportaccess.Session, error) {
	store.workerCallOrder = append(store.workerCallOrder, "export_reconcile")
	out := make([]exportaccess.Session, len(store.exportReconcileCandidates))
	copy(out, store.exportReconcileCandidates)
	return out, nil
}

func (store *fakeWorkerAppStore) RecoverStaleExportRuntimeRequests(context.Context, exportaccess.StaleRuntimeRequestRecovery) (exportaccess.StaleRuntimeRequestRecoveryResult, error) {
	return exportaccess.StaleRuntimeRequestRecoveryResult{}, nil
}

func (store *fakeWorkerAppStore) ReconcileExportSessionTerminal(_ context.Context, request exportaccess.ReconcileRequest) (exportaccess.ReconcileResult, error) {
	store.exportReconciles = append(store.exportReconciles, request)
	session := exportaccess.Session{
		ID:                 request.ExportID,
		NamespaceID:        request.NamespaceID,
		Status:             request.TargetStatus,
		TerminalObservedAt: &request.ObservedAt,
		UpdatedAt:          request.ObservedAt,
	}
	for _, candidate := range store.exportReconcileCandidates {
		if candidate.ID == request.ExportID {
			session = candidate
			session.Status = request.TargetStatus
			session.TerminalObservedAt = &request.ObservedAt
			session.ActiveRequestCount = 0
			session.ActiveWriteCount = 0
			session.UpdatedAt = request.ObservedAt
			break
		}
	}
	return exportaccess.ReconcileResult{Session: session, Operation: request.Operation}, nil
}

func (store *fakeWorkerAppStore) RestoreReconciliationWriteBlocked(context.Context, string, string) (bool, error) {
	return false, nil
}

func (store *fakeWorkerAppStore) ActiveRun(context.Context) (restorereconcile.Run, error) {
	store.workerCallOrder = append(store.workerCallOrder, "restore_reconciliation")
	store.restoreReconciliationActiveRunCalls++
	if store.restoreReconciliationRun.ID == "" {
		return restorereconcile.Run{}, sql.ErrNoRows
	}
	return store.restoreReconciliationRun, nil
}

func (store *fakeWorkerAppStore) ListObservations(context.Context, string) ([]restorereconcile.Observation, error) {
	out := make([]restorereconcile.Observation, len(store.restoreReconciliationObservations))
	copy(out, store.restoreReconciliationObservations)
	return out, nil
}

func (store *fakeWorkerAppStore) ListTargets(context.Context, string) ([]restorereconcile.Target, error) {
	out := make([]restorereconcile.Target, len(store.restoreReconciliationTargets))
	copy(out, store.restoreReconciliationTargets)
	return out, nil
}

func (store *fakeWorkerAppStore) ObserveTarget(_ context.Context, target restorereconcile.Target) (restorereconcile.Observation, error) {
	for _, observation := range store.restoreReconciliationObservations {
		if observation.RepoID == target.RepoID {
			return observation, nil
		}
	}
	return restorereconcile.Observation{}, restorereconcile.ErrObservationMissing
}

func (store *fakeWorkerAppStore) CompleteRun(_ context.Context, runID string, _ time.Time) error {
	store.restoreReconciliationCompletedRunID = runID
	return nil
}

func (store *fakeWorkerAppStore) CompleteRestoreReconciliationRun(ctx context.Context, runID string, now time.Time) error {
	return store.CompleteRun(ctx, runID, now)
}

func (store *fakeWorkerAppStore) CommitMismatch(_ context.Context, request restorereconcile.MismatchCommit) error {
	store.restoreReconciliationMismatchCommits = append(store.restoreReconciliationMismatchCommits, request)
	return nil
}

func (store *fakeWorkerAppStore) CommitRestoreReconciliationMismatch(ctx context.Context, request restorereconcile.MismatchCommit) error {
	return store.CommitMismatch(ctx, request)
}

func (store *fakeWorkerAppStore) ListStaleNonTerminalWorkloadMountBindings(_ context.Context, now time.Time, limit int) ([]workloadmount.Binding, error) {
	store.workerCallOrder = append(store.workerCallOrder, "workload_mount_stale")
	store.workloadMountStaleNow = now
	store.workloadMountStaleLimit = limit
	out := make([]workloadmount.Binding, len(store.staleWorkloadMountBindings))
	copy(out, store.staleWorkloadMountBindings)
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

func (store *fakeWorkerAppStore) ListWorkloadMountBindingOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	store.workloadMountBindingListCalls++
	if deadline, ok := ctx.Deadline(); ok {
		store.listDeadline = deadline
	}
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if !workerAppMountOperation(record.Type) {
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

func (store *fakeWorkerAppStore) AcquireNamespaceDisableOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationNamespaceDisable || record.Phase != operations.OperationPhaseNamespaceDisableValidate {
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

func (store *fakeWorkerAppStore) AcquireWorkloadMountBindingOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if !workerAppMountOperation(record.Type) {
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

func (store *fakeWorkerAppStore) ListTemplateCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	store.templateCreateListCalls++
	return store.listTemplateOperationsForRecovery(now, limit, operations.OperationTemplateCreate, operations.OperationPhaseTemplateCreateValidate)
}

func (store *fakeWorkerAppStore) ListTemplateCloneOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	store.templateCloneListCalls++
	return store.listTemplateOperationsForRecovery(now, limit, operations.OperationTemplateClone, operations.OperationPhaseTemplateCloneValidate)
}

func (store *fakeWorkerAppStore) listTemplateOperationsForRecovery(now time.Time, limit int, typ operations.OperationType, phase string) ([]operations.OperationRecord, error) {
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != typ || record.Phase != phase {
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

func (store *fakeWorkerAppStore) ListRestoreOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	store.restoreListCalls++
	var out []operations.OperationRecord
	for _, operationID := range store.order {
		record := store.records[operationID]
		if len(out) >= limit {
			break
		}
		if record.Type != operations.OperationRestore || (record.Phase != operations.OperationPhaseRestoreValidate && record.Phase != operations.OperationPhaseRestoreWriterFenced) {
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
			if record.Phase == operations.OperationPhaseRestoreValidate && namespaceRecoveryLeaseDue(record, now) {
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

func (store *fakeWorkerAppStore) AcquireTemplateCreateOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	return store.acquireTemplateOperationLease(operationID, request, operations.OperationTemplateCreate, operations.OperationPhaseTemplateCreateValidate)
}

func (store *fakeWorkerAppStore) AcquireTemplateCloneOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	return store.acquireTemplateOperationLease(operationID, request, operations.OperationTemplateClone, operations.OperationPhaseTemplateCloneValidate)
}

func (store *fakeWorkerAppStore) acquireTemplateOperationLease(operationID string, request operations.LeaseRequest, typ operations.OperationType, phase string) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != typ || record.Phase != phase {
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

func (store *fakeWorkerAppStore) AcquireRestoreOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if record.Type != operations.OperationRestore || (record.Phase != operations.OperationPhaseRestoreValidate && record.Phase != operations.OperationPhaseRestoreWriterFenced) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	if request.CancelPolicy == operations.LeaseCancelPolicyFinalize && record.Phase != operations.OperationPhaseRestoreValidate {
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

func workerAppMountOperation(typ operations.OperationType) bool {
	switch typ {
	case operations.OperationMountBindingCreate, operations.OperationMountBindingStatusUpdate, operations.OperationMountBindingHeartbeat, operations.OperationMountBindingRelease, operations.OperationMountBindingRevoke:
		return true
	default:
		return false
	}
}

func workerAppCommittedOperation(record operations.SanitizedOperationRecord) operations.OperationRecord {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	return operation
}

func (store *fakeWorkerAppStore) RenewOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	call := workerAppRenewCall{operationID: operationID, request: request}
	store.renewCalls = append(store.renewCalls, call)
	if store.renewCh != nil {
		select {
		case store.renewCh <- call:
		case <-ctx.Done():
			return operations.OperationRecord{}, ctx.Err()
		}
	}
	if store.renewErr != nil {
		return operations.OperationRecord{}, store.renewErr
	}
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	decision := operations.RenewLease(record, request)
	if !decision.Allowed {
		return operations.OperationRecord{}, decision.Error
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeWorkerAppStore) UpdateOperationWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	store.genericUpdateCalls++
	operation := record.Record()
	existing, ok := store.records[operation.ID]
	if !ok || existing.State != operations.OperationStateRunning || existing.LeaseOwner != owner || existing.LeaseExpiresAt == nil || !existing.LeaseExpiresAt.After(now) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitOperationWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	existing, ok := store.records[operation.ID]
	if !ok || existing.State != operations.OperationStateRunning || existing.LeaseOwner != owner || existing.LeaseExpiresAt == nil || !existing.LeaseExpiresAt.After(now) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
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

func (store *fakeWorkerAppStore) CommitNamespaceDisableWithLease(_ context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
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

func (store *fakeWorkerAppStore) CommitNamespaceVolumeBindingPutFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitWorkloadMountBindingCreateWithLease(_ context.Context, binding workloadmount.Binding, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	operation := workerAppCommittedOperation(record)
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return binding, operation, nil
}

func (store *fakeWorkerAppStore) CommitWorkloadMountBindingStatusWithLease(_ context.Context, _ string, _ sessionstate.MountStatus, _ string, _ time.Time, _ *time.Time, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	operation := workerAppCommittedOperation(record)
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return workloadmount.Binding{}, operation, nil
}

func (store *fakeWorkerAppStore) CommitWorkloadMountBindingHeartbeatWithLease(_ context.Context, _ string, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	operation := workerAppCommittedOperation(record)
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return workloadmount.Binding{}, operation, nil
}

func (store *fakeWorkerAppStore) CommitWorkloadMountBindingReleaseWithLease(_ context.Context, _ string, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	operation := workerAppCommittedOperation(record)
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return workloadmount.Binding{}, operation, nil
}

func (store *fakeWorkerAppStore) CommitWorkloadMountBindingRevokeWithLease(_ context.Context, _ string, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	operation := workerAppCommittedOperation(record)
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return workloadmount.Binding{}, operation, nil
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

func (store *fakeWorkerAppStore) MarkSavePointCreateWriterDrainPendingWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	operation := record.Record()
	existing, ok := store.records[operation.ID]
	if !ok || existing.State != operations.OperationStateRunning || existing.LeaseOwner != owner || existing.LeaseExpiresAt == nil || !existing.LeaseExpiresAt.After(now) {
		return operations.OperationRecord{}, operations.ErrLeaseUnavailable
	}
	operation.LeaseOwner = existing.LeaseOwner
	operation.LeaseExpiresAt = &now
	store.records[operation.ID] = operation
	store.operation = operation
	return operation, nil
}

func (store *fakeWorkerAppStore) RecordSavePointCreateRecoveryCapability(_ context.Context, owner string, observedAt, expiresAt time.Time) error {
	if store.savePointCapabilityErr != nil {
		return store.savePointCapabilityErr
	}
	store.savePointCapabilityOwner = owner
	store.savePointCapabilityObservedAt = observedAt
	store.savePointCapabilityExpiresAt = expiresAt
	return nil
}

func (store *fakeWorkerAppStore) CommitTemplateCreateSucceededWithLease(_ context.Context, template resources.Repo, _ string, _ string, _ string, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.repo = template
	store.auditEvents = append(store.auditEvents, event)
	return template, operation, nil
}

func (store *fakeWorkerAppStore) MarkTemplateCreateWriterFencedWithLease(_ context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, _ string, _ time.Time) (fences.Fence, operations.OperationRecord, error) {
	operation := record.Record()
	store.fences = append(store.fences, fence)
	store.records[operation.ID] = operation
	store.operation = operation
	return fence, operation, nil
}

func (store *fakeWorkerAppStore) CommitTemplateCreateFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return store.commitTemplateFailed(record, now, event)
}

func (store *fakeWorkerAppStore) CommitTemplateCloneSucceededWithLease(_ context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.repo = repo
	store.auditEvents = append(store.auditEvents, event)
	return repo, operation, nil
}

func (store *fakeWorkerAppStore) CommitTemplateCloneFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return store.commitTemplateFailed(record, now, event)
}

func (store *fakeWorkerAppStore) commitTemplateFailed(record operations.SanitizedOperationRecord, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	if operation.Type == operations.OperationTemplateCreate && operation.Phase == operations.OperationPhaseTemplateCreateWriterFenced && operation.State == operations.OperationStateFailed {
		store.releaseWorkerAppWriterFence(operation.SessionFenceID, now)
	}
	return operation, nil
}

func (store *fakeWorkerAppStore) MarkRestoreWriterFencedWithLease(_ context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, _ string, _ time.Time) (fences.Fence, operations.OperationRecord, error) {
	operation := record.Record()
	store.records[operation.ID] = operation
	store.operation = operation
	for _, existing := range store.fences {
		if existing.ID == fence.ID && existing.Kind == fences.KindWriterSession && existing.HolderOperationID == operation.ID && existing.Status == fences.StatusActive && existing.ReleasedAt == nil && existing.RecoveredAt == nil {
			return existing, operation, nil
		}
	}
	store.fences = append(store.fences, fence)
	return fence, operation, nil
}

func (store *fakeWorkerAppStore) CommitRestoreSucceededWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	store.releaseWorkerAppWriterFence(operation.SessionFenceID, now)
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
}

func (store *fakeWorkerAppStore) CommitRestoreFailedWithLease(_ context.Context, record operations.SanitizedOperationRecord, _ string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	store.records[operation.ID] = operation
	if operation.Phase == operations.OperationPhaseRestoreWriterFenced && operation.State == operations.OperationStateFailed {
		store.releaseWorkerAppWriterFence(operation.SessionFenceID, now)
	}
	store.operation = operation
	store.auditEvents = append(store.auditEvents, event)
	return operation, nil
}

func (store *fakeWorkerAppStore) releaseWorkerAppWriterFence(fenceID string, now time.Time) {
	for idx, fence := range store.fences {
		if fence.ID == fenceID && fence.Kind == fences.KindWriterSession && fence.Status == fences.StatusActive && fence.ReleasedAt == nil && fence.RecoveredAt == nil {
			releasedAt := now
			store.fences[idx].Status = fences.StatusReleased
			store.fences[idx].ReleasedAt = &releasedAt
			store.fences[idx].UpdatedAt = now
			store.releasedFenceID = fenceID
			return
		}
	}
}

func (store *fakeWorkerAppStore) GetOperation(_ context.Context, operationID string) (operations.OperationRecord, error) {
	if record, ok := store.records[operationID]; ok {
		return record, nil
	}
	return operations.OperationRecord{}, errors.New("operation not found")
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
		AllowedCallers:    []resources.AllowedCaller{{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}}},
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         workerAppNow().Add(-time.Hour),
		UpdatedAt:         workerAppNow(),
	}, nil
}

func (store *fakeWorkerAppStore) GetVolume(context.Context, string) (resources.Volume, error) {
	if store.volumeReadErr != nil {
		return resources.Volume{}, store.volumeReadErr
	}
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
	calls                []string
	beforeInit           func(context.Context) error
	beforeDirectRestore  func(context.Context) error
	initSummary          jvsrunner.InitSummary
	doctorSummary        jvsrunner.DoctorSummary
	saveSummary          jvsrunner.SaveSummary
	historySummary       jvsrunner.HistorySummary
	directTarget         jvsrunner.DirectTarget
	directSaveSummary    jvsrunner.DirectSaveSummary
	directListSummary    jvsrunner.DirectListSummary
	directRestoreSummary jvsrunner.DirectRestoreSummary
	directCloneSummary   jvsrunner.DirectCloneSummary
	directStatusSummary  jvsrunner.DirectStatusSummary
	directDoctorSummary  jvsrunner.DirectDoctorSummary
	directSaveErr        error
	directListErr        error
	directRestoreErr     error
	directCloneErr       error
	directStatusErr      error
	directDoctorErr      error
	controlRoot          string
	payloadRoot          string
	saveMessage          string
	savePurpose          string
	restoreSavePointID   string
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

func (runner *workerAppFakeJVSRunner) Init(ctx context.Context, _, _ string) (jvsrunner.InitSummary, error) {
	runner.calls = append(runner.calls, "init")
	if runner.beforeInit != nil {
		if err := runner.beforeInit(ctx); err != nil {
			return jvsrunner.InitSummary{}, err
		}
	}
	return runner.initSummary, nil
}

func (runner *workerAppFakeJVSRunner) DirectClone(_ context.Context, source jvsrunner.DirectTarget, target jvsrunner.DirectTarget, savePointID string) (jvsrunner.DirectCloneSummary, error) {
	runner.calls = append(runner.calls, "direct_clone")
	runner.directTarget = source
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	if runner.directCloneSummary.SourceRepoID == "" {
		sourceRepoID := runner.directCloneSummary.SourceRepoID
		if sourceRepoID == "" {
			sourceRepoID = "jvs_repo_alpha"
		}
		targetRepoID := runner.directCloneSummary.TargetRepoID
		if targetRepoID == "" {
			targetRepoID = "jvs_repo_clone"
		}
		runner.directCloneSummary = jvsrunner.DirectCloneSummary{SourceRepoID: sourceRepoID, TargetRepoID: targetRepoID, SavePointID: savePointID, SavePointsMode: "main", SavePointsCopiedCount: 1, RuntimeStateCopied: false, Workspace: "main"}
	}
	return runner.directCloneSummary, runner.directCloneErr
}

func (runner *workerAppFakeJVSRunner) DirectSave(ctx context.Context, target jvsrunner.DirectTarget, message string) (jvsrunner.DirectSaveSummary, error) {
	return runner.DirectSaveWithPurpose(ctx, target, message, "")
}

func (runner *workerAppFakeJVSRunner) DirectSaveWithPurpose(_ context.Context, target jvsrunner.DirectTarget, message string, purpose string) (jvsrunner.DirectSaveSummary, error) {
	runner.calls = append(runner.calls, "direct_save")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	runner.saveMessage = message
	runner.savePurpose = purpose
	if runner.directSaveErr != nil {
		return runner.directSaveSummary, runner.directSaveErr
	}
	if runner.directSaveSummary.SavePointID == "" && runner.saveSummary.SavePointID != "" {
		runner.directSaveSummary = jvsrunner.DirectSaveSummary{SavePointID: runner.saveSummary.SavePointID, HistoryHeadID: runner.saveSummary.NewestSavePointID, Message: message, Purpose: purpose, CreatedAt: runner.saveSummary.CreatedAt}
	}
	if runner.directSaveSummary.SavePointID == "" {
		runner.directSaveSummary = jvsrunner.DirectSaveSummary{SavePointID: "sp_001", HistoryHeadID: "sp_001", Message: message, Purpose: purpose, CreatedAt: "2026-05-05T12:00:00Z"}
	}
	if runner.directSaveSummary.HistoryHeadID == "" {
		runner.directSaveSummary.HistoryHeadID = runner.directSaveSummary.SavePointID
	}
	if runner.directSaveSummary.Message == "" {
		runner.directSaveSummary.Message = message
	}
	return runner.directSaveSummary, nil
}

func (runner *workerAppFakeJVSRunner) DirectList(_ context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectListSummary, error) {
	runner.calls = append(runner.calls, "direct_list")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	if runner.directListSummary.HistoryHeadID == "" && len(runner.directListSummary.SavePoints) == 0 && runner.directSaveSummary.SavePointID != "" {
		runner.directListSummary = jvsrunner.DirectListSummary{
			HistoryHeadID: runner.directSaveSummary.SavePointID,
			SavePoints: []jvsrunner.DirectSavePointSummary{{
				SavePointID: runner.directSaveSummary.SavePointID,
				Message:     runner.directSaveSummary.Message,
				Purpose:     runner.directSaveSummary.Purpose,
				CreatedAt:   runner.directSaveSummary.CreatedAt,
				HistoryHead: true,
			}},
		}
	}
	if runner.directListSummary.HistoryHeadID == "" && len(runner.directListSummary.SavePoints) == 0 {
		savePoints := make([]jvsrunner.DirectSavePointSummary, 0, len(runner.historySummary.SavePoints))
		for _, savePoint := range runner.historySummary.SavePoints {
			savePoints = append(savePoints, jvsrunner.DirectSavePointSummary{SavePointID: savePoint.SavePointID, Message: savePoint.Message, Purpose: savePoint.Purpose, CreatedAt: savePoint.CreatedAt, HistoryHead: savePoint.SavePointID == runner.historySummary.NewestSavePointID})
		}
		runner.directListSummary = jvsrunner.DirectListSummary{HistoryHeadID: runner.historySummary.NewestSavePointID, SavePoints: savePoints}
	}
	return runner.directListSummary, runner.directListErr
}

func (runner *workerAppFakeJVSRunner) DirectRestore(ctx context.Context, target jvsrunner.DirectTarget, savePointID string) (jvsrunner.DirectRestoreSummary, error) {
	runner.calls = append(runner.calls, "direct_restore")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	runner.restoreSavePointID = savePointID
	if runner.beforeDirectRestore != nil {
		if err := runner.beforeDirectRestore(ctx); err != nil {
			return jvsrunner.DirectRestoreSummary{}, err
		}
	}
	if runner.directRestoreSummary.RestoredSavePointID == "" {
		runner.directRestoreSummary = jvsrunner.DirectRestoreSummary{RestoredSavePointID: savePointID, NewHeadID: savePointID}
	}
	return runner.directRestoreSummary, runner.directRestoreErr
}

func (runner *workerAppFakeJVSRunner) DirectStatus(_ context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectStatusSummary, error) {
	runner.calls = append(runner.calls, "direct_status")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	if runner.directStatusSummary.HistoryHeadID == "" {
		runner.directStatusSummary = jvsrunner.DirectStatusSummary{HistoryHeadID: runner.restoreSavePointID, MetadataState: "ready", ActiveOperation: "none", Recovery: "none"}
	}
	return runner.directStatusSummary, runner.directStatusErr
}

func (runner *workerAppFakeJVSRunner) DirectDoctor(_ context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectDoctorSummary, error) {
	runner.calls = append(runner.calls, "direct_doctor")
	runner.directTarget = target
	runner.controlRoot = target.ControlRoot
	runner.payloadRoot = target.Home
	if runner.directDoctorSummary.RepoID == "" && runner.directDoctorSummary.MetadataState == "" {
		repoID := runner.doctorSummary.RepoID
		if repoID == "" {
			repoID = "jvs_repo_alpha"
		}
		healthy := runner.doctorSummary.Healthy
		if runner.doctorSummary == (jvsrunner.DoctorSummary{}) {
			healthy = true
		}
		runner.directDoctorSummary = jvsrunner.DirectDoctorSummary{RepoID: repoID, Healthy: healthy, FindingCount: 0, MetadataState: "ready", Journal: "clean", Recovery: "none"}
	}
	return runner.directDoctorSummary, runner.directDoctorErr
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
