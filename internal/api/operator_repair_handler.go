package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operatorrepair"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

type OperatorRepairStore interface {
	ReadOperationForRepair(ctx context.Context, operationID string) (operations.OperationRecord, error)
	CommitOperatorRepairFailed(ctx context.Context, request operatorrepair.CommitRequest) (operations.OperationRecord, error)
}

type OperatorRepairHandlerConfig struct {
	Store              OperatorRepairStore
	PrincipalResolver  PrincipalResolver
	AllowedCallers     AllowedCallerPolicy
	GenerateAuditEvent func() string
	Now                func() time.Time
	AuditSink          audit.Sink
}

func OperatorRepairHandler(config OperatorRepairHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("repairOperation")
	allowedCallers := config.AllowedCallers
	if allowedCallers == nil {
		allowedCallers = operationInspectionMissingPolicy{}
	}
	leaf := operatorRepairLeafHandler{
		route:              route,
		store:              config.Store,
		generateAuditEvent: config.GenerateAuditEvent,
		now:                config.Now,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, operationInspectionRouteResolver{route: route}, allowedCallers, config.AuditSink)
}

type operatorRepairLeafHandler struct {
	route              RouteMetadata
	store              OperatorRepairStore
	generateAuditEvent func() string
	now                func() time.Time
}

func (handler operatorRepairLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeOperationInspectionError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	operationID, ok := operationInspectionOperationID(r, handler.route)
	if !ok {
		writeOperationInspectionError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, operationID); err != nil {
		writeValidationError(w, r, handler.route, CodeInvalidID, http.StatusBadRequest, "invalid operation id", []string{"invalid_operation_id"})
		return
	}
	if handler.store == nil {
		writeOperationInspectionError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}

	var req operatorrepair.Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeOperationInspectionError(w, r, http.StatusBadRequest, CodeInvalidID, "invalid operator repair request", false)
		return
	}
	req.OperationID = operationID
	if err := operatorrepair.ValidateRequest(req); err != nil {
		writeOperationInspectionError(w, r, http.StatusBadRequest, CodeInvalidID, "invalid operator repair request", false)
		return
	}

	before, err := handler.store.ReadOperationForRepair(r.Context(), operationID)
	if err != nil {
		handler.writeRepairError(w, r, err)
		return
	}
	if before.State.IsTerminal() {
		response := operatorrepair.Result{
			Action:        req.Action,
			OperationID:   operationID,
			Before:        operatorrepair.OperationStateSnapshot{State: before.State.String(), Phase: before.Phase},
			After:         operatorrepair.OperationStateSnapshot{State: before.State.String(), Phase: before.Phase},
			RepairOutcome: "already_terminal",
			Reason:        audit.RedactString(req.Reason),
			EvidenceRef:   audit.RedactString(req.EvidenceRef),
			AffectedIDs:   req.AffectedIDs,
		}
		_ = writeJSON(w, http.StatusConflict, response)
		return
	}
	now := handler.currentTime()
	actor := operatorrepair.Actor{Type: requestContext.Actor.Type, ID: requestContext.Actor.ID}
	result, err := operatorrepair.BuildFailedRepair(before, req, actor, now)
	if err != nil {
		handler.writeRepairError(w, r, err)
		return
	}
	eventID := handler.auditEventID()
	event, err := operatorrepair.NewAuditEvent(eventID, before, req, actor, now)
	if err != nil {
		handler.writeRepairError(w, r, err)
		return
	}
	updated, err := handler.store.CommitOperatorRepairFailed(r.Context(), operatorrepair.CommitRequest{
		OperationID: operationID,
		Before:      before,
		After:       result.Operation,
		Event:       event,
		Now:         now,
	})
	if err != nil {
		handler.writeRepairError(w, r, err)
		return
	}
	result.Operation = pruneOperationInspectionRecord(updated)
	result = operatorrepair.ResultWithAudit(result, eventID)
	_ = writeJSON(w, http.StatusOK, result)
}

func (handler operatorRepairLeafHandler) currentTime() time.Time {
	if handler.now != nil {
		return handler.now().UTC()
	}
	return time.Now().UTC()
}

func (handler operatorRepairLeafHandler) auditEventID() string {
	if handler.generateAuditEvent != nil {
		if id := strings.TrimSpace(handler.generateAuditEvent()); id != "" {
			return id
		}
	}
	return "audit_" + time.Now().UTC().Format("20060102150405.000000000")
}

func (handler operatorRepairLeafHandler) writeRepairError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, operatorrepair.ErrOperationNotRepairable):
		writeOperationInspectionError(w, r, http.StatusNotFound, CodeOperationNotFound, "operation was not found", false)
	case errors.Is(err, operatorrepair.ErrAlreadyTerminal):
		writeOperationInspectionError(w, r, http.StatusConflict, CodeIdempotencyConflict, "operation is already terminal", false)
	case errors.Is(err, operatorrepair.ErrUnsafeIntervention):
		writeOperationInspectionError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "operator repair preconditions were not met", false)
	case errors.Is(err, operatorrepair.ErrUnknownAction),
		errors.Is(err, operatorrepair.ErrMissingReason),
		errors.Is(err, operatorrepair.ErrMissingEvidenceRef),
		errors.Is(err, operatorrepair.ErrMissingAffectedIDs),
		errors.Is(err, operatorrepair.ErrSensitiveRepairInput):
		writeOperationInspectionError(w, r, http.StatusBadRequest, CodeInvalidID, "invalid operator repair request", false)
	default:
		writeOperationInspectionError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
	}
}
