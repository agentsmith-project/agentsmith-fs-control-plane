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

type RestorePlanActiveGateReader interface {
	GetActiveRestorePlanByRepo(ctx context.Context, repoID string) (restoreplan.Plan, error)
}

type RestoreHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	FenceReader       RepoFenceReader
	MutationGate      RepoJVSMutationGateReader
	RestorePlanReader RestorePlanActiveGateReader
	IntakeStore       RestoreOperationIntakeStore
	IntakeLookupStore OperationIdempotencyLookupStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type RestoreAdmitHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	FenceReader       RepoFenceReader
	MutationGate      RepoJVSMutationGateReader
	RestorePlanReader RestorePlanActiveGateReader
	HistoryReader     SavePointHistoryReader
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	AdmissionDisabled bool
	AuditSink         audit.Sink
}

type restoreRequestDTO struct {
	SavePointID                    string `json:"save_point_id"`
	DiscardUnsavedChangesConfirmed bool   `json:"discard_unsaved_changes_confirmed"`
}

type RestoreAdmitResponse struct {
	Admitted      bool   `json:"admitted"`
	RepoID        string `json:"repo_id"`
	SavePointID   string `json:"save_point_id"`
	OperationType string `json:"operation_type"`
}

type restoreCanonicalRequest struct {
	RepoID                         string `json:"repo_id"`
	SavePointID                    string `json:"save_point_id"`
	DiscardUnsavedChangesConfirmed bool   `json:"discard_unsaved_changes_confirmed"`
}

func RestoreHandler(config RestoreHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("restore")
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
		if typed, ok := config.IntakeStore.(RestorePlanActiveGateReader); ok {
			planReader = typed
		}
	}
	leaf := restoreLeafHandler{
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
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, restoreRouteResolver{route: route}, config.AllowedCallers, config.AuditSink)
}

func RestoreAdmitHandler(config RestoreAdmitHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("restoreAdmit")
	mutationGate := config.MutationGate
	planReader := config.RestorePlanReader
	leaf := restoreAdmitLeafHandler{
		route:             route,
		repoReader:        config.RepoReader,
		namespaceReader:   config.NamespaceReader,
		bindingReader:     config.BindingReader,
		fenceReader:       config.FenceReader,
		mutationGate:      mutationGate,
		planReader:        planReader,
		historyReader:     config.HistoryReader,
		admissionDisabled: config.AdmissionDisabled,
		sink:              config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, restoreRouteResolver{route: route}, config.AllowedCallers, config.AuditSink)
}

type restoreRouteResolver struct {
	route RouteMetadata
}

func (resolver restoreRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	if strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	_, ok := RoutePathParams(resolver.route.Path, r.URL.Path)
	return resolver.route, ok
}

type restoreLeafHandler struct {
	route           RouteMetadata
	repoReader      RepoReader
	namespaceReader NamespaceReader
	bindingReader   NamespaceVolumeBindingReader
	fenceReader     RepoFenceReader
	mutationGate    RepoJVSMutationGateReader
	planReader      RestorePlanActiveGateReader
	intakeStore     RestoreOperationIntakeStore
	lookupStore     OperationIdempotencyLookupStore
	operationID     OperationIDGenerator
	now             func() time.Time
	sink            audit.Sink
}

type restoreAdmitLeafHandler struct {
	route             RouteMetadata
	repoReader        RepoReader
	namespaceReader   NamespaceReader
	bindingReader     NamespaceVolumeBindingReader
	fenceReader       RepoFenceReader
	mutationGate      RepoJVSMutationGateReader
	planReader        RestorePlanActiveGateReader
	historyReader     SavePointHistoryReader
	admissionDisabled bool
	sink              audit.Sink
}

