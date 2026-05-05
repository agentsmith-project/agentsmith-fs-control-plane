package auditdelivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
)

func TestCoordinatorRejectsInvalidConfigBeforeStoreOrDelivererCalls(t *testing.T) {
	now := deliveryTestNow()
	tests := []struct {
		name   string
		config Config
	}{
		{name: "nil store", config: Config{Deliverer: &fakeDeliverer{}, Owner: "audit-deliverer", Limit: 1, MaxAttempts: 3, Now: now}},
		{name: "nil deliverer", config: Config{Store: &fakeAuditOutboxDeliveryStore{}, Owner: "audit-deliverer", Limit: 1, MaxAttempts: 3, Now: now}},
		{name: "blank owner", config: Config{Store: &fakeAuditOutboxDeliveryStore{}, Deliverer: &fakeDeliverer{}, Owner: " \t", Limit: 1, MaxAttempts: 3, Now: now}},
		{name: "non-positive limit", config: Config{Store: &fakeAuditOutboxDeliveryStore{}, Deliverer: &fakeDeliverer{}, Owner: "audit-deliverer", MaxAttempts: 3, Now: now}},
		{name: "non-positive max attempts", config: Config{Store: &fakeAuditOutboxDeliveryStore{}, Deliverer: &fakeDeliverer{}, Owner: "audit-deliverer", Limit: 1, Now: now}},
		{name: "negative backoff", config: Config{Store: &fakeAuditOutboxDeliveryStore{}, Deliverer: &fakeDeliverer{}, Owner: "audit-deliverer", Limit: 1, MaxAttempts: 3, RetryBackoff: -time.Second, Now: now}},
		{name: "zero now", config: Config{Store: &fakeAuditOutboxDeliveryStore{}, Deliverer: &fakeDeliverer{}, Owner: "audit-deliverer", Limit: 1, MaxAttempts: 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := tt.config.Store.(*fakeAuditOutboxDeliveryStore)
			deliverer, _ := tt.config.Deliverer.(*fakeDeliverer)

			result, err := NewCoordinator(tt.config).RunOnce(context.Background())
			if err == nil {
				t.Fatal("RunOnce succeeded, want invalid config error")
			}
			if result.Claimed != 0 {
				t.Fatalf("Claimed = %d, want 0", result.Claimed)
			}
			if store != nil && store.claimCalls != 0 {
				t.Fatalf("claim calls = %d, want 0", store.claimCalls)
			}
			if deliverer != nil && len(deliverer.calls) != 0 {
				t.Fatalf("deliverer calls = %d, want 0", len(deliverer.calls))
			}
		})
	}
}

func TestCoordinatorClaimsWithContextOwnerNowLimitAndDoesNothingWhenEmpty(t *testing.T) {
	ctx := context.WithValue(context.Background(), deliveryContextKey("ctx"), "sentinel")
	now := deliveryTestNow()
	store := &fakeAuditOutboxDeliveryStore{}
	deliverer := &fakeDeliverer{}
	coordinator := NewCoordinator(Config{
		Store:        store,
		Deliverer:    deliverer,
		Owner:        "audit-deliverer",
		Limit:        7,
		MaxAttempts:  3,
		RetryBackoff: time.Minute,
		Now:          now,
	})

	result, err := coordinator.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Claimed != 0 || len(deliverer.calls) != 0 || len(store.markDeliveredCalls) != 0 || len(store.markFailedCalls) != 0 {
		t.Fatalf("result/deliver/marks = %#v/%d/%d/%d, want no delivery work", result, len(deliverer.calls), len(store.markDeliveredCalls), len(store.markFailedCalls))
	}
	if store.claimContext != ctx || store.claimOwner != "audit-deliverer" || !store.claimNow.Equal(now) || store.claimLimit != 7 {
		t.Fatalf("claim args ctx/owner/now/limit = %v/%q/%v/%d", store.claimContext == ctx, store.claimOwner, store.claimNow, store.claimLimit)
	}
}

func TestCoordinatorClaimErrorDoesNotDeliverOrMark(t *testing.T) {
	now := deliveryTestNow()
	claimErr := errors.New("claim failed")
	store := &fakeAuditOutboxDeliveryStore{claimErr: claimErr}
	deliverer := &fakeDeliverer{}
	coordinator := validCoordinator(store, deliverer, now)

	result, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, claimErr) {
		t.Fatalf("RunOnce error = %v, want claim error", err)
	}
	if result.Claimed != 0 || len(result.Results) != 0 {
		t.Fatalf("result = %#v, want empty result", result)
	}
	if len(deliverer.calls) != 0 || len(store.markDeliveredCalls) != 0 || len(store.markFailedCalls) != 0 {
		t.Fatalf("deliver/marks = %d/%d/%d, want none", len(deliverer.calls), len(store.markDeliveredCalls), len(store.markFailedCalls))
	}
}

