package inspection

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestClassifyOperationRecovery(t *testing.T) {
	now := recoveryPlannerTestTime()
	liveLease := now.Add(5 * time.Minute)
	expiredLease := now.Add(-time.Minute)

	tests := []struct {
		name   string
		record operations.OperationRecord
		ctx    RecoveryContext
		want   RecoveryAction
	}{
		{name: "terminal noop", record: operations.OperationRecord{State: operations.OperationStateSucceeded}, want: RecoveryActionNoop},
		{name: "queued first claim", record: operations.OperationRecord{State: operations.OperationStateQueued}, want: RecoveryActionClaimable},
		{name: "queued retry", record: operations.OperationRecord{State: operations.OperationStateQueued, Attempt: 2}, want: RecoveryActionRetry},
		{name: "running live lease waits", record: operations.OperationRecord{State: operations.OperationStateRunning, LeaseOwner: "worker-a", LeaseExpiresAt: &liveLease}, want: RecoveryActionWait},
		{name: "running expired lease reclaims", record: operations.OperationRecord{State: operations.OperationStateRunning, LeaseOwner: "worker-a", LeaseExpiresAt: &expiredLease}, want: RecoveryActionReclaim},
		{name: "running missing lease is manual", record: operations.OperationRecord{State: operations.OperationStateRunning}, want: RecoveryActionManualIntervention},
		{name: "operator intervention is manual by default", record: operations.OperationRecord{State: operations.OperationStateOperatorInterventionRequired}, want: RecoveryActionManualIntervention},
		{name: "operator intervention with explicit recovery", record: operations.OperationRecord{State: operations.OperationStateOperatorInterventionRequired}, ctx: RecoveryContext{ExplicitRecovery: true}, want: RecoveryActionRecover},
		{name: "cancel requested finalizes", record: operations.OperationRecord{State: operations.OperationStateCancelRequested}, want: RecoveryActionFinalizeCancellation},
		{name: "unknown state is manual", record: operations.OperationRecord{State: operations.OperationState("wedged")}, want: RecoveryActionManualIntervention},
		{name: "negative attempt is manual", record: operations.OperationRecord{State: operations.OperationStateQueued, Attempt: -1}, want: RecoveryActionManualIntervention},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			ctx.Now = now
			plan := ClassifyOperationRecovery(tt.record, ctx)
			assertRecoveryPlan(t, plan, tt.want)
		})
	}
}

func TestClassifyFenceRecovery(t *testing.T) {
	now := recoveryPlannerTestTime()
	releasedAt := now.Add(-time.Minute)
	recoveredAt := now
	recoveryStartedAt := now.Add(-2 * time.Minute)

	tests := []struct {
		name  string
		fence fences.Fence
		want  RecoveryAction
	}{
		{name: "released noop", fence: recoveryFence(fences.StatusReleased, &releasedAt, nil, nil), want: RecoveryActionNoop},
		{name: "recovered noop", fence: recoveryFence(fences.StatusRecovered, &releasedAt, &recoveredAt, &recoveryStartedAt), want: RecoveryActionNoop},
		{name: "active held waits", fence: recoveryFence(fences.StatusActive, nil, nil, nil), want: RecoveryActionWait},
		{name: "expired held recovers", fence: recoveryFence(fences.StatusExpired, nil, nil, nil), want: RecoveryActionRecover},
		{name: "recovery required held recovers", fence: recoveryFence(fences.StatusRecoveryRequired, nil, nil, nil), want: RecoveryActionRecover},
		{name: "invalid fence manual", fence: fences.Fence{ID: "fence_1", RepoID: "repo_alpha", Kind: fences.KindWriterSession, Status: fences.StatusActive}, want: RecoveryActionManualIntervention},
		{name: "unknown status manual", fence: recoveryFence(fences.Status("wedged"), nil, nil, nil), want: RecoveryActionManualIntervention},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := ClassifyFenceRecovery(tt.fence)
			assertRecoveryPlan(t, plan, tt.want)
		})
	}
}

