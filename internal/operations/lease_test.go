package operations

import (
	"errors"
	"testing"
	"time"
)

func TestAcquireLeaseClaimsQueuedOperationWithoutLease(t *testing.T) {
	now := leaseTestTime()
	record := OperationRecord{
		ID:        "op-queued",
		State:     OperationStateQueued,
		Attempt:   0,
		CreatedAt: now.Add(-time.Hour),
	}

	decision := AcquireLease(record, LeaseRequest{
		Owner:    "worker-a",
		Duration: 30 * time.Minute,
		Now:      now,
	})

	if !decision.Allowed || decision.Action != LeaseActionClaim {
		t.Fatalf("AcquireLease decision = %#v, want allowed claim", decision)
	}
	updated := decision.Record
	if updated.State != OperationStateRunning {
		t.Fatalf("state = %s, want running", updated.State)
	}
	if updated.LeaseOwner != "worker-a" {
		t.Fatalf("lease owner = %q, want worker-a", updated.LeaseOwner)
	}
	if !sameTimePtr(updated.LeaseExpiresAt, now.Add(30*time.Minute)) {
		t.Fatalf("lease expiry = %v, want %v", updated.LeaseExpiresAt, now.Add(30*time.Minute))
	}
	if updated.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", updated.Attempt)
	}
	if !sameTimePtr(updated.StartedAt, now) {
		t.Fatalf("started_at = %v, want %v", updated.StartedAt, now)
	}

	if record.State != OperationStateQueued || record.LeaseOwner != "" || record.LeaseExpiresAt != nil || record.StartedAt != nil {
		t.Fatalf("AcquireLease mutated input record: %#v", record)
	}
}

func TestAcquireLeaseReclaimsExpiredRunningLease(t *testing.T) {
	now := leaseTestTime()
	startedAt := now.Add(-2 * time.Hour)
	expiresAt := now.Add(-time.Second)
	record := OperationRecord{
		ID:             "op-running",
		State:          OperationStateRunning,
		Attempt:        2,
		LeaseOwner:     "worker-a",
		LeaseExpiresAt: &expiresAt,
		StartedAt:      &startedAt,
	}

	decision := AcquireLease(record, LeaseRequest{
		Owner:    "worker-b",
		Duration: 45 * time.Minute,
		Now:      now,
	})

	if !decision.Allowed || decision.Action != LeaseActionReclaim {
		t.Fatalf("AcquireLease decision = %#v, want allowed reclaim", decision)
	}
	updated := decision.Record
	if updated.State != OperationStateRunning {
		t.Fatalf("state = %s, want running", updated.State)
	}
	if updated.LeaseOwner != "worker-b" {
		t.Fatalf("lease owner = %q, want worker-b", updated.LeaseOwner)
	}
	if !sameTimePtr(updated.LeaseExpiresAt, now.Add(45*time.Minute)) {
		t.Fatalf("lease expiry = %v, want %v", updated.LeaseExpiresAt, now.Add(45*time.Minute))
	}
	if updated.Attempt != 3 {
		t.Fatalf("attempt = %d, want 3", updated.Attempt)
	}
	if updated.StartedAt != &startedAt {
		t.Fatalf("started_at pointer changed; got %p want %p", updated.StartedAt, &startedAt)
	}
}

