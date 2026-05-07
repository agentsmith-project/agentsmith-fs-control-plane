package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationEnvelopeJSONIsFlatAndSchemaShaped(t *testing.T) {
	envelope := NewOperationEnvelope(OperationEnvelopeSpec{
		OperationID:    "op_alpha",
		OperationState: OperationStateQueued,
		Resource:       ResourceRef{Type: "repo", ID: "repo_unit"},
	})

	got, err := MarshalOperationEnvelope(envelope)
	if err != nil {
		t.Fatalf("MarshalOperationEnvelope returned error: %v", err)
	}

	want := `{"operation_id":"op_alpha","operation_state":"queued","resource":{"type":"repo","id":"repo_unit"},"result":null,"error":null}`
	if string(got) != want {
		t.Fatalf("unexpected JSON\nwant: %s\n got: %s", want, string(got))
	}
	if strings.Contains(string(got), `"operation"`) {
		t.Fatalf("flat API OperationEnvelope must not contain a top-level operation object: %s", got)
	}
}

func TestOperationEnvelopeCarriesResultAndTerminalError(t *testing.T) {
	operationID := "op_alpha"
	envelope := NewOperationEnvelope(OperationEnvelopeSpec{
		OperationID:    operationID,
		OperationState: OperationStateFailed,
		Resource:       ResourceRef{Type: "repo", ID: "repo_unit"},
		Result:         map[string]any{"repo_id": "repo_unit"},
		Error: &StandardError{
			Code:          CodeCapabilityDenied,
			Message:       "denied",
			Retryable:     false,
			CorrelationID: "corr-1",
			OperationID:   &operationID,
			Details:       map[string]any{},
		},
	})

	got, err := MarshalOperationEnvelope(envelope)
	if err != nil {
		t.Fatalf("MarshalOperationEnvelope returned error: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("OperationEnvelope JSON did not decode: %v", err)
	}
	if _, ok := decoded["operation"]; ok {
		t.Fatalf("flat API OperationEnvelope must not include operation: %s", got)
	}
	if string(decoded["result"]) != `{"repo_id":"repo_unit"}` {
		t.Fatalf("result JSON = %s, want repo object", decoded["result"])
	}
	if string(decoded["error"]) == "null" {
		t.Fatalf("terminal error must be included in failed envelope: %s", got)
	}
}
