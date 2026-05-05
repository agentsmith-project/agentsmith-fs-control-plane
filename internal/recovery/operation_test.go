package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestOperationCoordinatorRejectsInvalidConfigBeforeStoreCalls(t *testing.T) {
	now := recoveryTestNow()
	tests := []struct {
		name   string
		config OperationConfig
	}{
		{name: "nil reader", config: OperationConfig{LeaseStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now}},
		{name: "nil lease store", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now}},
		{name: "nil executor", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now}},
		{name: "blank owner", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: " \t", LeaseDuration: time.Minute, Limit: 1, Now: now}},
		{name: "non-positive duration", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", Limit: 1, Now: now}},
		{name: "non-positive limit", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Now: now}},
		{name: "zero now", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, _ := tt.config.Reader.(*fakeOperationRecoveryReader)
			store, _ := tt.config.LeaseStore.(*fakeOperationLeaseStore)

			result, err := NewOperationCoordinator(tt.config).RunOnce(context.Background())
			if err == nil {
				t.Fatal("RunOnce succeeded, want invalid config error")
			}
			if result.Scanned != 0 {
				t.Fatalf("result scanned = %d, want 0", result.Scanned)
			}
			if reader != nil && reader.calls != 0 {
				t.Fatalf("reader calls = %d, want 0", reader.calls)
			}
			if store != nil && len(store.acquireCalls) != 0 {
				t.Fatalf("lease calls = %d, want 0", len(store.acquireCalls))
			}
			if executor, ok := tt.config.Executor.(*fakeOperationExecutor); ok && len(executor.calls) != 0 {
				t.Fatalf("executor calls = %d, want 0", len(executor.calls))
			}
			if executor, ok := tt.config.Executor.(*fakeOperationExecutor); ok && len(executor.supportCalls) != 0 {
				t.Fatalf("support calls = %d, want 0", len(executor.supportCalls))
			}
		})
	}
}

func TestOperationCoordinatorReaderReceivesContextNowAndLimit(t *testing.T) {
	ctx := context.WithValue(context.Background(), recoveryContextKey("test"), "ctx")
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         7,
		Now:           now,
	})

	if _, err := coordinator.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if reader.lastContext != ctx {
		t.Fatal("reader did not receive request context")
	}
	if !reader.lastNow.Equal(now) || reader.lastLimit != 7 {
		t.Fatalf("reader now/limit = %v/%d, want %v/7", reader.lastNow, reader.lastLimit, now)
	}
}

func TestOperationCoordinatorAcquiresClaimRetryReclaimAndFinalizeInReaderOrder(t *testing.T) {
	now := recoveryTestNow()
	expired := now.Add(-time.Minute)
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			{ID: "op_claim", State: operations.OperationStateQueued, Phase: "reader-claim"},
			{ID: "op_retry", State: operations.OperationStateQueued, Attempt: 2, Phase: "reader-retry"},
			{ID: "op_reclaim", State: operations.OperationStateRunning, Phase: "reader-reclaim", LeaseOwner: "worker-a", LeaseExpiresAt: &expired},
			{ID: "op_cancel", State: operations.OperationStateCancelRequested},
		},
	}
	store := &fakeOperationLeaseStore{acquireRecords: map[string]operations.OperationRecord{
		"op_claim":   {ID: "op_claim", State: operations.OperationStateRunning, Phase: "updated-claim"},
		"op_retry":   {ID: "op_retry", State: operations.OperationStateRunning, Phase: "updated-retry"},
		"op_reclaim": {ID: "op_reclaim", State: operations.OperationStateRunning, Phase: "updated-reclaim"},
		"op_cancel":  {ID: "op_cancel", State: operations.OperationStateCancelled},
	}}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: 5 * time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Scanned != 4 || result.Claimed != 2 || result.Reclaimed != 1 || result.Finalized != 1 {
		t.Fatalf("result = %#v, want scanned 4 claimed 2 reclaimed 1 finalized 1", result)
	}
	gotIDs := store.acquireOperationIDs()
	if strings.Join(gotIDs, ",") != "op_claim,op_retry,op_reclaim,op_cancel" {
		t.Fatalf("acquire order = %#v", gotIDs)
	}
	for _, call := range store.acquireCalls {
		if call.request.Owner != "recovery-worker" || call.request.Duration != 5*time.Minute || !call.request.Now.Equal(now) {
			t.Fatalf("lease request = %#v, want configured owner/duration/now", call.request)
		}
	}
	if store.acquireCalls[3].request.CancelPolicy != operations.LeaseCancelPolicyFinalize {
		t.Fatalf("cancel policy = %#v, want finalize", store.acquireCalls[3].request.CancelPolicy)
	}
	if gotIDs := executor.supportOperationIDs(); strings.Join(gotIDs, ",") != "op_claim,op_retry,op_reclaim" {
		t.Fatalf("support check order = %#v, want claim retry reclaim only", gotIDs)
	}
	if gotIDs := executor.operationIDs(); strings.Join(gotIDs, ",") != "op_claim,op_retry,op_reclaim" {
		t.Fatalf("executor order = %#v, want claim retry reclaim only", gotIDs)
	}
	if gotPhases := executor.operationPhases(); strings.Join(gotPhases, ",") != "updated-claim,updated-retry,updated-reclaim" {
		t.Fatalf("executor phases = %#v, want updated records from lease store", gotPhases)
	}
	if executor.calls[0].plan.Action != RecoveryActionClaimable || executor.calls[1].plan.Action != RecoveryActionRetry || executor.calls[2].plan.Action != RecoveryActionReclaim {
		t.Fatalf("executor plans = %#v", executor.calls)
	}
	if store.updateCalls != 0 || store.renewCalls != 0 {
		t.Fatalf("unexpected lease store calls update=%d renew=%d", store.updateCalls, store.renewCalls)
	}
}

