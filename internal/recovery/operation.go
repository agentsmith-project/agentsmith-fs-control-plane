package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/inspection"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

type OperationConfig struct {
	Reader        store.OperationRecoveryReader
	LeaseStore    store.OperationLeaseStore
	Executor      OperationExecutor
	Owner         string
	LeaseDuration time.Duration
	Limit         int
	Now           time.Time
	Clock         func() time.Time
}

type OperationExecutor interface {
	SupportsOperationRecovery(ctx context.Context, record operations.OperationRecord, plan RecoveryPlan) OperationSupport
	ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan RecoveryPlan) error
}

type OperationSupport struct {
	Supported bool
	Reason    string
}

type RecoveryPlan = inspection.RecoveryPlan
type RecoveryAction = inspection.RecoveryAction

const (
	RecoveryActionClaimable            = inspection.RecoveryActionClaimable
	RecoveryActionRetry                = inspection.RecoveryActionRetry
	RecoveryActionReclaim              = inspection.RecoveryActionReclaim
	RecoveryActionFinalizeCancellation = inspection.RecoveryActionFinalizeCancellation
)

type OperationCoordinator struct {
	config OperationConfig
}

type OperationBatchResult struct {
	Scanned     int
	Claimed     int
	Reclaimed   int
	Finalized   int
	Skipped     int
	Manual      int
	Unsupported int
	RaceLost    int
	Failed      int
	Results     []OperationResult
}

type OperationResult struct {
	OperationID string
	Action      inspection.RecoveryAction
	Outcome     OperationOutcome
	Reason      string
	Error       error
}

type OperationOutcome string

const (
	OperationOutcomeClaimed     OperationOutcome = "claimed"
	OperationOutcomeReclaimed   OperationOutcome = "reclaimed"
	OperationOutcomeFinalized   OperationOutcome = "finalized"
	OperationOutcomeSkipped     OperationOutcome = "skipped"
	OperationOutcomeManual      OperationOutcome = "manual"
	OperationOutcomeUnsupported OperationOutcome = "unsupported"
	OperationOutcomeRaceLost    OperationOutcome = "race_lost"
	OperationOutcomeFailed      OperationOutcome = "failed"
)

const unsupportedOperationRecoveryReason = "unsupported_operation_recovery"

var ErrOperationManualIntervention = errors.New("operation recovery committed operator intervention")

func NewOperationCoordinator(config OperationConfig) OperationCoordinator {
	return OperationCoordinator{config: config}
}

func (coordinator OperationCoordinator) RunOnce(ctx context.Context) (OperationBatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	config, now, err := coordinator.validatedConfig()
	if err != nil {
		return OperationBatchResult{}, err
	}

	records, err := config.Reader.ListOperationsForRecovery(ctx, now, config.Limit)
	if err != nil {
		return OperationBatchResult{}, err
	}

	var result OperationBatchResult
	result.Scanned = len(records)
	if result.Scanned > config.Limit {
		result.Scanned = config.Limit
	}
	for idx, record := range records {
		if idx >= config.Limit {
			break
		}

		plan := inspection.ClassifyOperationRecovery(record, inspection.RecoveryContext{Now: now})
		item := OperationResult{
			OperationID: record.ID,
			Action:      plan.Action,
			Reason:      plan.Reason,
		}

		switch plan.Action {
		case inspection.RecoveryActionClaimable, inspection.RecoveryActionRetry:
			item.Outcome = OperationOutcomeClaimed
			if !operationSupported(ctx, config, record, plan, &result, item) {
				continue
			}
			updated, err := acquireOperation(ctx, config, record.ID, operations.LeaseCancelPolicyNone)
			if err != nil {
				if fatal := recordAcquireError(&result, item, err); fatal != nil {
					return result, fatal
				}
				continue
			}
			countClaim, fatal := executeOperation(ctx, config, updated, plan, &result, item)
			if fatal != nil {
				return result, fatal
			}
			if countClaim {
				result.Claimed++
			} else {
				continue
			}
		case inspection.RecoveryActionReclaim:
			item.Outcome = OperationOutcomeReclaimed
			if !operationSupported(ctx, config, record, plan, &result, item) {
				continue
			}
			updated, err := acquireOperation(ctx, config, record.ID, operations.LeaseCancelPolicyNone)
			if err != nil {
				if fatal := recordAcquireError(&result, item, err); fatal != nil {
					return result, fatal
				}
				continue
			}
			countReclaim, fatal := executeOperation(ctx, config, updated, plan, &result, item)
			if fatal != nil {
				return result, fatal
			}
			if countReclaim {
				result.Reclaimed++
			} else {
				continue
			}
		case inspection.RecoveryActionFinalizeCancellation:
			item.Outcome = OperationOutcomeFinalized
			if _, err := acquireOperation(ctx, config, record.ID, operations.LeaseCancelPolicyFinalize); err != nil {
				if fatal := recordAcquireError(&result, item, err); fatal != nil {
					return result, fatal
				}
				continue
			}
			result.Finalized++
		case inspection.RecoveryActionWait, inspection.RecoveryActionNoop:
			item.Outcome = OperationOutcomeSkipped
			result.Skipped++
		case inspection.RecoveryActionManualIntervention, inspection.RecoveryActionRecover:
			item.Outcome = OperationOutcomeManual
			result.Manual++
		default:
			item.Outcome = OperationOutcomeManual
			result.Manual++
		}

		result.Results = append(result.Results, item)
	}

	return result, nil
}