func (handler restoreLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeRestoreError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	params, ok := RoutePathParams(handler.route.Path, r.URL.Path)
	if !ok {
		writeRestoreError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
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
		writeRestoreError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	body, err := decodeRestoreRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid restore request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if err := operations.ValidateSavePointID(body.SavePointID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid save point id", []string{"invalid_save_point_id"}, handler.sink)
		return
	}
	if !body.DiscardUnsavedChangesConfirmed {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeRestoreConfirmationRequired, http.StatusBadRequest, "discard unsaved changes confirmation is required", []string{"discard_unsaved_changes_confirmation_required"}, handler.sink)
		return
	}
	canonical := restoreCanonicalRequest{RepoID: repoID, SavePointID: body.SavePointID, DiscardUnsavedChangesConfirmed: true}
	if handler.writeExistingIdempotentOperation(w, r, requestContext, namespaceID, canonical) {
		return
	}
	repo, namespace, binding, heldFences, ok := handler.loadMetadata(w, r, namespaceID, repoID)
	if !ok {
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: heldFences, Intent: repoaccess.IntentRestoreRun, Mode: repoaccess.ModeReadWrite})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, handler.route, requestContext, decision, handler.sink)
		return
	}
	if !handler.checkJVSMutationGate(w, r, repoID) {
		return
	}
	if !handler.checkActivePlanGate(w, r, repoID) {
		return
	}

	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	envelope, intakeErr := CreateOrReuseRestoreOperationIntake(r.Context(), handler.intakeStore, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		Resource:            operations.ResourceRef{Type: "repo", ID: repoID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"save_point_id": body.SavePointID, "discard_unsaved_changes_confirmed": true},
		Phase:               operations.OperationPhaseRestoreValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler restoreAdmitLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeRestoreError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	if err := auth.ValidateRequestContextForRoute(requestContext, auth.RouteValidation{Class: auth.RouteClassNamespaceBound, Mutating: true}); err != nil {
		writeAuthGateValidationError(w, r, handler.route, requestContext, err, handler.sink)
		return
	}
	params, ok := RoutePathParams(handler.route.Path, r.URL.Path)
	if !ok {
		writeRestoreError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
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
	if handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.fenceReader == nil || handler.mutationGate == nil || handler.planReader == nil {
		writeRestoreError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	body, err := decodeRestoreRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid restore request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if err := operations.ValidateSavePointID(body.SavePointID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid save point id", []string{"invalid_save_point_id"}, handler.sink)
		return
	}
	if !body.DiscardUnsavedChangesConfirmed {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeRestoreConfirmationRequired, http.StatusBadRequest, "discard unsaved changes confirmation is required", []string{"discard_unsaved_changes_confirmation_required"}, handler.sink)
		return
	}
	repo, namespace, binding, heldFences, ok := handler.loadMetadata(w, r, namespaceID, repoID)
	if !ok {
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: heldFences, Intent: repoaccess.IntentRestoreRun, Mode: repoaccess.ModeReadWrite})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, handler.route, requestContext, decision, handler.sink)
		return
	}
	if !handler.checkJVSMutationGate(w, r, repoID) {
		return
	}
	if !handler.checkActivePlanGate(w, r, repoID) {
		return
	}
	if !handler.checkSavePointAvailable(w, r, namespaceID, repoID, body.SavePointID) {
		return
	}
	if handler.admissionDisabled {
		writeRestoreAdmitCapabilityDenied(w, r, handler.route, requestContext, handler.sink)
		return
	}
	_ = writeJSON(w, http.StatusOK, RestoreAdmitResponse{
		Admitted:      true,
		RepoID:        repoID,
		SavePointID:   body.SavePointID,
		OperationType: string(operations.OperationRestore),
	})
}

func (handler restoreLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, requestContext auth.RequestContext, namespaceID string, canonical any) bool {
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		writeRestoreError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, operations.OperationRestore, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return true
	}
	if record.RequestHash != requestHash {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return true
	}
	_ = writeJSON(w, http.StatusAccepted, operationEnvelopeFromRecord(record))
	return true
}