func TestOperationCoordinatorMarksUnsupportedClaimRetryAndReclaimWithoutLeaseOrExecute(t *testing.T) {
	now := recoveryTestNow()
	expired := now.Add(-time.Minute)
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			{ID: "op_claim", State: operations.OperationStateQueued},
			{ID: "op_retry", State: operations.OperationStateQueued, Attempt: 2},
			{ID: "op_reclaim", State: operations.OperationStateRunning, LeaseOwner: "worker-a", LeaseExpiresAt: &expired},
		},
	}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{unsupported: map[string]string{
		"op_claim":   "unsupported claim",
		"op_retry":   "unsupported retry",
		"op_reclaim": "unsupported reclaim",
	}}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Unsupported != 3 || len(store.acquireCalls) != 0 || len(executor.calls) != 0 {
		t.Fatalf("result/acquire/execute = %#v/%d/%d, want unsupported 3 and no calls", result, len(store.acquireCalls), len(executor.calls))
	}
	if gotIDs := executor.supportOperationIDs(); strings.Join(gotIDs, ",") != "op_claim,op_retry,op_reclaim" {
		t.Fatalf("support checks = %#v", gotIDs)
	}
}

func TestOperationCoordinatorContinuesAfterUnsupportedCandidate(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			{ID: "op_unsupported", State: operations.OperationStateQueued},
			{ID: "op_supported", State: operations.OperationStateQueued},
		},
	}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{unsupported: map[string]string{"op_unsupported": "not implemented"}}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Unsupported != 1 || result.Claimed != 1 {
		t.Fatalf("result = %#v, want unsupported 1 claimed 1", result)
	}
	if gotIDs := store.acquireOperationIDs(); strings.Join(gotIDs, ",") != "op_supported" {
		t.Fatalf("acquire IDs = %#v, want supported only", gotIDs)
	}
	if gotIDs := executor.operationIDs(); strings.Join(gotIDs, ",") != "op_supported" {
		t.Fatalf("executor IDs = %#v, want supported only", gotIDs)
	}
}

func TestOperationCoordinatorUsesFallbackReasonForUnsupportedWithoutExecutorReason(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		{ID: "op_unsupported", State: operations.OperationStateQueued},
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{unsupported: map[string]string{"op_unsupported": " \t"}}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("result items = %#v, want one unsupported result", result.Results)
	}
	if result.Results[0].Outcome != OperationOutcomeUnsupported || result.Results[0].Reason != "unsupported_operation_recovery" {
		t.Fatalf("unsupported result = %#v, want fallback unsupported reason", result.Results[0])
	}
	if result.Results[0].Reason == "queued_operation_claimable" {
		t.Fatalf("unsupported result kept planner reason: %#v", result.Results[0])
	}
}

func TestOperationCoordinatorSkipsWaitNoopManualAndAutomaticRecoverPlans(t *testing.T) {
	now := recoveryTestNow()
	live := now.Add(time.Minute)
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			{ID: "op_wait", State: operations.OperationStateRunning, LeaseOwner: "worker-a", LeaseExpiresAt: &live},
			{ID: "op_noop", State: operations.OperationStateSucceeded},
			{ID: "op_manual", State: operations.OperationStateRunning},
			{ID: "op_operator", State: operations.OperationStateOperatorInterventionRequired},
		},
	}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Skipped != 2 || result.Manual != 2 || len(store.acquireCalls) != 0 || len(executor.calls) != 0 || len(executor.supportCalls) != 0 {
		t.Fatalf("result/store/executor/support = %#v/%d/%d/%d calls, want skipped 2 manual 2 no calls", result, len(store.acquireCalls), len(executor.calls), len(executor.supportCalls))
	}
}

