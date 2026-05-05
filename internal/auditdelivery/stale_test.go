package auditdelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
)

func TestStaleRecoveryCoordinatorRejectsInvalidConfigBeforeStoreCalls(t *testing.T) {
	now := deliveryTestNow()
	tests := []struct {
		name   string
		config StaleRecoveryConfig
	}{
		{name: "nil store", config: StaleRecoveryConfig{Owner: "audit-deliverer", StaleThreshold: time.Minute, Limit: 1, MaxAttempts: 3, Now: now}},
		{name: "blank owner", config: StaleRecoveryConfig{Store: &fakeAuditOutboxDeliveryStore{}, Owner: " ", StaleThreshold: time.Minute, Limit: 1, MaxAttempts: 3, Now: now}},
		{name: "zero stale threshold", config: StaleRecoveryConfig{Store: &fakeAuditOutboxDeliveryStore{}, Owner: "audit-deliverer", Limit: 1, MaxAttempts: 3, Now: now}},
		{name: "zero limit", config: StaleRecoveryConfig{Store: &fakeAuditOutboxDeliveryStore{}, Owner: "audit-deliverer", StaleThreshold: time.Minute, MaxAttempts: 3, Now: now}},
		{name: "zero max attempts", config: StaleRecoveryConfig{Store: &fakeAuditOutboxDeliveryStore{}, Owner: "audit-deliverer", StaleThreshold: time.Minute, Limit: 1, Now: now}},
		{name: "negative backoff", config: StaleRecoveryConfig{Store: &fakeAuditOutboxDeliveryStore{}, Owner: "audit-deliverer", StaleThreshold: time.Minute, Limit: 1, MaxAttempts: 3, RetryBackoff: -time.Second, Now: now}},
		{name: "zero now", config: StaleRecoveryConfig{Store: &fakeAuditOutboxDeliveryStore{}, Owner: "audit-deliverer", StaleThreshold: time.Minute, Limit: 1, MaxAttempts: 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := tt.config.Store.(*fakeAuditOutboxDeliveryStore)
			result, err := NewStaleRecoveryCoordinator(tt.config).RunOnce(context.Background())
			if err == nil {
				t.Fatal("RunOnce succeeded, want invalid config error")
			}
			if result.Recovered != 0 {
				t.Fatalf("result = %#v, want empty", result)
			}
			if store != nil && store.recoverStaleCalls != 0 {
				t.Fatalf("recover stale calls = %d, want 0", store.recoverStaleCalls)
			}
		})
	}
}

func TestStaleRecoveryCoordinatorPassesConfigAndCountsRecoveredRecords(t *testing.T) {
	ctx := context.WithValue(context.Background(), deliveryContextKey("ctx"), "sentinel")
	now := deliveryTestNow()
	store := &fakeAuditOutboxDeliveryStore{recoveredStale: []audit.OutboxRecord{
		staleRecoveredRecord("audit-retry", audit.OutboxStatusRetryWait, now),
		staleRecoveredRecord("audit-failed", audit.OutboxStatusFailed, now),
	}}
	coordinator := NewStaleRecoveryCoordinator(StaleRecoveryConfig{
		Store:          store,
		Owner:          "audit-deliverer",
		StaleThreshold: 10 * time.Minute,
		Limit:          7,
		MaxAttempts:    5,
		RetryBackoff:   2 * time.Minute,
		Now:            now,
	})

	result, err := coordinator.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Recovered != 2 || result.RetryWait != 1 || result.FailedTerminal != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v, want recovered retry and failed counts", result)
	}
	if store.recoverStaleContext != ctx ||
		store.recoverStaleOwner != "audit-deliverer" ||
		store.recoverStaleThreshold != 10*time.Minute ||
		store.recoverStaleLimit != 7 {
		t.Fatalf("recover stale call = ctx %v owner %q threshold %v limit %d", store.recoverStaleContext == ctx, store.recoverStaleOwner, store.recoverStaleThreshold, store.recoverStaleLimit)
	}
	failure := store.recoverStaleFailure
	if failure.MaxAttempts != 5 || failure.Backoff != 2*time.Minute || !failure.Now.Equal(now) || failure.LastError != "stale_delivering_recovered_for_replay" {
		t.Fatalf("failure = %#v, want configured failure with stable reason", failure)
	}
	if store.claimCalls != 0 {
		t.Fatalf("claim due calls = %d, want 0", store.claimCalls)
	}
}

func TestStaleRecoveryCoordinatorStoreErrorIsFatal(t *testing.T) {
	now := deliveryTestNow()
	storeErr := errors.New("recover stale failed")
	store := &fakeAuditOutboxDeliveryStore{recoverStaleErr: storeErr}
	coordinator := NewStaleRecoveryCoordinator(StaleRecoveryConfig{
		Store:          store,
		Owner:          "audit-deliverer",
		StaleThreshold: time.Minute,
		Limit:          10,
		MaxAttempts:    3,
		Now:            now,
	})

	result, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, storeErr) {
		t.Fatalf("RunOnce error = %v, want store error", err)
	}
	if result.Failed != 1 || result.Recovered != 0 {
		t.Fatalf("result = %#v, want fatal empty recovery", result)
	}
}

func staleRecoveredRecord(eventID string, status audit.OutboxStatus, now time.Time) audit.OutboxRecord {
	record := auditRecord(eventID, now)
	record.Status = status
	record.LastError = "stale_delivering_recovered_for_replay"
	if status == audit.OutboxStatusRetryWait {
		nextRetryAt := now.Add(time.Minute)
		record.NextRetryAt = &nextRetryAt
	}
	return record
}