func TestCoordinatorDeliversAndMarksDelivered(t *testing.T) {
	ctx := context.WithValue(context.Background(), deliveryContextKey("ctx"), "sentinel")
	now := deliveryTestNow()
	record := auditRecord("audit-1", now)
	store := &fakeAuditOutboxDeliveryStore{claimed: []audit.OutboxRecord{record}}
	deliverer := &fakeDeliverer{}
	coordinator := validCoordinator(store, deliverer, now)

	result, err := coordinator.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Claimed != 1 || result.Delivered != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v, want claimed 1 delivered 1", result)
	}
	if len(deliverer.calls) != 1 || deliverer.calls[0].ctx != ctx || deliverer.calls[0].record.EventID != "audit-1" {
		t.Fatalf("deliverer calls = %#v", deliverer.calls)
	}
	if len(store.markDeliveredCalls) != 1 || store.markDeliveredCalls[0].ctx != ctx || store.markDeliveredCalls[0].eventID != "audit-1" || !store.markDeliveredCalls[0].now.Equal(now) {
		t.Fatalf("mark delivered calls = %#v", store.markDeliveredCalls)
	}
	if len(store.markFailedCalls) != 0 {
		t.Fatalf("mark failed calls = %#v, want none", store.markFailedCalls)
	}
}

func TestCoordinatorRecordsDeliveryFailureAndContinues(t *testing.T) {
	ctx := context.WithValue(context.Background(), deliveryContextKey("ctx"), "sentinel")
	now := deliveryTestNow()
	store := &fakeAuditOutboxDeliveryStore{claimed: []audit.OutboxRecord{
		auditRecord("audit-fail", now),
		auditRecord("audit-ok", now),
	}}
	deliverer := &fakeDeliverer{errors: map[string]error{
		"audit-fail": errors.New("post failed token=delivery-secret"),
	}}
	coordinator := validCoordinator(store, deliverer, now)

	result, err := coordinator.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Claimed != 2 || result.Delivered != 1 || result.DeliveryFailuresRecorded != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v, want one recorded failure and one delivery", result)
	}
	if len(store.markFailedCalls) != 1 {
		t.Fatalf("mark failed calls = %#v, want one", store.markFailedCalls)
	}
	if store.markFailedCalls[0].ctx != ctx {
		t.Fatal("mark failed did not receive request context")
	}
	failure := store.markFailedCalls[0].failure
	if failure.MaxAttempts != 5 || failure.Backoff != 2*time.Minute || !failure.Now.Equal(now) {
		t.Fatalf("failure args = %#v", failure)
	}
	if strings.Contains(failure.LastError, "delivery-secret") || !strings.Contains(failure.LastError, "[REDACTED]") {
		t.Fatalf("failure LastError = %q, want redacted", failure.LastError)
	}
	if len(result.Results) == 0 || strings.Contains(result.Results[0].Reason, "delivery-secret") || !strings.Contains(result.Results[0].Reason, "[REDACTED]") {
		t.Fatalf("result failure reason = %#v, want redacted", result.Results)
	}
	if gotIDs := deliverer.eventIDs(); strings.Join(gotIDs, ",") != "audit-fail,audit-ok" {
		t.Fatalf("deliver order = %#v", gotIDs)
	}
}

func TestCoordinatorMarkDeliveredFailureIsFatalAndDoesNotMarkFailed(t *testing.T) {
	now := deliveryTestNow()
	markErr := errors.New("mark delivered failed")
	store := &fakeAuditOutboxDeliveryStore{
		claimed:          []audit.OutboxRecord{auditRecord("audit-1", now), auditRecord("audit-2", now)},
		markDeliveredErr: markErr,
	}
	deliverer := &fakeDeliverer{}
	coordinator := validCoordinator(store, deliverer, now)

	result, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, markErr) {
		t.Fatalf("RunOnce error = %v, want mark delivered error", err)
	}
	if result.Claimed != 2 || result.Failed != 1 || result.Delivered != 0 || len(store.markFailedCalls) != 0 || len(deliverer.calls) != 1 {
		t.Fatalf("result/failed/deliver = %#v/%d/%d, want fatal partial", result, len(store.markFailedCalls), len(deliverer.calls))
	}
	if len(result.Results) != 1 || result.Results[0].Reason != "mark_delivered_after_delivery_failed" {
		t.Fatalf("result = %#v, want stable mark delivered failure reason", result)
	}
}

