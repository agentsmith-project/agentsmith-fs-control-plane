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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

type SavePointHistoryReader interface {
	ListSavePoints(ctx context.Context, namespaceID, repoID string) (SavePointHistory, error)
}

type SavePointSessionStateReader interface {
	ListExportSessionsByRepo(ctx context.Context, repoID string) ([]sessionstate.ExportSession, error)
	ListWorkloadMountBindingsByRepo(ctx context.Context, repoID string) ([]sessionstate.WorkloadMountBinding, error)
}

type RepoJVSMutationGateStatus = operations.RepoJVSMutationGateStatus

type RepoJVSMutationGateStatusReader interface {
	GetRepoJVSMutationGateStatus(ctx context.Context, repoID string) (RepoJVSMutationGateStatus, error)
}

type SavePointCreateRecoveryCapabilityReader interface {
	SavePointCreateRecoveryCapabilityReady(ctx context.Context, now time.Time) (bool, error)
}

type SavePointHistory struct {
	SavePoints []SavePointResponse
}

type SavePointResponse struct {
	SavePointID string `json:"save_point_id"`
	Message     string `json:"message"`
	CreatedAt   string `json:"created_at"`
	RepoID      string `json:"repo_id"`
}

type SavePointListResponse struct {
	SavePoints []SavePointResponse `json:"save_points"`
}

type SavePointHandlerConfig struct {
	RepoReader         RepoReader
	NamespaceReader    NamespaceReader
	BindingReader      NamespaceVolumeBindingReader
	FenceReader        RepoFenceReader
	HistoryReader      SavePointHistoryReader
	MutationGate       RepoJVSMutationGateStatusReader
	SessionStateReader SavePointSessionStateReader
	CreateReady        bool
	CreateReadyReader  SavePointCreateRecoveryCapabilityReader
	IntakeStore        OperationIntakeStore
	IntakeLookupStore  OperationIdempotencyLookupStore
	PrincipalResolver  PrincipalResolver
	AllowedCallers     AllowedCallerPolicy
	OperationID        OperationIDGenerator
	Now                func() time.Time
	AuditSink          audit.Sink
}

type savePointCreateRequestDTO struct {
	Message string `json:"message"`
}

type savePointCreateCanonicalRequest struct {
	RepoID  string `json:"repo_id"`
	Message string `json:"message"`
}

func SavePointHandler(config SavePointHandlerConfig) http.Handler {
	createRoute, _ := RouteMetadataByOperationID("createSavePoint")
	listRoute, _ := RouteMetadataByOperationID("listSavePoints")
	lookupStore := config.IntakeLookupStore
	if lookupStore == nil {
		if typed, ok := config.IntakeStore.(OperationIdempotencyLookupStore); ok {
			lookupStore = typed
		}
	}
	mutationGate := config.MutationGate
	if mutationGate == nil {
		if typed, ok := config.IntakeStore.(RepoJVSMutationGateStatusReader); ok {
			mutationGate = typed
		}
	}
	sessionReader := config.SessionStateReader
	if sessionReader == nil {
		if typed, ok := config.IntakeStore.(SavePointSessionStateReader); ok {
			sessionReader = typed
		}
	}
	createReadyReader := config.CreateReadyReader
	if createReadyReader == nil {
		if typed, ok := config.IntakeStore.(SavePointCreateRecoveryCapabilityReader); ok {
			createReadyReader = typed
		}
	}
	leaf := savePointLeafHandler{
		createRoute:       createRoute,
		listRoute:         listRoute,
		repoReader:        config.RepoReader,
		namespaceReader:   config.NamespaceReader,
		bindingReader:     config.BindingReader,
		fenceReader:       config.FenceReader,
		historyReader:     config.HistoryReader,
		mutationGate:      mutationGate,
		sessionReader:     sessionReader,
		createReady:       config.CreateReady,
		createReadyReader: createReadyReader,
		intakeStore:       config.IntakeStore,
		lookupStore:       lookupStore,
		operationID:       config.OperationID,
		now:               config.Now,
		sink:              config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, savePointRouteResolver{createRoute: createRoute, listRoute: listRoute}, config.AllowedCallers, config.AuditSink)
}

