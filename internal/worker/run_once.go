package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
)

type OperationRecoveryRunner interface {
	RunOnce(context.Context) (recovery.OperationBatchResult, error)
}

type AuditStaleRecoveryRunner interface {
	RunOnce(context.Context) (auditdelivery.StaleRecoveryResult, error)
}

type AuditDeliveryRunner interface {
	RunOnce(context.Context) (auditdelivery.BatchResult, error)
}

type Config struct {
	OperationRecovery  OperationRecoveryRunner
	AuditStaleRecovery AuditStaleRecoveryRunner
	AuditDelivery      AuditDeliveryRunner
}

type Runner struct {
	config Config
}

type Result struct {
	OperationRecovery  recovery.OperationBatchResult
	AuditStaleRecovery auditdelivery.StaleRecoveryResult
	AuditDelivery      auditdelivery.BatchResult
}

type Summary struct {
	Operation     OperationSummary     `json:"operation_recovery"`
	AuditStale    AuditStaleSummary    `json:"audit_stale_recovery"`
	AuditDelivery AuditDeliverySummary `json:"audit_delivery"`
}

type OperationSummary struct {
	Scanned     int `json:"scanned"`
	Claimed     int `json:"claimed"`
	Reclaimed   int `json:"reclaimed"`
	Finalized   int `json:"finalized"`
	Skipped     int `json:"skipped"`
	Manual      int `json:"manual"`
	Unsupported int `json:"unsupported"`
	RaceLost    int `json:"race_lost"`
	Failed      int `json:"failed"`
}

type AuditStaleSummary struct {
	Recovered      int `json:"recovered"`
	RetryWait      int `json:"retry_wait"`
	FailedTerminal int `json:"failed_terminal"`
	Failed         int `json:"failed"`
}

type AuditDeliverySummary struct {
	Claimed                  int `json:"claimed"`
	Delivered                int `json:"delivered"`
	DeliveryFailuresRecorded int `json:"delivery_failures_recorded"`
	Failed                   int `json:"failed"`
}

func New(config Config) Runner {
	return Runner{config: config}
}

func (runner Runner) RunOnce(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner.config.OperationRecovery == nil && runner.config.AuditStaleRecovery == nil && runner.config.AuditDelivery == nil {
		return Result{}, errors.New("worker run-once requires at least one runner")
	}

	var result Result
	var errs []error

	if runner.config.OperationRecovery != nil {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		operationResult, err := runner.config.OperationRecovery.RunOnce(ctx)
		result.OperationRecovery = operationResult
		if err != nil {
			errs = append(errs, fmt.Errorf("operation recovery: %w", err))
			if isContextError(err) {
				return result, errors.Join(errs...)
			}
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			return result, errors.Join(errs...)
		}
	}

	if runner.config.AuditStaleRecovery != nil {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		staleResult, err := runner.config.AuditStaleRecovery.RunOnce(ctx)
		result.AuditStaleRecovery = staleResult
		if err != nil {
			errs = append(errs, fmt.Errorf("audit stale recovery: %w", err))
			if isContextError(err) {
				return result, errors.Join(errs...)
			}
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			return result, errors.Join(errs...)
		}
	}

	if runner.config.AuditDelivery != nil {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		deliveryResult, err := runner.config.AuditDelivery.RunOnce(ctx)
		result.AuditDelivery = deliveryResult
		if err != nil {
			errs = append(errs, fmt.Errorf("audit delivery: %w", err))
			if isContextError(err) {
				return result, errors.Join(errs...)
			}
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			return result, errors.Join(errs...)
		}
	}

	return result, errors.Join(errs...)
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (result Result) Summary() Summary {
	return Summary{
		Operation: OperationSummary{
			Scanned:     result.OperationRecovery.Scanned,
			Claimed:     result.OperationRecovery.Claimed,
			Reclaimed:   result.OperationRecovery.Reclaimed,
			Finalized:   result.OperationRecovery.Finalized,
			Skipped:     result.OperationRecovery.Skipped,
			Manual:      result.OperationRecovery.Manual,
			Unsupported: result.OperationRecovery.Unsupported,
			RaceLost:    result.OperationRecovery.RaceLost,
			Failed:      result.OperationRecovery.Failed,
		},
		AuditStale: AuditStaleSummary{
			Recovered:      result.AuditStaleRecovery.Recovered,
			RetryWait:      result.AuditStaleRecovery.RetryWait,
			FailedTerminal: result.AuditStaleRecovery.FailedTerminal,
			Failed:         result.AuditStaleRecovery.Failed,
		},
		AuditDelivery: AuditDeliverySummary{
			Claimed:                  result.AuditDelivery.Claimed,
			Delivered:                result.AuditDelivery.Delivered,
			DeliveryFailuresRecorded: result.AuditDelivery.DeliveryFailuresRecorded,
			Failed:                   result.AuditDelivery.Failed,
		},
	}
}