func (coordinator OperationCoordinator) validatedConfig() (OperationConfig, time.Time, error) {
	config := coordinator.config
	if config.Reader == nil {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery reader is required")
	}
	if config.LeaseStore == nil {
		return OperationConfig{}, time.Time{}, errors.New("operation lease store is required")
	}
	if config.Executor == nil {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery executor is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery owner is required")
	}
	if config.LeaseDuration <= 0 {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery lease duration must be positive")
	}
	if config.Limit <= 0 {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery limit must be positive")
	}

	now := config.Now
	if now.IsZero() && config.Clock != nil {
		now = config.Clock()
	}
	if now.IsZero() {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery time must be set")
	}
	config.Now = now
	return config, now, nil
}

func acquireOperation(ctx context.Context, config OperationConfig, operationID string, cancelPolicy operations.LeaseCancelPolicy) (operations.OperationRecord, error) {
	return config.LeaseStore.AcquireOperationLease(ctx, operationID, operations.LeaseRequest{
		Owner:        config.Owner,
		Duration:     config.LeaseDuration,
		Now:          config.Now,
		CancelPolicy: cancelPolicy,
	})
}

func operationSupported(ctx context.Context, config OperationConfig, record operations.OperationRecord, plan RecoveryPlan, result *OperationBatchResult, item OperationResult) bool {
	support := config.Executor.SupportsOperationRecovery(ctx, record, plan)
	if support.Supported {
		return true
	}
	item.Outcome = OperationOutcomeUnsupported
	if reason := strings.TrimSpace(support.Reason); reason != "" {
		item.Reason = reason
	} else {
		item.Reason = unsupportedOperationRecoveryReason
	}
	result.Unsupported++
	result.Results = append(result.Results, item)
	return false
}

func executeOperation(ctx context.Context, config OperationConfig, record operations.OperationRecord, plan RecoveryPlan, result *OperationBatchResult, item OperationResult) (bool, error) {
	if err := config.Executor.ExecuteOperationRecovery(ctx, record, plan); err != nil {
		if errors.Is(err, ErrOperationManualIntervention) {
			item.Outcome = OperationOutcomeManual
			item.Error = err
			result.Manual++
			result.Results = append(result.Results, item)
			return false, nil
		}
		item.Outcome = OperationOutcomeFailed
		item.Error = err
		result.Failed++
		result.Results = append(result.Results, item)
		return false, fmt.Errorf("operation recovery execute %q: %w", item.OperationID, err)
	}
	return true, nil
}

func recordAcquireError(result *OperationBatchResult, item OperationResult, err error) error {
	item.Error = err
	if errors.Is(err, operations.ErrLeaseUnavailable) {
		item.Outcome = OperationOutcomeRaceLost
		result.RaceLost++
		result.Results = append(result.Results, item)
		return nil
	}

	item.Outcome = OperationOutcomeFailed
	result.Failed++
	result.Results = append(result.Results, item)
	return fmt.Errorf("operation recovery acquire %q: %w", item.OperationID, err)
}
