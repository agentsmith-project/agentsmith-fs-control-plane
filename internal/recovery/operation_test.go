package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestOperationCoordinatorRejectsInvalidConfigBeforeStoreCalls(t *testing.T) {
	now := recoveryTestNow()
	tests := []struct {
		name   string
		config OperationConfig
	}{
		{name: "nil reader", config: OperationConfig{LeaseStore: &fakeOperationLeaseStore{}, CommitStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now, AuditEventID: fakeOperationAuditEventID}},
		{name: "nil lease store", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, CommitStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now, AuditEventID: fakeOperationAuditEventID}},
		{name: "nil commit store", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now, AuditEventID: fakeOperationAuditEventID}},
		{name: "nil executor", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, CommitStore: &fakeOperationLeaseStore{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now, AuditEventID: fakeOperationAuditEventID}},
		{name: "blank owner", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, CommitStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: " \t", LeaseDuration: time.Minute, Limit: 1, Now: now, AuditEventID: fakeOperationAuditEventID}},
		{name: "non-positive duration", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, CommitStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", Limit: 1, Now: now, AuditEventID: fakeOperationAuditEventID}},
		{name: "non-positive limit", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, CommitStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Now: now, AuditEventID: fakeOperationAuditEventID}},
		{name: "zero now", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, CommitStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, AuditEventID: fakeOperationAuditEventID}},
		{name: "nil audit event id", config: OperationConfig{Reader: &fakeOperationRecoveryReader{}, LeaseStore: &fakeOperationLeaseStore{}, CommitStore: &fakeOperationLeaseStore{}, Executor: &fakeOperationExecutor{}, Owner: "recovery-worker", LeaseDuration: time.Minute, Limit: 1, Now: now}},
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
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         7,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
			recoveryOperationRecord("op_claim", operations.OperationStateQueued, "reader-claim", now),
			func() operations.OperationRecord {
				record := recoveryOperationRecord("op_retry", operations.OperationStateQueued, "reader-retry", now)
				record.Attempt = 2
				return record
			}(),
			func() operations.OperationRecord {
				record := recoveryOperationRecord("op_reclaim", operations.OperationStateRunning, "reader-reclaim", now)
				record.LeaseOwner = "worker-a"
				record.LeaseExpiresAt = &expired
				return record
			}(),
			recoveryOperationRecord("op_cancel", operations.OperationStateCancelRequested, operations.OperationPhaseRepoCreateValidate, now),
		},
	}
	store := &fakeOperationLeaseStore{acquireRecords: map[string]operations.OperationRecord{
		"op_claim":   recoveryOperationRecord("op_claim", operations.OperationStateRunning, "updated-claim", now),
		"op_retry":   recoveryOperationRecord("op_retry", operations.OperationStateRunning, "updated-retry", now),
		"op_reclaim": recoveryOperationRecord("op_reclaim", operations.OperationStateRunning, "updated-reclaim", now),
		"op_cancel":  recoveryOperationRecord("op_cancel", operations.OperationStateCancelled, operations.OperationPhaseRepoCreateValidate, now),
	}}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: 5 * time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
	if len(store.updateCalls) != 0 || store.renewCalls != 0 {
		t.Fatalf("unexpected lease store calls update=%d renew=%d", len(store.updateCalls), store.renewCalls)
	}
}

