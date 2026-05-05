package workerapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
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
	if !strings.Contains(err.Error(), "open worker operation recovery store") {
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
	records         map[string]operations.OperationRecord
	order           []string
	volume          resources.Volume
	namespace       resources.Namespace
	binding         resources.NamespaceVolumeBinding
	operation       operations.OperationRecord
	auditEvents     []audit.Event
	listDeadline    time.Time
	closeCalls      int
	acquireIDs      []string
	acquirePolicies map[string]operations.LeaseCancelPolicy
}

func newWorkerAppStore(records ...operations.OperationRecord) *fakeWorkerAppStore {
	store := &fakeWorkerAppStore{records: map[string]operations.OperationRecord{}, acquirePolicies: map[string]operations.LeaseCancelPolicy{}}
	for _, record := range records {
		store.records[record.ID] = record
		store.order = append(store.order, record.ID)
	}
	return store
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

type fakeOperationRecoveryRunner struct {
	result recovery.OperationBatchResult
	err    error
}

func (runner *fakeOperationRecoveryRunner) RunOnce(context.Context) (recovery.OperationBatchResult, error) {
	return runner.result, runner.err
}