func TestCoordinatorMarkFailedFailureIsFatalAndStops(t *testing.T) {
	now := deliveryTestNow()
	markErr := errors.New("mark failed failed")
	store := &fakeAuditOutboxDeliveryStore{
		claimed:       []audit.OutboxRecord{auditRecord("audit-fail", now), auditRecord("audit-after", now)},
		markFailedErr: markErr,
	}
	deliverer := &fakeDeliverer{err: errors.New("delivery failed")}
	coordinator := validCoordinator(store, deliverer, now)

	result, err := coordinator.RunOnce(context.Background())
	if !errors.Is(err, markErr) {
		t.Fatalf("RunOnce error = %v, want mark failed error", err)
	}
	if result.Claimed != 2 || result.Failed != 1 || result.DeliveryFailuresRecorded != 0 || len(deliverer.calls) != 1 {
		t.Fatalf("result/deliver = %#v/%d, want fatal after first mark failed", result, len(deliverer.calls))
	}
}

type deliveryContextKey string

type fakeAuditOutboxDeliveryStore struct {
	claimed  []audit.OutboxRecord
	claimErr error

	claimCalls   int
	claimContext context.Context
	claimOwner   string
	claimNow     time.Time
	claimLimit   int

	markDeliveredCalls []markDeliveredCall
	markDeliveredErr   error
	markFailedCalls    []markFailedCall
	markFailedErr      error
}

type markDeliveredCall struct {
	ctx     context.Context
	eventID string
	now     time.Time
}

type markFailedCall struct {
	ctx     context.Context
	eventID string
	failure audit.DeliveryFailure
}

func (store *fakeAuditOutboxDeliveryStore) ListDueAuditOutboxRecords(context.Context, time.Time, int) ([]audit.OutboxRecord, error) {
	return nil, errors.New("ListDueAuditOutboxRecords must not be used by delivery coordinator")
}

func (store *fakeAuditOutboxDeliveryStore) ClaimDueAuditOutboxRecords(ctx context.Context, owner string, now time.Time, limit int) ([]audit.OutboxRecord, error) {
	store.claimCalls++
	store.claimContext = ctx
	store.claimOwner = owner
	store.claimNow = now
	store.claimLimit = limit
	if store.claimErr != nil {
		return nil, store.claimErr
	}
	out := make([]audit.OutboxRecord, len(store.claimed))
	copy(out, store.claimed)
	return out, nil
}

func (store *fakeAuditOutboxDeliveryStore) MarkAuditOutboxDelivered(ctx context.Context, eventID string, now time.Time) error {
	store.markDeliveredCalls = append(store.markDeliveredCalls, markDeliveredCall{ctx: ctx, eventID: eventID, now: now})
	return store.markDeliveredErr
}

func (store *fakeAuditOutboxDeliveryStore) MarkAuditOutboxDeliveryFailed(ctx context.Context, eventID string, failure audit.DeliveryFailure) error {
	store.markFailedCalls = append(store.markFailedCalls, markFailedCall{ctx: ctx, eventID: eventID, failure: failure})
	return store.markFailedErr
}

type fakeDeliverer struct {
	calls  []deliverCall
	err    error
	errors map[string]error
}

type deliverCall struct {
	ctx    context.Context
	record audit.OutboxRecord
}

func (deliverer *fakeDeliverer) DeliverAuditOutboxRecord(ctx context.Context, record audit.OutboxRecord) error {
	deliverer.calls = append(deliverer.calls, deliverCall{ctx: ctx, record: record})
	if err := deliverer.errors[record.EventID]; err != nil {
		return err
	}
	return deliverer.err
}

func (deliverer *fakeDeliverer) eventIDs() []string {
	out := make([]string, len(deliverer.calls))
	for idx, call := range deliverer.calls {
		out[idx] = call.record.EventID
	}
	return out
}

func validCoordinator(store *fakeAuditOutboxDeliveryStore, deliverer *fakeDeliverer, now time.Time) Coordinator {
	return NewCoordinator(Config{
		Store:        store,
		Deliverer:    deliverer,
		Owner:        "audit-deliverer",
		Limit:        10,
		MaxAttempts:  5,
		RetryBackoff: 2 * time.Minute,
		Now:          now,
	})
}

func auditRecord(eventID string, now time.Time) audit.OutboxRecord {
	return audit.OutboxRecord{
		EventID:         eventID,
		EventType:       audit.EventTypeExportCreate,
		EventTime:       now.Add(-time.Minute),
		PayloadJSON:     []byte(`{"event_id":"` + eventID + `"}`),
		Status:          audit.OutboxStatusDelivering,
		DeliveryAttempt: 1,
		CreatedAt:       now.Add(-time.Minute),
		UpdatedAt:       now,
	}
}

func deliveryTestNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}