func TestOperationCoordinatorCommitsUnsupportedClaimRetryAndReclaimWithAuditWithoutExecute(t *testing.T) {
	now := recoveryTestNow()
	expired := now.Add(-time.Minute)
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			recoveryOperationRecord("op_claim", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
			func() operations.OperationRecord {
				record := recoveryOperationRecord("op_retry", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now)
				record.Attempt = 2
				return record
			}(),
			func() operations.OperationRecord {
				record := recoveryOperationRecord("op_reclaim", operations.OperationStateRunning, operations.OperationPhaseRepoCreateValidate, now)
				record.LeaseOwner = "worker-a"
				record.LeaseExpiresAt = &expired
				return record
			}(),
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
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Unsupported != 3 || len(store.acquireCalls) != 3 || len(store.commitCalls) != 3 || len(store.updateCalls) != 0 || len(executor.calls) != 0 {
		t.Fatalf("result/acquire/commit/update/execute = %#v/%d/%d/%d/%d, want unsupported 3 with audit commits and no update-only/execute", result, len(store.acquireCalls), len(store.commitCalls), len(store.updateCalls), len(executor.calls))
	}
	if gotIDs := executor.supportOperationIDs(); strings.Join(gotIDs, ",") != "op_claim,op_retry,op_reclaim" {
		t.Fatalf("support checks = %#v", gotIDs)
	}
	if gotIDs := store.acquireOperationIDs(); strings.Join(gotIDs, ",") != "op_claim,op_retry,op_reclaim" {
		t.Fatalf("acquire IDs = %#v, want unsupported candidates leased in order", gotIDs)
	}
	if gotIDs := store.commitOperationIDs(); strings.Join(gotIDs, ",") != "op_claim,op_retry,op_reclaim" {
		t.Fatalf("commit IDs = %#v, want unsupported candidates persisted in order", gotIDs)
	}
	for _, call := range store.commitCalls {
		if call.owner != "recovery-worker" || !call.now.Equal(now) {
			t.Fatalf("commit fence = %q/%v, want configured owner/now", call.owner, call.now)
		}
		if call.record.State != operations.OperationStateOperatorInterventionRequired {
			t.Fatalf("unsupported commit state = %q, want operator_intervention_required", call.record.State)
		}
		if call.record.Error == nil || call.record.Error.Code != "OPERATION_RECOVERY_REQUIRED" || call.record.Error.Retryable {
			t.Fatalf("unsupported error = %#v, want stable non-retryable unsupported recovery error", call.record.Error)
		}
		reason, _ := call.record.Error.Details["reason"].(string)
		if reason == "" {
			t.Fatalf("unsupported error details = %#v, want reason evidence", call.record.Error.Details)
		}
		if call.event.Type != audit.EventTypeRepoCreate || call.event.Outcome != audit.OutcomeFailed || call.event.Reason != "unsupported_operation_recovery" || call.event.OperationID != call.record.ID {
			t.Fatalf("unsupported audit = %#v, want failed repo_create unsupported event for operation", call.event)
		}
		if call.event.Details["reason"] != reason || call.event.Details["evidence"] == nil {
			t.Fatalf("unsupported audit details = %#v, want reason/evidence", call.event.Details)
		}
	}
}

func TestOperationCoordinatorContinuesAfterUnsupportedCandidate(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			recoveryOperationRecord("op_unsupported", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
			recoveryOperationRecord("op_supported", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
		},
	}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{unsupported: map[string]string{"op_unsupported": "not implemented"}}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
	})

	result, err := coordinator.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Unsupported != 1 || result.Claimed != 1 {
		t.Fatalf("result = %#v, want unsupported 1 claimed 1", result)
	}
	if gotIDs := store.acquireOperationIDs(); strings.Join(gotIDs, ",") != "op_unsupported,op_supported" {
		t.Fatalf("acquire IDs = %#v, want unsupported terminalized then supported claimed", gotIDs)
	}
	if gotIDs := store.commitOperationIDs(); strings.Join(gotIDs, ",") != "op_unsupported" {
		t.Fatalf("commit IDs = %#v, want unsupported candidate persisted", gotIDs)
	}
	if gotIDs := executor.operationIDs(); strings.Join(gotIDs, ",") != "op_supported" {
		t.Fatalf("executor IDs = %#v, want supported only", gotIDs)
	}
}

