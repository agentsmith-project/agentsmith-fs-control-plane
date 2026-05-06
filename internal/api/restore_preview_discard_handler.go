package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

type RestorePreviewDiscardMetadataReader interface {
	GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error)
	GetRestorePlanByPreviewOperation(ctx context.Context, previewOperationID string) (restoreplan.Plan, error)
}

type RestorePreviewDiscardHandlerConfig struct {
	MetadataReader    RestorePreviewDiscardMetadataReader
	IntakeStore       OperationIntakeStore
	IntakeLookupStore OperationIdempotencyLookupStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type restorePreviewDiscardRequestDTO struct {
	PreviewOperationID string `json:"preview_operation_id"`
}

type restorePreviewDiscardCanonicalRequest struct {
	RepoID             string `json:"repo_id"`
	PreviewOperationID string `json:"preview_operation_id"`
}

func RestorePreviewDiscardHandler(config RestorePreviewDiscardHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("restorePreviewDiscard")
	lookupStore := config.IntakeLookupStore
	if lookupStore == nil {
		if typed, ok := config.IntakeStore.(OperationIdempotencyLookupStore); ok {
			lookupStore = typed
		}
	}
	reader := config.MetadataReader
	if reader == nil {
		if typed, ok := config.IntakeStore.(RestorePreviewDiscardMetadataReader); ok {
			reader = typed
		}
	}
	leaf := restorePreviewDiscardLeafHandler{route: route, metadataReader: reader, intakeStore: config.IntakeStore, lookupStore: lookupStore, operationID: config.OperationID, now: config.Now, sink: config.AuditSink}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, restorePreviewDiscardRouteResolver{route: route}, config.AllowedCallers, config.AuditSink)
}

type restorePreviewDiscardRouteResolver struct {
	route RouteMetadata
}

func (resolver restorePreviewDiscardRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	if strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	_, ok := RoutePathParams(resolver.route.Path, r.URL.Path)
	return resolver.route, ok
}

type restorePreviewDiscardLeafHandler struct {
	route          RouteMetadata
	metadataReader RestorePreviewDiscardMetadataReader
	intakeStore    OperationIntakeStore
	lookupStore    OperationIdempotencyLookupStore
	operationID    OperationIDGenerator
	now            func() time.Time
	sink           audit.Sink
}

func (handler restorePreviewDiscardLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeRestorePreviewDiscardError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	params, ok := RoutePathParams(handler.route.Path, r.URL.Path)
	if !ok {
		writeRestorePreviewDiscardError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	repoID := strings.TrimSpace(params["repoId"])
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid repo id", []string{"invalid_repo_id"}, handler.sink)
		return
	}
	namespaceID := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if namespaceID == "" {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace is required", []string{"missing_namespace_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if handler.metadataReader == nil || handler.intakeStore == nil || handler.lookupStore == nil {
		writeRestorePreviewDiscardError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	body, err := decodeRestorePreviewDiscardRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid restore preview discard request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, body.PreviewOperationID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid preview operation id", []string{"invalid_preview_operation_id"}, handler.sink)
		return
	}
	canonical := restorePreviewDiscardCanonicalRequest{RepoID: repoID, PreviewOperationID: body.PreviewOperationID}
	if handler.writeExistingIdempotentOperation(w, r, requestContext, namespaceID, canonical) {
		return
	}
	if !handler.validatePreviewAndPlan(w, r, namespaceID, repoID, body.PreviewOperationID) {
		return
	}

	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	envelope, intakeErr := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		Resource:            operations.ResourceRef{Type: "repo", ID: repoID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"preview_operation_id": body.PreviewOperationID},
		Phase:               operations.OperationPhaseRestorePreviewDiscardValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler restorePreviewDiscardLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, requestContext auth.RequestContext, namespaceID string, canonical any) bool {
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		writeRestorePreviewDiscardError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, operations.OperationRestorePreviewDiscard, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		writeRestorePreviewDiscardError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return true
	}
	if record.RequestHash != requestHash {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return true
	}
	_ = writeJSON(w, http.StatusAccepted, operationEnvelopeFromRecord(record))
	return true
}

func (handler restorePreviewDiscardLeafHandler) validatePreviewAndPlan(w http.ResponseWriter, r *http.Request, namespaceID, repoID, previewOperationID string) bool {
	preview, err := handler.metadataReader.GetOperation(r.Context(), previewOperationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRestorePreviewDiscardError(w, r, http.StatusNotFound, CodeOperationNotFound, "preview operation was not found", false)
			return false
		}
		writeRestorePreviewDiscardError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if preview.Type != operations.OperationRestorePreview || preview.State != operations.OperationStateSucceeded || preview.Phase != operations.OperationPhaseRestorePreviewCommitted || preview.NamespaceID != namespaceID || preview.RepoID != repoID || preview.Resource.Type != "repo" || preview.Resource.ID != repoID {
		writeRestorePreviewDiscardError(w, r, http.StatusNotFound, CodeOperationNotFound, "preview operation was not found", false)
		return false
	}
	plan, err := handler.metadataReader.GetRestorePlanByPreviewOperation(r.Context(), previewOperationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRestorePreviewDiscardError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "restore preview plan requires operator recovery", true)
			return false
		}
		writeRestorePreviewDiscardError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if plan.NamespaceID != namespaceID || plan.RepoID != repoID || plan.PreviewOperationID != previewOperationID || plan.Status != restoreplan.StatusPending {
		writeRestorePreviewDiscardError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "restore preview plan is not pending", true)
		return false
	}
	return true
}

func decodeRestorePreviewDiscardRequest(r *http.Request) (restorePreviewDiscardRequestDTO, error) {
	var body restorePreviewDiscardRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return restorePreviewDiscardRequestDTO{}, err
	}
	body.PreviewOperationID = strings.TrimSpace(body.PreviewOperationID)
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return restorePreviewDiscardRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return restorePreviewDiscardRequestDTO{}, err
	}
	return body, nil
}

func writeRestorePreviewDiscardError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