type savePointRouteResolver struct {
	createRoute RouteMetadata
	listRoute   RouteMetadata
}

func (resolver savePointRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range []RouteMetadata{resolver.createRoute, resolver.listRoute} {
		if route.Method == method {
			if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
				return route, true
			}
		}
	}
	return RouteMetadata{}, false
}

type savePointLeafHandler struct {
	createRoute       RouteMetadata
	listRoute         RouteMetadata
	repoReader        RepoReader
	namespaceReader   NamespaceReader
	bindingReader     NamespaceVolumeBindingReader
	fenceReader       RepoFenceReader
	historyReader     SavePointHistoryReader
	mutationGate      RepoJVSMutationGateStatusReader
	sessionReader     SavePointSessionStateReader
	createReady       bool
	createReadyReader SavePointCreateRecoveryCapabilityReader
	intakeStore       OperationIntakeStore
	lookupStore       OperationIdempotencyLookupStore
	operationID       OperationIDGenerator
	now               func() time.Time
	sink              audit.Sink
}

func (handler savePointLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeSavePointError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	route, ok := handler.routeForRequest(r)
	if !ok {
		writeSavePointError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	repoID := savePointRepoIDFromRoute(r, route)
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		writeSavePointValidationError(w, r, route, requestContext, CodeInvalidID, "invalid repo id", []string{"invalid_repo_id"}, handler.sink)
		return
	}
	namespaceID := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if namespaceID == "" {
		writeSavePointValidationError(w, r, route, requestContext, CodeResourceNamespaceMismatch, "request namespace is required", []string{"missing_namespace_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeSavePointValidationError(w, r, route, requestContext, CodeInvalidID, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if route.OperationID == "listSavePoints" {
		handler.serveList(w, r, route, requestContext, namespaceID, repoID)
		return
	}
	handler.serveCreate(w, r, route, requestContext, namespaceID, repoID)
}

func (handler savePointLeafHandler) routeForRequest(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range []RouteMetadata{handler.createRoute, handler.listRoute} {
		if route.Method == method {
			if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
				return route, true
			}
		}
	}
	return RouteMetadata{}, false
}

func (handler savePointLeafHandler) serveCreate(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID, repoID string) {
	if handler.intakeStore == nil || handler.lookupStore == nil || handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.fenceReader == nil {
		writeSavePointError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	body, err := decodeSavePointCreateRequest(r)
	if err != nil {
		writeSavePointValidationError(w, r, route, requestContext, CodeInvalidID, "invalid save point request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	canonical := savePointCreateCanonicalRequest{RepoID: repoID, Message: body.Message}
	if handler.writeExistingIdempotentOperation(w, r, route, requestContext, namespaceID, canonical) {
		return
	}
	if !handler.checkCreateReady(w, r) {
		return
	}
	repo, namespace, binding, heldFences, ok := handler.loadMetadata(w, r, route, requestContext, namespaceID, repoID)
	if !ok {
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: heldFences, Intent: repoaccess.IntentSavePointCreate, Mode: repoaccess.ModeReadOnly})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, route, requestContext, decision, handler.sink)
		return
	}
	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	envelope, intakeErr := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               route,
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		Resource:            operations.ResourceRef{Type: "repo", ID: repoID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"repo_id": repoID, "message": body.Message},
		Phase:               operations.OperationPhaseSavePointCreateValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler savePointLeafHandler) serveList(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID, repoID string) {
	if handler.historyReader == nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeCapabilityDenied, "save point history capability is not configured", false)
		return
	}
	if handler.mutationGate == nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return
	}
	if handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.fenceReader == nil {
		writeSavePointError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	repo, namespace, binding, heldFences, ok := handler.loadMetadata(w, r, route, requestContext, namespaceID, repoID)
	if !ok {
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: heldFences, Intent: repoaccess.IntentSavePointCreate, Mode: repoaccess.ModeReadOnly})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, route, requestContext, decision, handler.sink)
		return
	}
	if !handler.checkHistoryReadGate(w, r, repoID) {
		return
	}
	history, err := handler.historyReader.ListSavePoints(r.Context(), namespaceID, repoID)
	if err != nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeJVSCommandFailed, "save point history is unavailable", true)
		return
	}
	if !handler.checkHistoryReadGate(w, r, repoID) {
		return
	}
	for _, savePoint := range history.SavePoints {
		if savePoint.RepoID != repoID || strings.TrimSpace(savePoint.SavePointID) == "" || strings.TrimSpace(savePoint.CreatedAt) == "" {
			writeSavePointError(w, r, http.StatusServiceUnavailable, CodeJVSCommandFailed, "save point history is unavailable", true)
			return
		}
	}
	_ = writeJSON(w, http.StatusOK, SavePointListResponse{SavePoints: history.SavePoints})
}