func TestOperationCoordinatorUsesFallbackReasonForUnsupportedWithoutExecutorReason(t *testing.T) {
	now := recoveryTestNow()
	reader := &fakeOperationRecoveryReader{records: []operations.OperationRecord{
		recoveryOperationRecord("op_unsupported", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{unsupported: map[string]string{"op_unsupported": " \t"}}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
	if len(store.commitCalls) != 1 {
		t.Fatalf("commits = %#v, want unsupported candidate persisted with audit", store.commitCalls)
	}
	if got := store.commitCalls[0].record.Error.Details["reason"]; got != "unsupported_operation_recovery" {
		t.Fatalf("persisted reason = %#v, want fallback unsupported reason", got)
	}
	if got := store.commitCalls[0].event.Details["reason"]; got != "unsupported_operation_recovery" {
		t.Fatalf("audit reason detail = %#v, want fallback unsupported reason", got)
	}
}

func TestOperationCoordinatorSkipsWaitNoopManualAndAutomaticRecoverPlans(t *testing.T) {
	now := recoveryTestNow()
	live := now.Add(time.Minute)
	reader := &fakeOperationRecoveryReader{
		records: []operations.OperationRecord{
			func() operations.OperationRecord {
				record := recoveryOperationRecord("op_wait", operations.OperationStateRunning, operations.OperationPhaseRepoCreateValidate, now)
				record.LeaseOwner = "worker-a"
				record.LeaseExpiresAt = &live
				return record
			}(),
			recoveryOperationRecord("op_noop", operations.OperationStateSucceeded, operations.OperationPhaseRepoCreateValidate, now),
			recoveryOperationRecord("op_manual", operations.OperationStateRunning, operations.OperationPhaseRepoCreateValidate, now),
			recoveryOperationRecord("op_operator", operations.OperationStateOperatorInterventionRequired, operations.OperationPhaseRepoCreateValidate, now),
		},
	}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
		recoveryOperationRecord("op_race", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
		recoveryOperationRecord("op_claim", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
	}}
	store := &fakeOperationLeaseStore{acquireErrors: map[string]error{"op_race": operations.ErrLeaseUnavailable}}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
		recoveryOperationRecord("op_cancel", operations.OperationStateCancelRequested, operations.OperationPhaseRepoCreateValidate, now),
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{unsupported: map[string]string{"op_cancel": "executor does not finalize"}}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
			recoveryOperationRecord("op_claim", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
			recoveryOperationRecord("op_after_error", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
		},
	}
	store := &fakeOperationLeaseStore{acquireErr: acquireErr}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
		recoveryOperationRecord("op_claim", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
		recoveryOperationRecord("op_after_error", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{err: executorErr}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
		recoveryOperationRecord("op_intervention", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{err: ErrOperationManualIntervention}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
		func() operations.OperationRecord {
			record := recoveryOperationRecord("op_cancel_live", operations.OperationStateCancelRequested, operations.OperationPhaseRepoCreateValidate, now)
			record.LeaseOwner = "worker-a"
			record.LeaseExpiresAt = &live
			return record
		}(),
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         10,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
		recoveryOperationRecord("op_one", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
		recoveryOperationRecord("op_two", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
		recoveryOperationRecord("op_three", operations.OperationStateQueued, operations.OperationPhaseRepoCreateValidate, now),
	}}
	store := &fakeOperationLeaseStore{}
	executor := &fakeOperationExecutor{}
	coordinator := NewOperationCoordinator(OperationConfig{
		Reader:        reader,
		LeaseStore:    store,
		CommitStore:   store,
		Executor:      executor,
		Owner:         "recovery-worker",
		LeaseDuration: time.Minute,
		Limit:         2,
		Now:           now,
		AuditEventID:  fakeOperationAuditEventID,
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
	updateCalls    []operationUpdateCall
	commitCalls    []operationCommitCall
}

type operationAcquireCall struct {
	operationID string
	request     operations.LeaseRequest
}

type operationUpdateCall struct {
	record operations.OperationRecord
	owner  string
	now    time.Time
}

type operationCommitCall struct {
	record operations.OperationRecord
	owner  string
	now    time.Time
	event  audit.Event
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
	return recoveryOperationRecord(operationID, operations.OperationStateRunning, operations.OperationPhaseRepoCreateValidate, recoveryTestNow()), nil
}

func (store *fakeOperationLeaseStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	store.renewCalls++
	return operations.OperationRecord{}, errors.New("unexpected renew call")
}

func (store *fakeOperationLeaseStore) UpdateOperationWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	operation := record.Record()
	store.updateCalls = append(store.updateCalls, operationUpdateCall{record: operation, owner: owner, now: now})
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	return operation, nil
}

func (store *fakeOperationLeaseStore) CommitOperationWithLease(_ context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	operation := record.Record()
	store.commitCalls = append(store.commitCalls, operationCommitCall{record: operation, owner: owner, now: now, event: event})
	operation.LeaseOwner = ""
	operation.LeaseExpiresAt = nil
	return operation, nil
}

func (store *fakeOperationLeaseStore) acquireOperationIDs() []string {
	out := make([]string, len(store.acquireCalls))
	for idx, call := range store.acquireCalls {
		out[idx] = call.operationID
	}
	return out
}

func (store *fakeOperationLeaseStore) updateOperationIDs() []string {
	out := make([]string, len(store.updateCalls))
	for idx, call := range store.updateCalls {
		out[idx] = call.record.ID
	}
	return out
}

func (store *fakeOperationLeaseStore) commitOperationIDs() []string {
	out := make([]string, len(store.commitCalls))
	for idx, call := range store.commitCalls {
		out[idx] = call.record.ID
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

func fakeOperationAuditEventID() string {
	return "evt_operation_recovery"
}

func recoveryOperationRecord(operationID string, state operations.OperationState, phase string, now time.Time) operations.OperationRecord {
	return operations.OperationRecord{
		ID:              operationID,
		Type:            operations.OperationRepoCreate,
		State:           state,
		Phase:           phase,
		CorrelationID:   "corr-alpha",
		CallerService:   "product-caller",
		AuthorizedActor: operations.Actor{Type: "system", ID: "svc-alpha"},
		Resource:        operations.ResourceRef{Type: "repo", ID: "repo_alpha01"},
		NamespaceID:     "ns_alpha01",
		RepoID:          "repo_alpha01",
		CreatedAt:       now.Add(-time.Hour),
	}
}
