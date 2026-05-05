package inspection

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

type RecoveryAction string

const (
	RecoveryActionNoop                 RecoveryAction = "noop"
	RecoveryActionClaimable            RecoveryAction = "claimable"
	RecoveryActionRetry                RecoveryAction = "retry"
	RecoveryActionWait                 RecoveryAction = "wait"
	RecoveryActionReclaim              RecoveryAction = "reclaim"
	RecoveryActionRecover              RecoveryAction = "recover"
	RecoveryActionDeliver              RecoveryAction = "deliver"
	RecoveryActionFinalizeCancellation RecoveryAction = "finalize_cancellation"
	RecoveryActionManualIntervention   RecoveryAction = "manual_intervention"
)

type RecoveryContext struct {
	Now                      time.Time
	ExplicitRecovery         bool
	StaleDeliveringThreshold time.Duration
}

type RecoveryPlan struct {
	Action RecoveryAction
	Reason string
}

func ClassifyOperationRecovery(record operations.OperationRecord, ctx RecoveryContext) RecoveryPlan {
	if record.Attempt < 0 {
		return manual("invalid_operation_attempt")
	}
	if err := validateOperationLeasePair(record); err != "" {
		return manual(err)
	}
	if !record.State.Valid() {
		return manual(fmt.Sprintf("invalid_operation_state:%s", record.State))
	}
	if record.State.IsTerminal() {
		return plan(RecoveryActionNoop, "operation_terminal")
	}

	switch record.State {
	case operations.OperationStateQueued:
		if record.LeaseExpiresAt != nil || strings.TrimSpace(record.LeaseOwner) != "" {
			return manual("queued_operation_has_lease")
		}
		if record.Attempt > 0 {
			return plan(RecoveryActionRetry, "queued_operation_retry")
		}
		return plan(RecoveryActionClaimable, "queued_operation_claimable")
	case operations.OperationStateRunning:
		if ctx.Now.IsZero() {
			return manual("missing_recovery_time")
		}
		if record.LeaseExpiresAt == nil || strings.TrimSpace(record.LeaseOwner) == "" {
			return manual("running_operation_invalid_lease")
		}
		if record.LeaseExpiresAt.After(ctx.Now) {
			return plan(RecoveryActionWait, "running_operation_live_lease")
		}
		return plan(RecoveryActionReclaim, "running_operation_expired_lease")
	case operations.OperationStateOperatorInterventionRequired:
		if ctx.ExplicitRecovery {
			return plan(RecoveryActionRecover, "operator_intervention_explicit_recovery")
		}
		return manual("operator_intervention_required")
	case operations.OperationStateCancelRequested:
		return plan(RecoveryActionFinalizeCancellation, "cancel_requested")
	default:
		return manual(fmt.Sprintf("unsupported_operation_state:%s", record.State))
	}
}

func ClassifyFenceRecovery(fence fences.Fence) RecoveryPlan {
	if err := fences.ValidateFence(fence); err != nil {
		return manual("invalid_fence:" + err.Field)
	}
	if !fence.Held() {
		return plan(RecoveryActionNoop, "fence_not_held")
	}

	switch fence.Status {
	case fences.StatusReleased, fences.StatusRecovered:
		return plan(RecoveryActionNoop, "fence_released_or_recovered")
	case fences.StatusActive:
		return plan(RecoveryActionWait, "fence_active_held")
	case fences.StatusExpired, fences.StatusRecoveryRequired:
		return plan(RecoveryActionRecover, "fence_held_recovery_required")
	default:
		return manual(fmt.Sprintf("invalid_fence_status:%s", fence.Status))
	}
}

func ClassifyAuditOutboxRecovery(record audit.OutboxRecord, ctx RecoveryContext) RecoveryPlan {
	if err := validateAuditOutboxRecord(record); err != "" {
		return manual(err)
	}

	switch record.Status {
	case audit.OutboxStatusPending:
		return plan(RecoveryActionDeliver, "outbox_pending")
	case audit.OutboxStatusRetryWait:
		if record.NextRetryAt == nil {
			return manual("retry_wait_missing_next_retry_at")
		}
		if ctx.Now.IsZero() {
			return manual("missing_recovery_time")
		}
		if record.NextRetryAt.After(ctx.Now) {
			return plan(RecoveryActionWait, "outbox_retry_wait_future")
		}
		return plan(RecoveryActionDeliver, "outbox_retry_wait_due")
	case audit.OutboxStatusDelivering:
		if record.UpdatedAt.IsZero() {
			return manual("delivering_missing_updated_at")
		}
		if ctx.Now.IsZero() {
			return manual("missing_recovery_time")
		}
		if ctx.StaleDeliveringThreshold <= 0 {
			return manual("invalid_stale_delivering_threshold")
		}
		if record.UpdatedAt.Add(ctx.StaleDeliveringThreshold).After(ctx.Now) {
			return plan(RecoveryActionWait, "outbox_delivering_not_stale")
		}
		return plan(RecoveryActionRecover, "outbox_delivering_stale")
	case audit.OutboxStatusDelivered:
		if record.DeliveredAt == nil {
			return manual("delivered_missing_delivered_at")
		}
		return plan(RecoveryActionNoop, "outbox_delivered")
	case audit.OutboxStatusFailed:
		return manual("outbox_failed")
	default:
		return manual(fmt.Sprintf("invalid_outbox_status:%s", record.Status))
	}
}

func validateOperationLeasePair(record operations.OperationRecord) string {
	owner := strings.TrimSpace(record.LeaseOwner)
	hasOwner := owner != ""
	hasExpiry := record.LeaseExpiresAt != nil
	if record.LeaseOwner != "" && !hasOwner {
		return "invalid_operation_lease_owner"
	}
	if hasOwner != hasExpiry {
		return "invalid_operation_lease_pair"
	}
	return ""
}

func validateAuditOutboxRecord(record audit.OutboxRecord) string {
	if strings.TrimSpace(record.EventID) == "" {
		return "missing_outbox_event_id"
	}
	if strings.TrimSpace(string(record.EventType)) == "" {
		return "missing_outbox_event_type"
	}
	if record.EventTime.IsZero() {
		return "missing_outbox_event_time"
	}
	if err := validateAuditOutboxPayload(record.PayloadJSON); err != "" {
		return err
	}
	if !record.Status.Valid() {
		return fmt.Sprintf("invalid_outbox_status:%s", record.Status)
	}
	if record.DeliveryAttempt < 0 {
		return "invalid_outbox_delivery_attempt"
	}
	return ""
}

func validateAuditOutboxPayload(payload json.RawMessage) string {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return "missing_outbox_payload_json"
	}

	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return "invalid_outbox_payload_json"
	}
	if object == nil {
		return "invalid_outbox_payload_json"
	}
	return ""
}

func plan(action RecoveryAction, reason string) RecoveryPlan {
	return RecoveryPlan{Action: action, Reason: reason}
}

func manual(reason string) RecoveryPlan {
	return plan(RecoveryActionManualIntervention, reason)
}
