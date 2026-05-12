package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/inspection"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

type OperationConfig struct {
	Reader        store.OperationRecoveryReader
	LeaseStore    store.OperationLeaseStore
	CommitStore   store.OperationWorkerCommitStore
	Executor      OperationExecutor
	Owner         string
	LeaseDuration time.Duration
	Limit         int
	Now           time.Time
	Clock         func() time.Time
	AuditEventID  func() string
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

const (
	unsupportedOperationRecoveryReason = "unsupported_operation_recovery"
	operationRecoveryRequiredCode      = "OPERATION_RECOVERY_REQUIRED"
)

var (
	ErrOperationManualIntervention = errors.New("operation recovery committed operator intervention")
	ErrOperationLeaseRenewalFailed = errors.New("operation recovery lease renewal failed")
)

const minOperationLeaseRenewalInterval = time.Second

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
			supported, fatal := operationSupported(ctx, config, record, plan, &result, item)
			if fatal != nil {
				return result, fatal
			}
			if !supported {
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
			supported, fatal := operationSupported(ctx, config, record, plan, &result, item)
			if fatal != nil {
				return result, fatal
			}
			if !supported {
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
	if config.CommitStore == nil {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery commit store is required")
	}
	if config.Executor == nil {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery executor is required")
	}
	if config.AuditEventID == nil {
		return OperationConfig{}, time.Time{}, errors.New("operation recovery audit event id generator is required")
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

func operationSupported(ctx context.Context, config OperationConfig, record operations.OperationRecord, plan RecoveryPlan, result *OperationBatchResult, item OperationResult) (bool, error) {
	support := config.Executor.SupportsOperationRecovery(ctx, record, plan)
	if support.Supported {
		return true, nil
	}
	item.Outcome = OperationOutcomeUnsupported
	item.Reason = unsupportedReason(support)

	updated, err := acquireOperation(ctx, config, record.ID, operations.LeaseCancelPolicyNone)
	if err != nil {
		if fatal := recordAcquireError(result, item, err); fatal != nil {
			return false, fatal
		}
		return false, nil
	}

	evidence := unsupportedOperationEvidence(updated, plan)
	operation := unsupportedOperationUpdate(updated, item.Reason, evidence)
	event, err := unsupportedOperationAuditEvent(config, updated, item.Reason, evidence)
	if err != nil {
		if fatal := recordUnsupportedCommitError(result, item, err); fatal != nil {
			return false, fatal
		}
		return false, nil
	}
	if _, err := config.CommitStore.CommitOperationWithLease(ctx, operation.SanitizedForPersistence(), config.Owner, config.Now, event); err != nil {
		if fatal := recordUnsupportedCommitError(result, item, err); fatal != nil {
			return false, fatal
		}
		return false, nil
	}

	result.Unsupported++
	result.Results = append(result.Results, item)
	return false, nil
}

func unsupportedReason(support OperationSupport) string {
	if reason := strings.TrimSpace(support.Reason); reason != "" {
		return reason
	}
	return unsupportedOperationRecoveryReason
}

func unsupportedOperationUpdate(record operations.OperationRecord, reason string, evidence map[string]any) operations.OperationRecord {
	operation := record
	operation.State = operations.OperationStateOperatorInterventionRequired
	operation.Error = &operations.OperationError{
		Code:          operationRecoveryRequiredCode,
		Message:       "operation recovery is unsupported; operator intervention required",
		Retryable:     false,
		CorrelationID: record.CorrelationID,
		OperationID:   record.ID,
		Details: map[string]any{
			"reason":   reason,
			"evidence": evidence,
		},
	}
	operation.VerificationResult = map[string]any{
		"reason":   reason,
		"evidence": evidence,
	}
	return operation
}

func unsupportedOperationAuditEvent(config OperationConfig, record operations.OperationRecord, reason string, evidence map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(config.AuditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("operation recovery audit event id must be set")
	}
	eventType, ok := audit.EventTypeForOperationType(record.Type.String())
	if !ok {
		return audit.Event{}, fmt.Errorf("operation recovery audit event type for %q is not registered", record.Type.String())
	}
	return audit.NewEvent(audit.Event{
		EventID:       eventID,
		Type:          eventType,
		Time:          config.Now,
		CallerService: record.CallerService,
		AuthorizedActor: audit.Actor{
			Type: record.AuthorizedActor.Type,
			ID:   record.AuthorizedActor.ID,
		},
		CorrelationID: record.CorrelationID,
		OperationID:   record.ID,
		Resource: audit.Resource{
			Type:        record.Resource.Type,
			ID:          record.Resource.ID,
			NamespaceID: record.NamespaceID,
		},
		Outcome: audit.OutcomeFailed,
		Reason:  unsupportedOperationRecoveryReason,
		Details: map[string]any{
			"reason":   reason,
			"evidence": evidence,
		},
	}), nil
}

func unsupportedOperationEvidence(record operations.OperationRecord, plan RecoveryPlan) map[string]any {
	return map[string]any{
		"operation_type":  record.Type.String(),
		"phase":           record.Phase,
		"recovery_action": string(plan.Action),
		"recovery_reason": plan.Reason,
	}
}

func executeOperation(ctx context.Context, config OperationConfig, record operations.OperationRecord, plan RecoveryPlan, result *OperationBatchResult, item OperationResult) (bool, error) {
	execCtx, stopRenewal := startOperationLeaseRenewal(ctx, config, record.ID)
	err := config.Executor.ExecuteOperationRecovery(execCtx, record, plan)
	renewErr := stopRenewal()
	if renewErr != nil {
		err = errors.Join(err, renewErr)
		item.Outcome = OperationOutcomeFailed
		item.Error = err
		result.Failed++
		result.Results = append(result.Results, item)
		return false, fmt.Errorf("operation recovery renew %q: %w", item.OperationID, err)
	}
	if err != nil {
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

func startOperationLeaseRenewal(ctx context.Context, config OperationConfig, operationID string) (context.Context, func() error) {
	if ctx == nil {
		ctx = context.Background()
	}
	execCtx, cancel := context.WithCancel(ctx)
	stop := make(chan struct{})
	done := make(chan error, 1)
	interval := operationLeaseRenewalInterval(config.LeaseDuration)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				request := operations.LeaseRequest{
					Owner:    config.Owner,
					Duration: config.LeaseDuration,
					Now:      operationLeaseRenewalNow(config),
				}
				if _, err := config.LeaseStore.RenewOperationLease(execCtx, operationID, request); err != nil {
					if ctx.Err() != nil {
						done <- nil
						return
					}
					select {
					case <-stop:
						done <- nil
						return
					default:
					}
					cancel()
					done <- fmt.Errorf("%w: %s: %w", ErrOperationLeaseRenewalFailed, operationID, err)
					return
				}
			case <-stop:
				done <- nil
				return
			case <-ctx.Done():
				done <- nil
				return
			}
		}
	}()

	return execCtx, func() error {
		close(stop)
		cancel()
		return <-done
	}
}

func operationLeaseRenewalInterval(leaseDuration time.Duration) time.Duration {
	if leaseDuration <= 0 {
		return minOperationLeaseRenewalInterval
	}
	interval := leaseDuration / 3
	if interval < minOperationLeaseRenewalInterval {
		interval = minOperationLeaseRenewalInterval
	}
	latestSafeInterval := leaseDuration / 2
	if latestSafeInterval > 0 && interval > latestSafeInterval {
		interval = latestSafeInterval
	}
	if interval <= 0 {
		return time.Millisecond
	}
	return interval
}

func operationLeaseRenewalNow(config OperationConfig) time.Time {
	if config.Clock != nil {
		return config.Clock()
	}
	return time.Now().UTC()
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

func recordUnsupportedCommitError(result *OperationBatchResult, item OperationResult, err error) error {
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
	return fmt.Errorf("operation recovery unsupported commit %q: %w", item.OperationID, err)
}
