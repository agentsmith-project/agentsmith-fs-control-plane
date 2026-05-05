package operations

import (
	"errors"
	"fmt"
)

var ErrInvalidStateTransition = errors.New("invalid operation state transition")

type OperationState string

const (
	OperationStateQueued                       OperationState = "queued"
	OperationStateRunning                      OperationState = "running"
	OperationStateSucceeded                    OperationState = "succeeded"
	OperationStateFailed                       OperationState = "failed"
	OperationStateCancelRequested              OperationState = "cancel_requested"
	OperationStateCancelled                    OperationState = "cancelled"
	OperationStateOperatorInterventionRequired OperationState = "operator_intervention_required"
)

func (state OperationState) String() string {
	return string(state)
}

func (state OperationState) IsTerminal() bool {
	switch state {
	case OperationStateSucceeded, OperationStateFailed, OperationStateCancelled:
		return true
	default:
		return false
	}
}

func ValidateTransition(from, to OperationState) error {
	if !from.Valid() || !to.Valid() {
		return fmt.Errorf("%w: unknown state %q -> %q", ErrInvalidStateTransition, from, to)
	}
	if from == to {
		return nil
	}
	if from.IsTerminal() {
		return fmt.Errorf("%w: terminal state %q cannot transition to %q", ErrInvalidStateTransition, from, to)
	}

	if allowedNextStates[from][to] {
		return nil
	}

	return fmt.Errorf("%w: %q cannot transition to %q", ErrInvalidStateTransition, from, to)
}

func (state OperationState) Valid() bool {
	switch state {
	case OperationStateQueued,
		OperationStateRunning,
		OperationStateSucceeded,
		OperationStateFailed,
		OperationStateCancelRequested,
		OperationStateCancelled,
		OperationStateOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

var allowedNextStates = map[OperationState]map[OperationState]bool{
	OperationStateQueued: {
		OperationStateRunning:                      true,
		OperationStateCancelRequested:              true,
		OperationStateCancelled:                    true,
		OperationStateFailed:                       true,
		OperationStateOperatorInterventionRequired: true,
	},
	OperationStateRunning: {
		OperationStateSucceeded:                    true,
		OperationStateFailed:                       true,
		OperationStateCancelRequested:              true,
		OperationStateCancelled:                    true,
		OperationStateOperatorInterventionRequired: true,
	},
	OperationStateCancelRequested: {
		OperationStateCancelled:                    true,
		OperationStateFailed:                       true,
		OperationStateOperatorInterventionRequired: true,
	},
	OperationStateOperatorInterventionRequired: {
		OperationStateRunning:   true,
		OperationStateFailed:    true,
		OperationStateCancelled: true,
	},
}
