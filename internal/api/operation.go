package api

import "encoding/json"

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

type ResourceRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type OperationEnvelope struct {
	OperationID    string         `json:"operation_id"`
	OperationState OperationState `json:"operation_state"`
	Resource       ResourceRef    `json:"resource"`
	Result         map[string]any `json:"result"`
	Error          *StandardError `json:"error"`
}

type OperationEnvelopeSpec struct {
	OperationID    string
	OperationState OperationState
	Resource       ResourceRef
	Result         map[string]any
	Error          *StandardError
}

func NewOperationEnvelope(spec OperationEnvelopeSpec) OperationEnvelope {
	return OperationEnvelope{
		OperationID:    spec.OperationID,
		OperationState: spec.OperationState,
		Resource:       spec.Resource,
		Result:         cloneOperationResult(spec.Result),
		Error:          spec.Error,
	}
}

func MarshalOperationEnvelope(envelope OperationEnvelope) ([]byte, error) {
	return json.Marshal(envelope)
}

func cloneOperationResult(result map[string]any) map[string]any {
	if result == nil {
		return nil
	}

	cloned := make(map[string]any, len(result))
	for key, value := range result {
		cloned[key] = value
	}
	return cloned
}