func TestOperationCoordinatorCountsLeaseUnavailableRaceAsNonFatal(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		{ID: "op_race", State: operations.OperationStateQueued},
		{ID: "op_claim", State: operations.OperationStateQueued},
	}}
	store := &fakeOperationLeaseStore{acquireErrors: map[string]error{"op_race": operations.ErrLeaseUnavailable}}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce returned fatal error for lease race: %v", err)
	}
	if result.RaceLost != 1 || result.Claimed != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v, want race lost 1 claimed 1 and failed 0", result)
	}
	if gotIDs := store.acquireOperationIDs(); strings.Join(gotIDs, ",") != "op_race,op_claim" {
		t.Fatalf("acquire order = %#v, want race then claim", gotIDs)
	}
	if gotIDs := executor.operationIDs(); strings.Join(gotIDs, ",") != "op_claim" {
		t.Fatalf("executor order = %#v, want only non-race claim", gotIDs)
	}
}

func TestOperationCoordinatorFinalizeCancellationDoesNotRequireExecutorSupport(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		{ID: "op_cancel", State: operations.OperationStateCancelRequested},
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{unsupported: map[string]string{"op_cancel": "executor does not finalize"}}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Finalized != 1 || len(store.acquireCalls) != 1 || store.acquireCalls[0].request.CancelPolicy != operations.LeaseCancelPolicyFinalize {
		t.Fatalf("result/acquire = %#v/%#v, want finalized via lease store", result, store.acquireCalls)
	}
	if len(executor.supportCalls) != 0 || len(executor.calls) != 0 {
		t.Fatalf("support/execute calls = %d/%d, want none", len(executor.supportCalls), len(executor.calls))
	}
}

func TestOperationCoordinatorReturnsNonLeaseAcquireErrorWithPartialResult(t *testing.T) {
	now := recoveryTestNow()
	acquireErr := errors.New("postgres unavailable")
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			{ID: "op_claim", State: operations.OperationStateQueued},
			{ID: "op_after_error", State: operations.OperationStateQueued},
		},
	}
	store := &fakeOperationLeaseStore{acquireErr: acquireErr}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, acquireErr) {
		t.Fatalf("RunOnce error = %v, want acquire error", err)
	}
	if result.Scanned != 2 || result.Failed != 1 || len(store.acquireCalls) != 1 {
		t.Fatalf("result/calls = %#v/%d, want partial result after first failed acquire", result, len(store.acquireCalls))
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d, want 0 after acquire error", len(executor.calls))
	}
}

func TestOperationCoordinatorReturnsExecutorErrorWithPartialResult(t *testing.T) {
	now := recoveryTestNow()
	executorErr := errors.New("executor failed")
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		{ID: "op_claim", State: operations.OperationStateQueued},
		{ID: "op_after_error", State: operations.OperationStateQueued},
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{err: executorErr}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, executorErr) {
		t.Fatalf("RunOnce error = %v, want executor error", err)
	}
	if result.Scanned != 2 || result.Failed != 1 || len(store.acquireCalls) != 1 || len(executor.calls) != 1 {
		t.Fatalf("result/acquire/executor = %#v/%d/%d, want partial result after executor error", result, len(store.acquireCalls), len(executor.calls))
	}
}

func TestOperationCoordinatorCountsCommittedOperatorInterventionAsManual(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		{ID: "op_intervention", State: operations.OperationStateQueued},
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{err: ErrOperationManualIntervention}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Manual != 1 || result.Failed != 0 || result.Unsupported != 0 {
		t.Fatalf("result = %#v, want manual=1 failed=0 unsupported=0", result)
	}
	if len(result.Results) != 1 || result.Results[0].Outcome != OperationOutcomeManual {
		t.Fatalf("results = %#v, want manual outcome", result.Results)
	}
}

func TestOperationCoordinatorReaderErrorDoesNotCallLeaseStoreOrExecutor(t *testing.T) {
	now := recoveryTestNow()
	readerErr := errors.New("reader failed")
	reader := &fakeOperationRecoveryReader{err: readerErr}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	_, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, readerErr) {
		t.Fatalf("RunOnce error = %v, want reader error", err)
	}
	if len(store.acquireCalls) != 0 || len(executor.calls) != 0 || len(executor.supportCalls) != 0 {
		t.Fatalf("lease/executor/support calls = %d/%d/%d, want none", len(store.acquireCalls), len(executor.calls), len(executor.supportCalls))
	}
}