func TestLiveRunningLeaseCannotBeClaimedByAnotherOwnerAndSameOwnerCanRenew(t *testing.T) {
	now := leaseTestTime()
	expiresAt := now.Add(10 * time.Minute)
	record := OperationRecord{
		ID:             "op-live",
		State:          OperationStateRunning,
		Attempt:        1,
		LeaseOwner:     "worker-a",
		LeaseExpiresAt: &expiresAt,
	}

	claim := AcquireLease(record, LeaseRequest{
		Owner:    "worker-b",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if claim.Allowed {
		t.Fatalf("AcquireLease allowed different owner on live lease: %#v", claim)
	}
	assertLeaseError(t, claim.Error, ErrLeaseUnavailable)
	if claim.Record.LeaseOwner != "worker-a" || !sameTimePtr(claim.Record.LeaseExpiresAt, expiresAt) {
		t.Fatalf("denied claim changed returned record: %#v", claim.Record)
	}

	renew := RenewLease(record, LeaseRequest{
		Owner:    "worker-a",
		Duration: 30 * time.Minute,
		Now:      now,
	})
	if !renew.Allowed || renew.Action != LeaseActionRenew {
		t.Fatalf("RenewLease decision = %#v, want allowed renew", renew)
	}
	if renew.Record.Attempt != 1 {
		t.Fatalf("renew attempt = %d, want unchanged 1", renew.Record.Attempt)
	}
	if renew.Record.LeaseOwner != "worker-a" {
		t.Fatalf("renew owner = %q, want worker-a", renew.Record.LeaseOwner)
	}
	if !sameTimePtr(renew.Record.LeaseExpiresAt, now.Add(30*time.Minute)) {
		t.Fatalf("renew expiry = %v, want %v", renew.Record.LeaseExpiresAt, now.Add(30*time.Minute))
	}
}

func TestTerminalStatesCannotBeClaimedOrRenewed(t *testing.T) {
	now := leaseTestTime()
	for _, state := range []OperationState{
		OperationStateSucceeded,
		OperationStateFailed,
		OperationStateCancelled,
	} {
		t.Run(state.String(), func(t *testing.T) {
			record := OperationRecord{ID: "op-terminal", State: state, Attempt: 1}
			request := LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now}

			claim := AcquireLease(record, request)
			if claim.Allowed {
				t.Fatalf("AcquireLease allowed terminal state %s: %#v", state, claim)
			}
			assertLeaseError(t, claim.Error, ErrInvalidStateTransition)

			renew := RenewLease(record, request)
			if renew.Allowed {
				t.Fatalf("RenewLease allowed terminal state %s: %#v", state, renew)
			}
			assertLeaseError(t, renew.Error, ErrInvalidStateTransition)
		})
	}
}

func TestOperatorInterventionRequiresExplicitRecoveryMode(t *testing.T) {
	now := leaseTestTime()
	record := OperationRecord{
		ID:      "op-operator",
		State:   OperationStateOperatorInterventionRequired,
		Attempt: 4,
	}
	request := LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now}

	automatic := AcquireLease(record, request)
	if automatic.Allowed {
		t.Fatalf("AcquireLease allowed operator intervention without explicit recovery: %#v", automatic)
	}
	assertLeaseError(t, automatic.Error, ErrLeaseUnavailable)
	if automatic.Record.State != OperationStateOperatorInterventionRequired || automatic.Record.Attempt != 4 {
		t.Fatalf("default-denied operator record changed: %#v", automatic.Record)
	}

	request.RecoveryMode = LeaseRecoveryExplicitAction
	recovery := AcquireLease(record, request)
	if !recovery.Allowed || recovery.Action != LeaseActionRecover {
		t.Fatalf("AcquireLease recovery decision = %#v, want allowed recovery", recovery)
	}
	if recovery.Record.State != OperationStateRunning {
		t.Fatalf("recovery state = %s, want running", recovery.Record.State)
	}
	if recovery.Record.Attempt != 5 {
		t.Fatalf("recovery attempt = %d, want 5", recovery.Record.Attempt)
	}
}

func TestCancelRequestedDoesNotBecomeRunningWithoutExplicitPolicy(t *testing.T) {
	now := leaseTestTime()
	record := OperationRecord{
		ID:      "op-cancel",
		State:   OperationStateCancelRequested,
		Attempt: 3,
	}
	request := LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now}

	automatic := AcquireLease(record, request)
	if automatic.Allowed {
		t.Fatalf("AcquireLease allowed cancel_requested by default: %#v", automatic)
	}
	assertLeaseError(t, automatic.Error, ErrInvalidStateTransition)
	if automatic.Record.State == OperationStateRunning || automatic.Record.State == OperationStateSucceeded {
		t.Fatalf("cancel_requested silently changed to unsafe state: %#v", automatic.Record)
	}

	request.CancelPolicy = LeaseCancelPolicyFinalize
	finalized := AcquireLease(record, request)
	if !finalized.Allowed || finalized.Action != LeaseActionFinalizeCancellation {
		t.Fatalf("AcquireLease finalize decision = %#v, want cancellation finalization", finalized)
	}
	if finalized.Record.State != OperationStateCancelled {
		t.Fatalf("finalized state = %s, want cancelled", finalized.Record.State)
	}
	if finalized.Record.Attempt != 3 {
		t.Fatalf("finalized attempt = %d, want unchanged 3", finalized.Record.Attempt)
	}
	if finalized.Record.LeaseOwner != "" || finalized.Record.LeaseExpiresAt != nil {
		t.Fatalf("finalized cancellation should not assign a lease: %#v", finalized.Record)
	}
	if !sameTimePtr(finalized.Record.FinishedAt, now) {
		t.Fatalf("finished_at = %v, want %v", finalized.Record.FinishedAt, now)
	}
}