func TestClassifyAuditOutboxRecovery(t *testing.T) {
	now := recoveryPlannerTestTime()
	pastRetry := now.Add(-time.Second)
	futureRetry := now.Add(time.Minute)
	staleUpdatedAt := now.Add(-30 * time.Minute)
	freshUpdatedAt := now.Add(-time.Minute)
	deliveredAt := now.Add(-time.Second)

	tests := []struct {
		name   string
		record audit.OutboxRecord
		want   RecoveryAction
	}{
		{name: "pending delivers", record: recoveryOutbox(audit.OutboxStatusPending, nil, now), want: RecoveryActionDeliver},
		{name: "retry wait due delivers", record: recoveryOutbox(audit.OutboxStatusRetryWait, &pastRetry, now), want: RecoveryActionDeliver},
		{name: "retry wait future waits", record: recoveryOutbox(audit.OutboxStatusRetryWait, &futureRetry, now), want: RecoveryActionWait},
		{name: "fresh delivering waits", record: recoveryOutbox(audit.OutboxStatusDelivering, nil, freshUpdatedAt), want: RecoveryActionWait},
		{name: "stale delivering recovers", record: recoveryOutbox(audit.OutboxStatusDelivering, nil, staleUpdatedAt), want: RecoveryActionRecover},
		{name: "delivered noop", record: deliveredRecoveryOutbox(deliveredAt, now), want: RecoveryActionNoop},
		{name: "failed manual", record: recoveryOutbox(audit.OutboxStatusFailed, nil, now), want: RecoveryActionManualIntervention},
		{name: "missing payload manual", record: audit.OutboxRecord{EventID: "evt_1", EventType: audit.EventTypeExportCreate, EventTime: now, Status: audit.OutboxStatusPending}, want: RecoveryActionManualIntervention},
		{name: "unknown status manual", record: recoveryOutbox(audit.OutboxStatus("wedged"), nil, now), want: RecoveryActionManualIntervention},
		{name: "negative attempt manual", record: recoveryOutboxWithAttempt(audit.OutboxStatusPending, nil, now, -1), want: RecoveryActionManualIntervention},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := ClassifyAuditOutboxRecovery(tt.record, RecoveryContext{
				Now:                      now,
				StaleDeliveringThreshold: 10 * time.Minute,
			})
			assertRecoveryPlan(t, plan, tt.want)
		})
	}
}

func assertRecoveryPlan(t *testing.T, plan RecoveryPlan, want RecoveryAction) {
	t.Helper()
	if plan.Action != want {
		t.Fatalf("Action = %q, want %q; plan = %#v", plan.Action, want, plan)
	}
	if plan.Reason == "" {
		t.Fatalf("Reason is empty for plan %#v", plan)
	}
}

func recoveryPlannerTestTime() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func recoveryFence(status fences.Status, releasedAt, recoveredAt, recoveryStartedAt *time.Time) fences.Fence {
	return fences.Fence{
		ID:                  "fence_1",
		RepoID:              "repo_alpha",
		Kind:                fences.KindWriterSession,
		HolderOperationID:   "op_restore",
		Status:              status,
		ExpiresAt:           recoveryPlannerTestTime().Add(time.Hour),
		ReleasedAt:          releasedAt,
		RecoveredAt:         recoveredAt,
		RecoveryStartedAt:   recoveryStartedAt,
		RecoveryOperationID: "op_recover",
		CreatedAt:           recoveryPlannerTestTime().Add(-time.Hour),
		UpdatedAt:           recoveryPlannerTestTime(),
	}
}

func recoveryOutbox(status audit.OutboxStatus, nextRetryAt *time.Time, updatedAt time.Time) audit.OutboxRecord {
	return recoveryOutboxWithAttempt(status, nextRetryAt, updatedAt, 1)
}

func recoveryOutboxWithAttempt(status audit.OutboxStatus, nextRetryAt *time.Time, updatedAt time.Time, attempt int) audit.OutboxRecord {
	payload, _ := json.Marshal(map[string]any{"event_id": "evt_1"})
	return audit.OutboxRecord{
		EventID:         "evt_1",
		EventType:       audit.EventTypeExportCreate,
		EventTime:       recoveryPlannerTestTime().Add(-time.Hour),
		PayloadJSON:     payload,
		Status:          status,
		DeliveryAttempt: attempt,
		NextRetryAt:     nextRetryAt,
		CreatedAt:       recoveryPlannerTestTime().Add(-time.Hour),
		UpdatedAt:       updatedAt,
	}
}

func deliveredRecoveryOutbox(deliveredAt, updatedAt time.Time) audit.OutboxRecord {
	record := recoveryOutbox(audit.OutboxStatusDelivered, nil, updatedAt)
	record.DeliveredAt = &deliveredAt
	return record
}