func (handler savePointLeafHandler) checkCreateReady(w http.ResponseWriter, r *http.Request) bool {
	if !handler.createReady {
		writeSavePointCreatePending(w, r, "worker_recovery_config_not_ready")
		return false
	}
	if handler.createReadyReader == nil {
		writeSavePointCreatePending(w, r, "worker_recovery_not_ready")
		return false
	}
	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	ready, err := handler.createReadyReader.SavePointCreateRecoveryCapabilityReady(r.Context(), now)
	if err != nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if !ready {
		writeSavePointCreatePending(w, r, "worker_recovery_not_ready")
		return false
	}
	return true
}

func writeSavePointCreatePending(w http.ResponseWriter, r *http.Request, reason string) {
	code, message, retryable, details := fileLibraryBlockingOperationError(false)
	details["execution_status"] = "pending"
	if strings.TrimSpace(reason) != "" {
		details["execution_reason"] = reason
	}
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, details)
	_ = WriteErrorEnvelope(w, http.StatusConflict, envelope)
}

func (handler savePointLeafHandler) checkHistoryReadGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	status, err := readRepoJVSMutationGateStatus(r.Context(), handler.mutationGate, repoID)
	if err != nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if status.InProgress {
		writeRepoJVSMutationGateError(w, r, status)
		return false
	}
	return true
}

func writeSavePointWriterDrainGateError(w http.ResponseWriter, r *http.Request, decision sessionstate.Decision) {
	code, message, retryable, details := fileLibraryBlockingOperationError(false)
	details["writer_drain_status"] = "pending"
	details["writer_drain_reason"] = decision.ErrorFamily.String()
	if strings.TrimSpace(decision.BlockingKind) != "" {
		details["blocking_session_kind"] = decision.BlockingKind
	}
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, details)
	_ = WriteErrorEnvelope(w, http.StatusConflict, envelope)
}

func readRepoJVSMutationGateStatus(ctx context.Context, reader RepoJVSMutationGateStatusReader, repoID string) (RepoJVSMutationGateStatus, error) {
	if reader == nil {
		return RepoJVSMutationGateStatus{}, errors.New("file library operation gate is not configured")
	}
	return reader.GetRepoJVSMutationGateStatus(ctx, repoID)
}

func writeRepoJVSMutationGateError(w http.ResponseWriter, r *http.Request, status RepoJVSMutationGateStatus) {
	recoveryRequired := status.RecoveryRequired || status.OperationState == operations.OperationStateOperatorInterventionRequired
	code, message, retryable, details := fileLibraryBlockingOperationError(recoveryRequired)
	var operationID *string
	if strings.TrimSpace(status.OperationID) != "" {
		id := status.OperationID
		operationID = &id
	}
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), operationID, details)
	_ = WriteErrorEnvelope(w, http.StatusConflict, envelope)
}

