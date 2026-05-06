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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

type RestorePreviewPlanGateReader interface {
	GetActiveRestorePlanByRepo(ctx context.Context, repoID string) (restoreplan.Plan, error)
}

type RestorePreviewHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	FenceReader       RepoFenceReader
	MutationGate      RepoJVSMutationGateReader
	RestorePlanReader RestorePreviewPlanGateReader
	IntakeStore       RestorePreviewOperationIntakeStore
	IntakeLookupStore OperationIdempotencyLookupStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type restorePreviewRequestDTO struct {
	SavePointID string `json:"save_point_id"`
}

type restorePreviewCanonicalRequest struct {
	RepoID      string `json:"repo_id"`
	SavePointID string `json:"save_point_id"`
}

func RestorePreviewHandler(config RestorePreviewHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("restorePreview")
	lookupStore := config.IntakeLookupStore
	if lookupStore == nil {
		if typed, ok := config.IntakeStore.(OperationIdempotencyLookupStore); ok {
			lookupStore = typed
		}
	}
	mutationGate := config.MutationGate
	if mutationGate == nil {
		if typed, ok := config.IntakeStore.(RepoJVSMutationGateReader); ok {
			mutationGate = typed
		}
	}
	planReader := config.RestorePlanReader
	if planReader == nil {
		if typed, ok := config.IntakeStore.(RestorePreviewPlanGateReader); ok {
			planReader = typed
		}
	}
	leaf := restorePreviewLeafHandler{
		route:           route,
		repoReader:      config.RepoReader,
		namespaceReader: config.NamespaceReader,
		bindingReader:   config.BindingReader,
		fenceReader:     config.FenceReader,
		mutationGate:    mutationGate,
		planReader:      planReader,
		intakeStore:     config.IntakeStore,
		lookupStore:     lookupStore,
		operationID:     config.OperationID,
		now:             config.Now,
		sink:            config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, restorePreviewRouteResolver{route: route}, config.AllowedCallers, config.AuditSink)
}

type restorePreviewRouteResolver struct {
	route RouteMetadata
}

func (resolver restorePreviewRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	if strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	_, ok := RoutePathParams(resolver.route.Path, r.URL.Path)
	return resolver.route, ok
}

type restorePreviewLeafHandler struct {
	route           RouteMetadata
	repoReader      RepoReader
	namespaceReader NamespaceReader
	bindingReader   NamespaceVolumeBindingReader
	fenceReader     RepoFenceReader
	mutationGate    RepoJVSMutationGateReader
	planReader      RestorePreviewPlanGateReader
	intakeStore     RestorePreviewOperationIntakeStore
	lookupStore     OperationIdempotencyLookupStore
	operationID     OperationIDGenerator
	now             func() time.Time
	sink            audit.Sink
}

func (handler restorePreviewLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeRestorePreviewError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	params, ok := RoutePathParams(handler.route.Path, r.URL.Path)
	if !ok {
		writeRestorePreviewError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
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
	if handler.intakeStore == nil || handler.lookupStore == nil || handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.fenceReader == nil || handler.mutationGate == nil || handler.planReader == nil {
		writeRestorePreviewError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	body, err := decodeRestorePreviewRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid restore preview request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if err := operations.ValidateSavePointID(body.SavePointID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid save point id", []string{"invalid_save_point_id"}, handler.sink)
		return
	}
	canonical := restorePreviewCanonicalRequest{RepoID: repoID, SavePointID: body.SavePointID}
	if handler.writeExistingIdempotentOperation(w, r, requestContext, namespaceID, canonical) {
		return
	}
	repo, namespace, binding, heldFences, ok := handler.loadMetadata(w, r, namespaceID, repoID)
	if !ok {
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: heldFences, Intent: repoaccess.IntentSavePointCreate, Mode: repoaccess.ModeReadOnly})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, handler.route, requestContext, decision, handler.sink)
		return
	}
	if !handler.checkJVSMutationGate(w, r, repoID) {
		return
	}
	if !handler.checkActiveRestorePlanGate(w, r, repoID) {
		return
	}

	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	envelope, intakeErr := CreateOrReuseRestorePreviewOperationIntake(r.Context(), handler.intakeStore, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		Resource:            operations.ResourceRef{Type: "repo", ID: repoID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"save_point_id": body.SavePointID},
		Phase:               operations.OperationPhaseRestorePreviewValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler restorePreviewLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, requestContext auth.RequestContext, namespaceID string, canonical any) bool {
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		writeRestorePreviewError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, operations.OperationRestorePreview, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		writeRestorePreviewError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return true
	}
	if record.RequestHash != requestHash {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return true
	}
	_ = writeJSON(w, http.StatusAccepted, operationEnvelopeFromRecord(record))
	return true
}

func (handler restorePreviewLeafHandler) loadMetadata(w http.ResponseWriter, r *http.Request, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRestorePreviewError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
			return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
		}
		writeRestorePreviewError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	if repo.NamespaceID != namespaceID {
		writeRestorePreviewError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	namespace, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		writeRestorePreviewError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	binding, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		writeRestorePreviewError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	held, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		writeRestorePreviewError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	return repo, namespace, binding, repoAccessFencesFromStore(held), true
}

func (handler restorePreviewLeafHandler) checkJVSMutationGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	inProgress, err := handler.mutationGate.RepoHasNonTerminalJVSMutation(r.Context(), repoID)
	if err != nil {
		writeRestorePreviewError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if inProgress {
		writeRestorePreviewError(w, r, http.StatusConflict, CodeRepoJVSMutationInProgress, "repo JVS mutation is in progress", true)
		return false
	}
	return true
}

func (handler restorePreviewLeafHandler) checkActiveRestorePlanGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	_, err := handler.planReader.GetActiveRestorePlanByRepo(r.Context(), repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true
		}
		writeRestorePreviewError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	writeRestorePreviewError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "active restore plan requires operator recovery", true)
	return false
}

func decodeRestorePreviewRequest(r *http.Request) (restorePreviewRequestDTO, error) {
	var body restorePreviewRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return restorePreviewRequestDTO{}, err
	}
	body.SavePointID = strings.TrimSpace(body.SavePointID)
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return restorePreviewRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return restorePreviewRequestDTO{}, err
	}
	return body, nil
}

func writeRestorePreviewError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
