package operations

import (
	"errors"
	"testing"
)

func TestOperationStatesAreStableWireValues(t *testing.T) {
	states := map[OperationState]string{
		OperationStateQueued:                       "queued",
		OperationStateRunning:                      "running",
		OperationStateSucceeded:                    "succeeded",
		OperationStateFailed:                       "failed",
		OperationStateCancelRequested:              "cancel_requested",
		OperationStateCancelled:                    "cancelled",
		OperationStateOperatorInterventionRequired: "operator_intervention_required",
	}

	for state, want := range states {
		if got := state.String(); got != want {
			t.Fatalf("%#v String() = %q, want %q", state, got, want)
		}
	}
}

func TestRecoveryOrientedStateTransitions(t *testing.T) {
	allowed := []struct {
		name string
		from OperationState
		to   OperationState
	}{
		{"queue starts running", OperationStateQueued, OperationStateRunning},
		{"queue can be cancelled before work starts", OperationStateQueued, OperationStateCancelled},
		{"running succeeds", OperationStateRunning, OperationStateSucceeded},
		{"running fails", OperationStateRunning, OperationStateFailed},
		{"running asks for cancellation", OperationStateRunning, OperationStateCancelRequested},
		{"cancel request reaches cancelled", OperationStateCancelRequested, OperationStateCancelled},
		{"running requires operator", OperationStateRunning, OperationStateOperatorInterventionRequired},
		{"operator can resume running after recovery action", OperationStateOperatorInterventionRequired, OperationStateRunning},
		{"operator can finish as failed", OperationStateOperatorInterventionRequired, OperationStateFailed},
	}

	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTransition(tc.from, tc.to); err != nil {
				t.Fatalf("ValidateTransition(%s, %s): %v", tc.from, tc.to, err)
			}
		})
	}
}

func TestStateTransitionsRejectRollbackFromTerminalOrLaterStates(t *testing.T) {
	illegal := []struct {
		name string
		from OperationState
		to   OperationState
	}{
		{"succeeded cannot return to running", OperationStateSucceeded, OperationStateRunning},
		{"failed cannot return to queued", OperationStateFailed, OperationStateQueued},
		{"cancelled cannot return to running", OperationStateCancelled, OperationStateRunning},
		{"operator cannot hide uncertainty as succeeded", OperationStateOperatorInterventionRequired, OperationStateSucceeded},
		{"running cannot go back to queued", OperationStateRunning, OperationStateQueued},
		{"cancel requested cannot go back to queued", OperationStateCancelRequested, OperationStateQueued},
	}

	for _, tc := range illegal {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTransition(tc.from, tc.to)
			if !errors.Is(err, ErrInvalidStateTransition) {
				t.Fatalf("ValidateTransition(%s, %s) = %v, want ErrInvalidStateTransition", tc.from, tc.to, err)
			}
		})
	}
}