func fileLibraryBlockingOperationError(recoveryRequired bool) (ErrorCode, string, bool, map[string]any) {
	if recoveryRequired {
		return CodeFileLibraryOperationRequiresRecovery,
			"file library operation requires recovery",
			false,
			map[string]any{"blocking_status": "requires_recovery", "reason": string(CodeFileLibraryOperationRequiresRecovery), "recovery_required": true, "retryable": false}
	}
	return CodeFileLibraryOperationPending,
		"file library operation is in progress",
		true,
		map[string]any{"blocking_status": "in_progress", "reason": string(CodeFileLibraryOperationPending), "recovery_required": false, "retryable": true}
}

func (handler savePointLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID string, canonical any) bool {
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		writeSavePointError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, operations.OperationSavePointCreate, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return true
	}
	if record.RequestHash != requestHash {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return true
	}
	envelope, projectionErr := operationEnvelopeFromRecord(record)
	if projectionErr != nil {
		writeOperationIntakeHTTPError(w, r, projectionErr)
		return true
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
	return true
}

func (handler savePointLeafHandler) loadMetadata(w http.ResponseWriter, r *http.Request, _ RouteMetadata, _ auth.RequestContext, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSavePointError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
			return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
		}
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	if repo.NamespaceID != namespaceID {
		writeSavePointError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	ns, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	b, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	held, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		writeSavePointError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	return repo, ns, b, repoAccessFencesFromStore(held), true
}

func decodeSavePointCreateRequest(r *http.Request) (savePointCreateRequestDTO, error) {
	var body savePointCreateRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return savePointCreateRequestDTO{}, err
	}
	message, err := operations.NormalizeSavePointMessage(body.Message)
	if err != nil {
		return savePointCreateRequestDTO{}, errors.New("invalid save point message")
	}
	body.Message = message
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return savePointCreateRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return savePointCreateRequestDTO{}, err
	}
	return body, nil
}

func savePointRepoIDFromRoute(r *http.Request, route RouteMetadata) string {
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		return ""
	}
	return strings.TrimSpace(params["repoId"])
}

func writeSavePointAdmissionDenied(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, decision repoaccess.Decision, sink audit.Sink) {
	code, status := savePointAdmissionError(decision.ErrorFamily)
	writePolicyDeniedErrorWithAudit(w, r, route, requestContext, code, status, false, decision.Reason, []string{strings.ToLower(string(decision.ErrorFamily))}, sink)
}

func savePointAdmissionError(family repoaccess.ErrorFamily) (ErrorCode, int) {
	switch family {
	case repoaccess.ErrorFamilyNamespaceDisabled:
		return CodeNamespaceDisabled, http.StatusConflict
	case repoaccess.ErrorFamilyRepoLifecycleFenceHeld:
		return CodeRepoLifecycleFenceHeld, http.StatusConflict
	case repoaccess.ErrorFamilyWriterSessionFenceHeld:
		return CodeWriterSessionFenceHeld, http.StatusConflict
	case repoaccess.ErrorFamilyOperationRecoveryRequired:
		return CodeOperationRecoveryRequired, http.StatusConflict
	case repoaccess.ErrorFamilyRepoArchived:
		return CodeRepoArchived, http.StatusConflict
	case repoaccess.ErrorFamilyRepoTombstoned:
		return CodeRepoTombstoned, http.StatusConflict
	case repoaccess.ErrorFamilyRepoPurged:
		return CodeRepoPurged, http.StatusConflict
	case repoaccess.ErrorFamilyRepoLifecycleInvalidState:
		return CodeRepoLifecycleInvalidState, http.StatusConflict
	default:
		return CodeInternalError, http.StatusInternalServerError
	}
}

func writeSavePointValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, code ErrorCode, message string, labels []string, sink audit.Sink) {
	writeValidationErrorWithAudit(w, r, route, requestContext, code, http.StatusBadRequest, message, labels, sink)
}

func writeSavePointError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
