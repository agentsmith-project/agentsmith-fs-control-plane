package auditdelivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

const staleDeliveringRecoveryReason = "stale_delivering_recovered_for_replay"

type StaleRecoveryConfig struct {
	Store          store.AuditOutboxDeliveryStore
	Owner          string
	StaleThreshold time.Duration
	Limit          int
	MaxAttempts    int
	RetryBackoff   time.Duration
	Now            time.Time
	Clock          func() time.Time
}

type StaleRecoveryCoordinator struct {
	config StaleRecoveryConfig
}

type StaleRecoveryResult struct {
	Recovered      int
	RetryWait      int
	FailedTerminal int
	Failed         int
	Records        []audit.OutboxRecord
}

func NewStaleRecoveryCoordinator(config StaleRecoveryConfig) StaleRecoveryCoordinator {
	return StaleRecoveryCoordinator{config: config}
}

func (coordinator StaleRecoveryCoordinator) RunOnce(ctx context.Context) (StaleRecoveryResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config, now, err := coordinator.validatedConfig()
	if err != nil {
		return StaleRecoveryResult{}, err
	}

	records, err := config.Store.RecoverStaleAuditOutboxRecords(ctx, config.Owner, config.StaleThreshold, config.Limit, audit.DeliveryFailure{
		MaxAttempts: config.MaxAttempts,
		Backoff:     config.RetryBackoff,
		LastError:   staleDeliveringRecoveryReason,
		Now:         now,
	})
	if err != nil {
		return StaleRecoveryResult{Failed: 1}, fmt.Errorf("recover stale audit outbox records: %w", err)
	}

	result := StaleRecoveryResult{Recovered: len(records), Records: records}
	for _, record := range records {
		switch record.Status {
		case audit.OutboxStatusRetryWait:
			result.RetryWait++
		case audit.OutboxStatusFailed:
			result.FailedTerminal++
		}
	}
	return result, nil
}

func (coordinator StaleRecoveryCoordinator) validatedConfig() (StaleRecoveryConfig, time.Time, error) {
	config := coordinator.config
	if config.Store == nil {
		return StaleRecoveryConfig{}, time.Time{}, errors.New("audit stale delivery store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return StaleRecoveryConfig{}, time.Time{}, errors.New("audit stale delivery owner is required")
	}
	if config.StaleThreshold <= 0 {
		return StaleRecoveryConfig{}, time.Time{}, errors.New("audit stale delivery threshold must be positive")
	}
	if config.Limit <= 0 {
		return StaleRecoveryConfig{}, time.Time{}, errors.New("audit stale delivery limit must be positive")
	}
	if config.MaxAttempts <= 0 {
		return StaleRecoveryConfig{}, time.Time{}, errors.New("audit stale delivery max attempts must be positive")
	}
	if config.RetryBackoff < 0 {
		return StaleRecoveryConfig{}, time.Time{}, errors.New("audit stale delivery retry backoff cannot be negative")
	}

	now := config.Now
	if now.IsZero() && config.Clock != nil {
		now = config.Clock()
	}
	if now.IsZero() {
		return StaleRecoveryConfig{}, time.Time{}, errors.New("audit stale delivery time must be set")
	}
	config.Now = now
	return config, now, nil
}
