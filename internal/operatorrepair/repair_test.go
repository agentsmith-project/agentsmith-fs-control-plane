package operatorrepair

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestOperatorRepairRejectsUnknownAction(t *testing.T) {
	req := validRequest()
	req.Action = Action("release_everything")

	if err := ValidateRequest(req); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("ValidateRequest error = %v, want ErrUnknownAction", err)
	}
}

func TestOperatorRepairRequiresReasonEvidenceAndAffectedIDs(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Request)
		want error
	}{
		{name: "missing reason", edit: func(req *Request) { req.Reason = "" }, want: ErrMissingReason},
		{name: "missing evidence", edit: func(req *Request) { req.EvidenceRef = "" }, want: ErrMissingEvidenceRef},
		{name: "missing affected ids", edit: func(req *Request) { req.AffectedIDs = nil }, want: ErrMissingAffectedIDs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.edit(&req)

			if err := ValidateRequest(req); !errors.Is(err, tt.want) {
				t.Fatalf("ValidateRequest error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestOperatorRepairRejectsSecretShapedReasonOrEvidenceRef(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Request)
	}{
		{name: "reason bearer", edit: func(req *Request) { req.Reason = "token Bearer secret-value" }},
		{name: "evidence raw path", edit: func(req *Request) { req.EvidenceRef = "/var/lib/afscp/secret-root/op" }},
		{name: "affected secret", edit: func(req *Request) { req.AffectedIDs = map[string]string{"secret_ref": "SecretRef/db"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.edit(&req)

			if err := ValidateRequest(req); !errors.Is(err, ErrSensitiveRepairInput) {
				t.Fatalf("ValidateRequest error = %v, want ErrSensitiveRepairInput", err)
			}
		})
	}
}

func TestOperatorRepairRejectsAmbiguousOrFencedIntervention(t *testing.T) {
	tests := []struct {
		name string
		edit func(*operations.OperationRecord)
	}{
		{name: "active lease", edit: func(record *operations.OperationRecord) {
			record.LeaseOwner = "worker-a"
			expires := time.Now().Add(time.Minute)
			record.LeaseExpiresAt = &expires
		}},
		{name: "session fence", edit: func(record *operations.OperationRecord) { record.SessionFenceID = "fence_123" }},
		{name: "restore ambiguity", edit: func(record *operations.OperationRecord) {
			record.Phase = operations.OperationPhaseRestoreWriterFenced
		}},
		{name: "missing unsupported marker", edit: func(record *operations.OperationRecord) {
			record.Error.Details["reason"] = "manual_investigation_required"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := eligibleRecord()
			tt.edit(&record)

			if err := ValidateEligibleOperation(record); !errors.Is(err, ErrUnsafeIntervention) {
				t.Fatalf("ValidateEligibleOperation error = %v, want ErrUnsafeIntervention", err)
			}
		})
	}
}

func TestOperatorRepairBuildsFailedRecordWithRedactedBeforeAfter(t *testing.T) {
	before := eligibleRecord()
	req := validRequest()
	req.Reason = "unsupported operation recovery reviewed"
	req.EvidenceRef = "docs/runbooks/ga-runbooks.md#op-123"
	req.AffectedIDs = map[string]string{"operation_id": "op_123", "repo_id": "repo_123"}
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)

	result, err := BuildFailedRepair(before, req, Actor{Type: "operator", ID: "ops-user"}, now)
	if err != nil {
		t.Fatalf("BuildFailedRepair: %v", err)
	}
	if result.Operation.State != operations.OperationStateFailed || result.Operation.FinishedAt == nil {
		t.Fatalf("after state/finished = %q/%v, want failed with finished_at", result.Operation.State, result.Operation.FinishedAt)
	}
	if result.Before.State != operations.OperationStateOperatorInterventionRequired.String() {
		t.Fatalf("before state = %q, want operator_intervention_required", result.Before.State)
	}
	if result.Operation.Error == nil || result.Operation.Error.Code != "OPERATION_REPAIR_TERMINALIZED_FAILED" {
		t.Fatalf("after error = %#v, want repair terminalized code", result.Operation.Error)
	}
	rendered := strings.ToLower(mustMarshalRepairResult(t, result))
	for _, forbidden := range []string{"secret", "bearer", "/var/lib", ".jvs"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("repair result leaked %q in %s", forbidden, rendered)
		}
	}
}

func validRequest() Request {
	return Request{
		OperationID: "op_123",
		Action:      ActionTerminalizeUnsupportedInterventionAsFailed,
		Reason:      "unsupported operation recovery reviewed",
		EvidenceRef: "docs/runbooks/ga-runbooks.md#op-123",
		AffectedIDs: map[string]string{"operation_id": "op_123", "repo_id": "repo_123"},
	}
}

func eligibleRecord() operations.OperationRecord {
	return operations.OperationRecord{
		ID:            "op_123",
		Type:          operations.OperationRepoCreate,
		State:         operations.OperationStateOperatorInterventionRequired,
		Phase:         operations.OperationPhaseRepoCreateValidate,
		CorrelationID: "corr_123",
		CallerService: "afscp-api",
		Resource:      operations.ResourceRef{Type: "repo", ID: "repo_123"},
		NamespaceID:   "ns_123",
		RepoID:        "repo_123",
		Error: &operations.OperationError{
			Code:        "OPERATION_RECOVERY_REQUIRED",
			Message:     "operation recovery is unsupported; operator intervention required",
			OperationID: "op_123",
			Details: map[string]any{
				"reason":   "unsupported_operation_recovery",
				"evidence": "worker_recovery_disabled",
			},
		},
		VerificationResult: map[string]any{
			"reason":   "unsupported_operation_recovery",
			"evidence": "worker_recovery_disabled",
		},
	}
}

func mustMarshalRepairResult(t *testing.T, result Result) string {
	t.Helper()
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal repair result: %v", err)
	}
	return string(body)
}
