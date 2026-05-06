package workloadmount

import (
	"context"
	"errors"
	"time"
)

type StaleLeaseStore interface {
	ListStaleNonTerminalWorkloadMountBindings(ctx context.Context, now time.Time, limit int) ([]Binding, error)
}

type StaleLeaseResult struct {
	Scanned     int
	KeptBlocked int
	Failed      int
}

type StaleLeaseReconciler struct {
	store StaleLeaseStore
	clock func() time.Time
	limit int
}

type StaleLeaseReconcilerConfig struct {
	Store StaleLeaseStore
	Clock func() time.Time
	Limit int
}

func NewStaleLeaseReconciler(config StaleLeaseReconcilerConfig) (*StaleLeaseReconciler, error) {
	if config.Store == nil {
		return nil, errors.New("workload mount stale lease store is required")
	}
	if config.Clock == nil {
		return nil, errors.New("workload mount stale lease clock is required")
	}
	if config.Limit <= 0 {
		return nil, errors.New("workload mount stale lease limit must be positive")
	}
	return &StaleLeaseReconciler{store: config.Store, clock: config.Clock, limit: config.Limit}, nil
}

func (reconciler *StaleLeaseReconciler) RunOnce(ctx context.Context) (StaleLeaseResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if reconciler == nil || reconciler.store == nil || reconciler.clock == nil || reconciler.limit <= 0 {
		return StaleLeaseResult{}, errors.New("workload mount stale lease reconciler is not configured")
	}
	now := reconciler.clock().UTC()
	if now.IsZero() {
		return StaleLeaseResult{}, errors.New("workload mount stale lease reconcile time must be set")
	}
	bindings, err := reconciler.store.ListStaleNonTerminalWorkloadMountBindings(ctx, now, reconciler.limit)
	if err != nil {
		return StaleLeaseResult{Failed: 1}, err
	}
	// Non-terminal expired bindings intentionally remain non-terminal. The
	// session gate treats them as stale blockers until an orchestrator terminal
	// observation or operator-owned recovery path provides evidence.
	return StaleLeaseResult{Scanned: len(bindings), KeptBlocked: len(bindings)}, nil
}