func TestAcquireLeaseRejectsInvalidInputsAndExistingRecords(t *testing.T) {
	now := leaseTestTime()
	validRecord := OperationRecord{ID: "op-queued", State: OperationStateQueued}
	expiresAt := now.Add(-time.Minute)

	tests := []struct {
		name    string
		record  OperationRecord
		request LeaseRequest
		want    error
	}{
		{
			name:    "missing owner",
			record:  validRecord,
			request: LeaseRequest{Duration: time.Minute, Now: now},
			want:    ErrInvalidLeaseRequest,
		},
		{
			name:    "non-positive duration",
			record:  validRecord,
			request: LeaseRequest{Owner: "worker-a", Now: now},
			want:    ErrInvalidLeaseRequest,
		},
		{
			name:    "zero now",
			record:  validRecord,
			request: LeaseRequest{Owner: "worker-a", Duration: time.Minute},
			want:    ErrInvalidLeaseRequest,
		},
		{
			name:    "negative attempt",
			record:  OperationRecord{ID: "op-bad", State: OperationStateQueued, Attempt: -1},
			request: LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now},
			want:    ErrInvalidLeaseRecord,
		},
		{
			name: "running record missing owner with expiry",
			record: OperationRecord{
				ID:             "op-bad-lease",
				State:          OperationStateRunning,
				Attempt:        1,
				LeaseExpiresAt: &expiresAt,
			},
			request: LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now},
			want:    ErrInvalidLeaseRecord,
		},
		{
			name: "running record blank owner with expiry",
			record: OperationRecord{
				ID:             "op-bad-lease",
				State:          OperationStateRunning,
				Attempt:        1,
				LeaseOwner:     "  ",
				LeaseExpiresAt: &expiresAt,
			},
			request: LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now},
			want:    ErrInvalidLeaseRecord,
		},
		{
			name: "running record owner without expiry",
			record: OperationRecord{
				ID:         "op-bad-lease",
				State:      OperationStateRunning,
				Attempt:    1,
				LeaseOwner: "worker-a",
			},
			request: LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now},
			want:    ErrInvalidLeaseRecord,
		},
		{
			name: "running record without lease pair",
			record: OperationRecord{
				ID:      "op-bad-lease",
				State:   OperationStateRunning,
				Attempt: 1,
			},
			request: LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now},
			want:    ErrInvalidLeaseRecord,
		},
		{
			name: "queued record carrying stale lease",
			record: OperationRecord{
				ID:             "op-bad-queued",
				State:          OperationStateQueued,
				LeaseOwner:     "worker-a",
				LeaseExpiresAt: &expiresAt,
			},
			request: LeaseRequest{Owner: "worker-b", Duration: time.Minute, Now: now},
			want:    ErrInvalidLeaseRecord,
		},
		{
			name:    "bad transition",
			record:  OperationRecord{ID: "op-bad-state", State: OperationState("unknown")},
			request: LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now},
			want:    ErrInvalidStateTransition,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := AcquireLease(tc.record, tc.request)
			if decision.Allowed {
				t.Fatalf("AcquireLease allowed invalid input: %#v", decision)
			}
			assertLeaseError(t, decision.Error, tc.want)
		})
	}
}

func TestRenewLeaseRejectsInvalidTransitionAndOwnerMismatch(t *testing.T) {
	now := leaseTestTime()
	expiresAt := now.Add(time.Minute)

	queued := RenewLease(
		OperationRecord{ID: "op-queued", State: OperationStateQueued},
		LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now},
	)
	if queued.Allowed {
		t.Fatalf("RenewLease allowed queued operation: %#v", queued)
	}
	assertLeaseError(t, queued.Error, ErrInvalidStateTransition)

	wrongOwner := RenewLease(
		OperationRecord{ID: "op-live", State: OperationStateRunning, Attempt: 1, LeaseOwner: "worker-a", LeaseExpiresAt: &expiresAt},
		LeaseRequest{Owner: "worker-b", Duration: time.Minute, Now: now},
	)
	if wrongOwner.Allowed {
		t.Fatalf("RenewLease allowed wrong owner: %#v", wrongOwner)
	}
	assertLeaseError(t, wrongOwner.Error, ErrLeaseUnavailable)
}

func TestRenewLeaseRejectsDirtyLeasePairBeforeOwnershipCheck(t *testing.T) {
	now := leaseTestTime()
	expiresAt := now.Add(time.Minute)

	tests := []struct {
		name   string
		record OperationRecord
	}{
		{
			name: "without lease pair",
			record: OperationRecord{
				ID:      "op-bad-renew",
				State:   OperationStateRunning,
				Attempt: 1,
			},
		},
		{
			name: "missing owner with expiry",
			record: OperationRecord{
				ID:             "op-bad-renew",
				State:          OperationStateRunning,
				Attempt:        1,
				LeaseExpiresAt: &expiresAt,
			},
		},
		{
			name: "owner without expiry",
			record: OperationRecord{
				ID:         "op-bad-renew",
				State:      OperationStateRunning,
				Attempt:    1,
				LeaseOwner: "worker-a",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := RenewLease(tc.record, LeaseRequest{
				Owner:    "worker-a",
				Duration: time.Minute,
				Now:      now,
			})
			if decision.Allowed {
				t.Fatalf("RenewLease allowed dirty lease pair: %#v", decision)
			}
			assertLeaseError(t, decision.Error, ErrInvalidLeaseRecord)
		})
	}
}

func assertLeaseError(t *testing.T, err error, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want errors.Is(_, %v)", err, want)
	}

	var leaseErr *OperationLeaseError
	if !errors.As(err, &leaseErr) {
		t.Fatalf("error = %T, want *OperationLeaseError", err)
	}
}

func sameTimePtr(got *time.Time, want time.Time) bool {
	return got != nil && got.Equal(want)
}

func leaseTestTime() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}
