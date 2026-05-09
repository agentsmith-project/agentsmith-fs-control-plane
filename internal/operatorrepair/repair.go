package operatorrepair

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

type Action string

const ActionTerminalizeUnsupportedInterventionAsFailed Action = "terminalize_unsupported_intervention_as_failed"

const (
	OutcomeTerminalizedFailed = "terminalized_failed"

	RepairErrorCodeTerminalizedFailed = "OPERATION_REPAIR_TERMINALIZED_FAILED"
	UnsupportedRecoveryErrorCode      = "OPERATION_RECOVERY_REQUIRED"
)

var (
	ErrUnknownAction          = errors.New("unknown operator repair action")
	ErrMissingReason          = errors.New("missing operator repair reason")
	ErrMissingEvidenceRef     = errors.New("missing operator repair evidence ref")
	ErrMissingAffectedIDs     = errors.New("missing operator repair affected ids")
	ErrSensitiveRepairInput   = errors.New("sensitive operator repair input")
	ErrUnsafeIntervention     = errors.New("unsafe operator intervention repair")
	ErrOperationNotRepairable = errors.New("operation is not repairable")
	ErrAlreadyTerminal        = errors.New("operation is already terminal")
)

type Request struct {
	OperationID string            `json:"-"`
	Action      Action            `json:"action"`
	Reason      string            `json:"reason"`
	EvidenceRef string            `json:"evidence_ref"`
	AffectedIDs map[string]string `json:"affected_ids"`
}

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Result struct {
	Action        Action                     `json:"action"`
	OperationID   string                     `json:"operation_id"`
	Before        OperationStateSnapshot     `json:"before"`
	After         OperationStateSnapshot     `json:"after"`
	AuditEventID  string                     `json:"audit_event_id,omitempty"`
	RepairOutcome string                     `json:"repair_outcome"`
	Reason        string                     `json:"reason"`
	EvidenceRef   string                     `json:"evidence_ref"`
	AffectedIDs   map[string]string          `json:"affected_ids"`
	Operation     operations.OperationRecord `json:"operation,omitempty"`
}

type OperationStateSnapshot struct {
	State string `json:"state"`
	Phase string `json:"phase,omitempty"`
}

type CommitRequest struct {
	OperationID string
	Before      operations.OperationRecord
	After       operations.OperationRecord
	Event       audit.Event
	Now         time.Time
}

func ValidateRequest(req Request) error {
	if req.Action != ActionTerminalizeUnsupportedInterventionAsFailed {
		return ErrUnknownAction
	}
	if strings.TrimSpace(req.Reason) == "" {
		return ErrMissingReason
	}
	if strings.TrimSpace(req.EvidenceRef) == "" {
		return ErrMissingEvidenceRef
	}
	if len(req.AffectedIDs) == 0 {
		return ErrMissingAffectedIDs
	}
	if sensitiveText(req.Reason) || sensitiveText(req.EvidenceRef) {
		return ErrSensitiveRepairInput
	}
	for key, value := range req.AffectedIDs {
		if sensitiveText(key) || sensitiveText(value) {
			return ErrSensitiveRepairInput
		}
	}
	return nil
}

func ValidateEligibleOperation(record operations.OperationRecord) error {
	if record.State.IsTerminal() {
		return ErrAlreadyTerminal
	}
	if record.State != operations.OperationStateOperatorInterventionRequired {
		return ErrUnsafeIntervention
	}
	if strings.TrimSpace(record.LeaseOwner) != "" || record.LeaseExpiresAt != nil {
		return ErrUnsafeIntervention
	}
	if strings.TrimSpace(record.SessionFenceID) != "" {
		return ErrUnsafeIntervention
	}
	if ambiguousPhase(record.Phase) {
		return ErrUnsafeIntervention
	}
	if record.Error == nil || record.Error.Code != UnsupportedRecoveryErrorCode {
		return ErrUnsafeIntervention
	}
	if !hasUnsupportedRecoveryMarker(record.Error.Details) {
		return ErrUnsafeIntervention
	}
	return nil
}