func TestOperationCoordinatorSkipsCancelRequestedLiveLease(t *testing.T) {
	now := recoveryTestNow()
	live := now.Add(time.Minute)
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		{ID: "op_cancel_live", State: operations.OperationStateCancelRequested, LeaseOwner: "worker-a", LeaseExpiresAt: &live},
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Skipped != 1 || len(store.acquireCalls) != 0 || len(executor.calls) != 0 {
		t.Fatalf("result/acquire/executor = %#v/%d/%d, want skipped and no calls", result, len(store.acquireCalls), len(executor.calls))
	}
}

func TestOperationCoordinatorProcessesOnlyLimitCandidates(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		{ID: "op_one", State: operations.OperationStateQueued},
		{ID: "op_two", State: operations.OperationStateQueued},
		{ID: "op_three", State: operations.OperationStateQueued},
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         2,
		Now:           now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Scanned != 2 || result.Claimed != 2 {
		t.Fatalf("result = %#v, want scanned/claimed 2", result)
	}
	if gotIDs := executor.operationIDs(); strings.Join(gotIDs, ",") != "op_one,op_two" {
		t.Fatalf("executor IDs = %#v, want first two only", gotIDs)
	}
}

type recoveryContextKey string

type fakeOperationRecoveryReader struct {
	records     []operations.OperationRecord
	err         error
	calls       int
	lastContext context.Context
	lastNow     time.Time
	lastLimit   int
}

func (reader *fakeOperationRecoveryReader) ListOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	reader.calls++
	reader.lastContext = ctx
	reader.lastNow = now
	reader.lastLimit = limit
	if reader.err != nil {
		return nil, reader.err
	}
	out := make([]operations.OperationRecord, len(reader.records))
	copy(out, reader.records)
	return out, nil
}

type fakeOperationLeaseStore struct {
	acquireCalls   []operationAcquireCall
	acquireErr     error
	acquireErrors  map[string]error
	acquireRecords map[string]operations.OperationRecord
	renewCalls     int
	updateCalls    int
}

type operationAcquireCall struct {
	operationID string
	request     operations.LeaseRequest
}

func (store *fakeOperationLeaseStore) AcquireOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	store.acquireCalls = append(store.acquireCalls, operationAcquireCall{operationID: operationID, request: request})
	if err := store.acquireErrors[operationID]; err != nil {
		return operations.OperationRecord{}, err
	}
	if store.acquireErr != nil {
		return operations.OperationRecord{}, store.acquireErr
	}
	if record, ok := store.acquireRecords[operationID]; ok {
		return record, nil
	}
	return operations.OperationRecord{ID: operationID, State: operations.OperationStateRunning}, nil
}

func (store *fakeOperationLeaseStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	store.renewCalls++
	return operations.OperationRecord{}, errors.New("unexpected renew call")
}

func (store *fakeOperationLeaseStore) UpdateOperationWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	store.updateCalls++
	return operations.OperationRecord{}, errors.New("unexpected update call")
}

func (store *fakeOperationLeaseStore) acquireOperationIDs() []string {
	out := make([]string, len(store.acquireCalls))
	for idx, call := range store.acquireCalls {
		out[idx] = call.operationID
	}
	return out
}

type fakeOperationExecutor struct {
	supportCalls []operationSupportCall
	calls        []operationExecutorCall
	err          error
	unsupported  map[string]string
}

type operationSupportCall struct {
	record operations.OperationRecord
	plan   RecoveryPlan
}

type operationExecutorCall struct {
	record operations.OperationRecord
	plan   RecoveryPlan
}

func (executor *fakeOperationExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan RecoveryPlan) OperationSupport {
	executor.supportCalls = append(executor.supportCalls, operationSupportCall{record: record, plan: plan})
	if reason := executor.unsupported[record.ID]; reason != "" {
		return OperationSupport{Supported: false, Reason: reason}
	}
	return OperationSupport{Supported: true}
}

func (executor *fakeOperationExecutor) ExecuteOperationRecovery(_ context.Context, record operations.OperationRecord, plan RecoveryPlan) error {
	executor.calls = append(executor.calls, operationExecutorCall{record: record, plan: plan})
	return executor.err
}

func (executor *fakeOperationExecutor) supportOperationIDs() []string {
	out := make([]string, len(executor.supportCalls))
	for idx, call := range executor.supportCalls {
		out[idx] = call.record.ID
	}
	return out
}

func (executor *fakeOperationExecutor) operationIDs() []string {
	out := make([]string, len(executor.calls))
	for idx, call := range executor.calls {
		out[idx] = call.record.ID
	}
	return out
}

func (executor *fakeOperationExecutor) operationPhases() []string {
	out := make([]string, len(executor.calls))
	for idx, call := range executor.calls {
		out[idx] = call.record.Phase
	}
	return out
}

func recoveryTestNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}
