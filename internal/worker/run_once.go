package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportreconcile"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restorereconcile"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

type ExportSessionReconcileRunner interface {
	RunOnce(context.Context) (exportreconcile.Result, error)
}

type OperationRecoveryRunner interface {
	RunOnce(context.Context) (recovery.OperationBatchResult, error)
}

type WorkloadMountStaleLeaseRunner interface {
	RunOnce(context.Context) (workloadmount.StaleLeaseResult, error)
}

type RestoreReconciliationRunner interface {
	RunOnce(context.Context) (restorereconcile.Result, error)
}

type AuditStaleRecoveryRunner interface {
	RunOnce(context.Context) (auditdelivery.StaleRecoveryResult, error)
}

type AuditDeliveryRunner interface {
	RunOnce(context.Context) (auditdelivery.BatchResult, error)
}

type Config struct {
	ExportSessionReconcile ExportSessionReconcileRunner
	OperationRecovery      OperationRecoveryRunner
	WorkloadMountStale     WorkloadMountStaleLeaseRunner
	RestoreReconciliation  RestoreReconciliationRunner
	AuditStaleRecovery     AuditStaleRecoveryRunner
	AuditDelivery          AuditDeliveryRunner
}

type Runner struct {
	config Config
}

type Result struct {
	ExportSessionReconcile exportreconcile.Result
	OperationRecovery      recovery.OperationBatchResult
	WorkloadMountStale     workloadmount.StaleLeaseResult
	RestoreReconciliation  restorereconcile.Result
	AuditStaleRecovery     auditdelivery.StaleRecoveryResult
	AuditDelivery          auditdelivery.BatchResult
}

type Summary struct {
	ExportSessionReconcile ExportSessionReconcileSummary `json:"export_session_reconcile"`
	Operation              OperationSummary              `json:"operation_recovery"`
	WorkloadMountStale     WorkloadMountStaleSummary     `json:"workload_mount_stale_lease_scan"`
	RestoreReconciliation  RestoreReconciliationSummary  `json:"restore_reconciliation"`
	AuditStale             AuditStaleSummary             `json:"audit_stale_recovery"`
	AuditDelivery          AuditDeliverySummary          `json:"audit_delivery"`
}

type ExportSessionReconcileSummary struct {
	RecoveredRuntimeRequests int `json:"recovered_runtime_requests"`
	RecoveredRuntimeWrites   int `json:"recovered_runtime_writes"`
	Scanned                  int `json:"scanned"`
	Terminalized             int `json:"terminalized"`
	Reused                   int `json:"reused"`
	Skipped                  int `json:"skipped"`
	RaceLost                 int `json:"race_lost"`
	Failed                   int `json:"failed"`
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

type WorkloadMountStaleSummary struct {
	Scanned     int `json:"scanned"`
	KeptBlocked int `json:"kept_blocked"`
	Failed      int `json:"failed"`
}

type RestoreReconciliationSummary struct {
	Scanned   int `json:"scanned"`
	Completed int `json:"completed"`
	Blocked   int `json:"blocked"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
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
	if runner.config.ExportSessionReconcile == nil && runner.config.OperationRecovery == nil && runner.config.WorkloadMountStale == nil && runner.config.RestoreReconciliation == nil && runner.config.AuditStaleRecovery == nil && runner.config.AuditDelivery == nil {
		return Result{}, errors.New("worker run-once requires at least one runner")
	}

	var result Result
	var errs []error

	if runner.config.ExportSessionReconcile != nil {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		exportResult, err := runner.config.ExportSessionReconcile.RunOnce(ctx)
		result.ExportSessionReconcile = exportResult
		if err != nil {
			errs = append(errs, fmt.Errorf("export session reconcile: %w", err))
			if isContextError(err) {
				return result, errors.Join(errs...)
			}
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			return result, errors.Join(errs...)
		}
	}

	if runner.config.WorkloadMountStale != nil {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		staleMountResult, err := runner.config.WorkloadMountStale.RunOnce(ctx)
		result.WorkloadMountStale = staleMountResult
		if err != nil {
			errs = append(errs, fmt.Errorf("workload mount stale lease scan: %w", err))
			if isContextError(err) {
				return result, errors.Join(errs...)
			}
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			return result, errors.Join(errs...)
		}
	}

	if runner.config.RestoreReconciliation != nil {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		restoreResult, err := runner.config.RestoreReconciliation.RunOnce(ctx)
		result.RestoreReconciliation = restoreResult
		if err != nil {
			errs = append(errs, fmt.Errorf("restore reconciliation: %w", err))
			if isContextError(err) {
				return result, errors.Join(errs...)
			}
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			return result, errors.Join(errs...)
		}
		if restoreResult.Blocked > 0 || restoreResult.Failed > 0 {
			return result, errors.Join(errs...)
		}
	}

	evidenceErrCount := len(errs)

	if runner.config.OperationRecovery != nil && evidenceErrCount == 0 {
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
			return result, errors.Join(errs...)
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
		ExportSessionReconcile: ExportSessionReconcileSummary{
			RecoveredRuntimeRequests: result.ExportSessionReconcile.RecoveredRuntimeRequests,
			RecoveredRuntimeWrites:   result.ExportSessionReconcile.RecoveredRuntimeWrites,
			Scanned:                  result.ExportSessionReconcile.Scanned,
			Terminalized:             result.ExportSessionReconcile.Terminalized,
			Reused:                   result.ExportSessionReconcile.Reused,
			Skipped:                  result.ExportSessionReconcile.Skipped,
			RaceLost:                 result.ExportSessionReconcile.RaceLost,
			Failed:                   result.ExportSessionReconcile.Failed,
		},
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
		WorkloadMountStale: WorkloadMountStaleSummary{
			Scanned:     result.WorkloadMountStale.Scanned,
			KeptBlocked: result.WorkloadMountStale.KeptBlocked,
			Failed:      result.WorkloadMountStale.Failed,
		},
		RestoreReconciliation: RestoreReconciliationSummary{
			Scanned:   result.RestoreReconciliation.Scanned,
			Completed: result.RestoreReconciliation.Completed,
			Blocked:   result.RestoreReconciliation.Blocked,
			Skipped:   result.RestoreReconciliation.Skipped,
			Failed:    result.RestoreReconciliation.Failed,
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