func BuildFailedRepair(before operations.OperationRecord, req Request, actor Actor, now time.Time) (Result, error) {
	if err := ValidateRequest(req); err != nil {
		return Result{}, err
	}
	if err := ValidateEligibleOperation(before); err != nil {
		return Result{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	after := before.Sanitized()
	after.State = operations.OperationStateFailed
	after.Phase = "operator_repair_terminalized_failed"
	after.LeaseOwner = ""
	after.LeaseExpiresAt = nil
	after.FinishedAt = &now
	after.Error = &operations.OperationError{
		Code:          RepairErrorCodeTerminalizedFailed,
		Message:       "operation was terminalized by allowlisted operator repair",
		Retryable:     false,
		CorrelationID: before.CorrelationID,
		OperationID:   before.ID,
		Details: map[string]any{
			"repair_action": req.Action,
			"reason":        req.Reason,
			"evidence_ref":  req.EvidenceRef,
			"affected_ids":  req.AffectedIDs,
			"operator": map[string]any{
				"type": actor.Type,
				"id":   actor.ID,
			},
			"before_state": before.State.String(),
			"after_state":  operations.OperationStateFailed.String(),
		},
	}
	after.VerificationResult = map[string]any{
		"repair_action":  req.Action,
		"reason":         req.Reason,
		"evidence_ref":   req.EvidenceRef,
		"affected_ids":   req.AffectedIDs,
		"before_state":   before.State.String(),
		"before_phase":   before.Phase,
		"after_state":    operations.OperationStateFailed.String(),
		"after_phase":    after.Phase,
		"repair_outcome": OutcomeTerminalizedFailed,
	}
	after = after.Sanitized()
	return Result{
		Action:      req.Action,
		OperationID: before.ID,
		Before: OperationStateSnapshot{
			State: before.State.String(),
			Phase: before.Phase,
		},
		After: OperationStateSnapshot{
			State: after.State.String(),
			Phase: after.Phase,
		},
		RepairOutcome: OutcomeTerminalizedFailed,
		Reason:        audit.RedactString(req.Reason),
		EvidenceRef:   audit.RedactString(req.EvidenceRef),
		AffectedIDs:   redactStringMap(req.AffectedIDs),
		Operation:     after,
	}, nil
}

func NewAuditEvent(eventID string, before operations.OperationRecord, req Request, actor Actor, now time.Time) (audit.Event, error) {
	if strings.TrimSpace(eventID) == "" {
		return audit.Event{}, fmt.Errorf("missing audit event id")
	}
	if err := ValidateRequest(req); err != nil {
		return audit.Event{}, err
	}
	eventType, ok := audit.EventTypeForOperationType(before.Type.String())
	if !ok {
		return audit.Event{}, ErrUnsafeIntervention
	}
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            eventType,
		Time:            now.UTC(),
		CallerService:   before.CallerService,
		AuthorizedActor: audit.Actor{Type: actor.Type, ID: actor.ID},
		CorrelationID:   before.CorrelationID,
		OperationID:     before.ID,
		Resource:        audit.Resource{Type: before.Resource.Type, ID: before.Resource.ID, NamespaceID: before.NamespaceID},
		Outcome:         audit.OutcomeFailed,
		Reason:          "operator_repair_terminalized_failed",
		Details: map[string]any{
			"repair_action": req.Action,
			"reason":        req.Reason,
			"evidence_ref":  req.EvidenceRef,
			"affected_ids":  req.AffectedIDs,
			"before_state":  before.State.String(),
			"before_phase":  before.Phase,
			"after_state":   operations.OperationStateFailed.String(),
		},
	}), nil
}

func ResultWithAudit(result Result, auditEventID string) Result {
	result.AuditEventID = strings.TrimSpace(auditEventID)
	return result
}

func hasUnsupportedRecoveryMarker(details map[string]any) bool {
	for _, key := range []string{"reason", "recovery_reason"} {
		value := strings.ToLower(strings.TrimSpace(fmt.Sprint(details[key])))
		if strings.Contains(value, "unsupported_operation_recovery") ||
			strings.Contains(value, "disabled") ||
			strings.Contains(value, "unsupported") ||
			strings.Contains(value, "worker_recovery_disabled") {
			return true
		}
	}
	return false
}

func ambiguousPhase(phase string) bool {
	phase = strings.ToLower(strings.TrimSpace(phase))
	return strings.Contains(phase, "writer_fenced") ||
		strings.Contains(phase, "consuming") ||
		strings.Contains(phase, "discarding") ||
		strings.Contains(phase, "committed")
}

func sensitiveText(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, marker := range []string{"bearer ", "secret", "password", "token", "/var/lib", ".jvs", "raw_root", "secretref"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func redactStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		redacted, _ := observability.RedactString(value)
		out[key] = redacted
	}
	return out
}
