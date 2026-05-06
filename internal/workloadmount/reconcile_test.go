package workloadmount

import (
	"context"
	"testing"
	"time"
)

func TestStaleLeaseReconcilerScansAndKeepsNonTerminalBindingsBlocked(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	store := &fakeStaleLeaseStore{bindings: []Binding{{ID: "wmb_123"}}}
	reconciler, err := NewStaleLeaseReconciler(StaleLeaseReconcilerConfig{
		Store: store,
		Clock: func() time.Time { return now },
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("NewStaleLeaseReconciler: %v", err)
	}

	result, err := reconciler.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Scanned != 1 || result.KeptBlocked != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v, want scanned/kept blocked without terminal mutation", result)
	}
	if !store.now.Equal(now) || store.limit != 10 {
		t.Fatalf("scan args now/limit = %v/%d, want %v/10", store.now, store.limit, now)
	}
}

type fakeStaleLeaseStore struct {
	bindings []Binding
	now      time.Time
	limit    int
}

func (store *fakeStaleLeaseStore) ListStaleNonTerminalWorkloadMountBindings(_ context.Context, now time.Time, limit int) ([]Binding, error) {
	store.now = now
	store.limit = limit
	return store.bindings, nil
}