func (handler restoreAdmitLeafHandler) loadMetadata(w http.ResponseWriter, r *http.Request, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, bool) {
	return restoreLeafHandler{
		route:           handler.route,
		repoReader:      handler.repoReader,
		namespaceReader: handler.namespaceReader,
		bindingReader:   handler.bindingReader,
		fenceReader:     handler.fenceReader,
		sink:            handler.sink,
	}.loadMetadata(w, r, namespaceID, repoID)
}

func (handler restoreAdmitLeafHandler) checkJVSMutationGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	return restoreLeafHandler{mutationGate: handler.mutationGate}.checkJVSMutationGate(w, r, repoID)
}

func (handler restoreAdmitLeafHandler) checkActivePlanGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	return restoreLeafHandler{planReader: handler.planReader}.checkActivePlanGate(w, r, repoID)
}

func (handler restoreAdmitLeafHandler) checkSavePointAvailable(w http.ResponseWriter, r *http.Request, namespaceID, repoID, savePointID string) bool {
	if handler.historyReader == nil {
		writeRestoreError(w, r, http.StatusForbidden, CodeCapabilityDenied, "save point history capability is not configured", false)
		return false
	}
	history, err := handler.historyReader.ListSavePoints(r.Context(), namespaceID, repoID)
	if err != nil {
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeJVSCommandFailed, "save point history is unavailable", true)
		return false
	}
	for _, savePoint := range history.SavePoints {
		if savePoint.RepoID != repoID || strings.TrimSpace(savePoint.SavePointID) == "" || strings.TrimSpace(savePoint.CreatedAt) == "" {
			writeRestoreError(w, r, http.StatusServiceUnavailable, CodeJVSCommandFailed, "save point history is unavailable", true)
			return false
		}
		if savePoint.SavePointID == savePointID {
			return true
		}
	}
	writeRestoreError(w, r, http.StatusNotFound, CodeJVSCommandFailed, "save point was not found", false)
	return false
}

func (handler restoreLeafHandler) loadMetadata(w http.ResponseWriter, r *http.Request, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRestoreError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
			return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
		}
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	if repo.NamespaceID != namespaceID {
		writeRestoreError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	namespace, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	binding, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	held, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	return repo, namespace, binding, repoAccessFencesFromStore(held), true
}

func (handler restoreLeafHandler) checkJVSMutationGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	inProgress, err := handler.mutationGate.RepoHasNonTerminalJVSMutation(r.Context(), repoID)
	if err != nil {
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if inProgress {
		writeRestoreError(w, r, http.StatusConflict, CodeRepoJVSMutationInProgress, "repo JVS mutation is in progress", true)
		return false
	}
	return true
}

func (handler restoreLeafHandler) checkActivePlanGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	_, err := handler.planReader.GetActiveRestorePlanByRepo(r.Context(), repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true
		}
		writeRestoreError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	writeRestoreError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "active restore plan requires operator recovery", true)
	return false
}

func decodeRestoreRequest(r *http.Request) (restoreRequestDTO, error) {
	var body restoreRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return restoreRequestDTO{}, err
	}
	body.SavePointID = strings.TrimSpace(body.SavePointID)
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return restoreRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return restoreRequestDTO{}, err
	}
	return body, nil
}

func writeRestoreError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}

func writeRestoreAdmitCapabilityDenied(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, sink audit.Sink) {
	message := "direct restore admission is disabled"
	validationErrors := []string{"direct_restore_admission_disabled"}
	var operationID *string
	if route.OperationID != "" {
		operationID = &route.OperationID
	}
	envelope := NewErrorEnvelope(
		CodeCapabilityDenied,
		message,
		false,
		CorrelationIDFromRequest(r),
		operationID,
		map[string]any{"validation_errors": validationErrors},
	)
	_ = WriteErrorEnvelope(w, http.StatusForbidden, envelope)
	emitDeniedAuditEvent(r.Context(), sink, r, deniedAuditEvent{
		Type:             audit.EventTypeCapabilityDenied,
		Route:            route,
		Status:           http.StatusForbidden,
		Code:             CodeCapabilityDenied,
		Reason:           message,
		ValidationErrors: validationErrors,
		RequestContext:   requestContext,
	})
}
