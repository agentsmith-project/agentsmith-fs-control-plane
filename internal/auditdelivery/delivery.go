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

type Config struct {
	Store        store.AuditOutboxDeliveryStore
	Deliverer    Deliverer
	Owner        string
	Limit        int
	MaxAttempts  int
	RetryBackoff time.Duration
	Now          time.Time
	Clock        func() time.Time
}

type Deliverer interface {
	DeliverAuditOutboxRecord(ctx context.Context, record audit.OutboxRecord) error
}

type Coordinator struct {
	config Config
}

type BatchResult struct {
	Claimed                  int
	Delivered                int
	DeliveryFailuresRecorded int
	Failed                   int
	Results                  []RecordResult
}

type RecordResult struct {
	EventID string
	Outcome Outcome
	Reason  string
	Error   error
}

type Outcome string

const (
	OutcomeDelivered               Outcome = "delivered"
	OutcomeDeliveryFailureRecorded Outcome = "delivery_failure_recorded"
	OutcomeFailed                  Outcome = "failed"
)

const reasonMarkDeliveredAfterDeliveryFailed = "mark_delivered_after_delivery_failed"

func NewCoordinator(config Config) Coordinator {
	return Coordinator{config: config}
}

func (coordinator Coordinator) RunOnce(ctx context.Context) (BatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	config, now, err := coordinator.validatedConfig()
	if err != nil {
		return BatchResult{}, err
	}

	records, err := config.Store.ClaimDueAuditOutboxRecords(ctx, config.Owner, now, config.Limit)
	if err != nil {
		return BatchResult{}, err
	}

	result := BatchResult{Claimed: len(records)}
	for _, record := range records {
		item := RecordResult{EventID: record.EventID}
		if err := config.Deliverer.DeliverAuditOutboxRecord(ctx, record); err != nil {
			redacted := audit.RedactString(err.Error())
			item.Outcome = OutcomeDeliveryFailureRecorded
			item.Reason = redacted
			failure := audit.DeliveryFailure{
				MaxAttempts: config.MaxAttempts,
				Backoff:     config.RetryBackoff,
				LastError:   redacted,
				Now:         now,
			}
			if markErr := config.Store.MarkAuditOutboxDeliveryFailed(ctx, record.EventID, failure); markErr != nil {
				item.Outcome = OutcomeFailed
				item.Error = markErr
				result.Failed++
				result.Results = append(result.Results, item)
				return result, fmt.Errorf("mark audit outbox %q delivery failed: %w", record.EventID, markErr)
			}
			result.DeliveryFailuresRecorded++
			result.Results = append(result.Results, item)
			continue
		}

		item.Outcome = OutcomeDelivered
		if err := config.Store.MarkAuditOutboxDelivered(ctx, record.EventID, now); err != nil {
			item.Outcome = OutcomeFailed
			item.Reason = reasonMarkDeliveredAfterDeliveryFailed
			item.Error = err
			result.Failed++
			result.Results = append(result.Results, item)
			return result, fmt.Errorf("mark audit outbox %q delivered: %w", record.EventID, err)
		}
		result.Delivered++
		result.Results = append(result.Results, item)
	}

	return result, nil
}

func (coordinator Coordinator) validatedConfig() (Config, time.Time, error) {
	config := coordinator.config
	if config.Store == nil {
		return Config{}, time.Time{}, errors.New("audit outbox delivery store is required")
	}
	if config.Deliverer == nil {
		return Config{}, time.Time{}, errors.New("audit outbox deliverer is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return Config{}, time.Time{}, errors.New("audit outbox delivery owner is required")
	}
	if config.Limit <= 0 {
		return Config{}, time.Time{}, errors.New("audit outbox delivery limit must be positive")
	}
	if config.MaxAttempts <= 0 {
		return Config{}, time.Time{}, errors.New("audit outbox delivery max attempts must be positive")
	}
	if config.RetryBackoff < 0 {
		return Config{}, time.Time{}, errors.New("audit outbox delivery retry backoff cannot be negative")
	}

	now := config.Now
	if now.IsZero() && config.Clock != nil {
		now = config.Clock()
	}
	if now.IsZero() {
		return Config{}, time.Time{}, errors.New("audit outbox delivery time must be set")
	}
	config.Now = now
	return config, now, nil
}
