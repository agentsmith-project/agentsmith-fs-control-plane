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

type RestoreRunMetadataReader interface {
	GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error)
	GetRestorePlanByPreviewOperation(ctx context.Context, previewOperationID string) (restoreplan.Plan, error)
}

type RestoreRunIntakeGateReader interface {
	RestoreRunExistsForPreviewOperation(ctx context.Context, namespaceID, repoID, previewOperationID string) (bool, error)
}

type RestoreRunHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	FenceReader       RepoFenceReader
	MetadataReader    RestoreRunMetadataReader
	RunGate           RestoreRunIntakeGateReader
	IntakeStore       RestoreRunOperationIntakeStore
	IntakeLookupStore OperationIdempotencyLookupStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type restoreRunRequestDTO struct {
	PreviewOperationID string `json:"preview_operation_id"`
}

type restoreRunCanonicalRequest struct {
	RepoID             string `json:"repo_id"`
	PreviewOperationID string `json:"preview_operation_id"`
}

func RestoreRunHandler(config RestoreRunHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("restoreRun")
	lookupStore := config.IntakeLookupStore
	if lookupStore == nil {
		if typed, ok := config.IntakeStore.(OperationIdempotencyLookupStore); ok {
			lookupStore = typed
		}
	}
	reader := config.MetadataReader
	if reader == nil {
		if typed, ok := config.IntakeStore.(RestoreRunMetadataReader); ok {
			reader = typed
		}
	}
	runGate := config.RunGate
	if runGate == nil {
		if typed, ok := config.IntakeStore.(RestoreRunIntakeGateReader); ok {
			runGate = typed
		}
	}
	leaf := restoreRunLeafHandler{
		route:           route,
		repoReader:      config.RepoReader,
		namespaceReader: config.NamespaceReader,
		bindingReader:   config.BindingReader,
		fenceReader:     config.FenceReader,
		metadataReader:  reader,
		runGate:         runGate,
		intakeStore:     config.IntakeStore,
		lookupStore:     lookupStore,
		operationID:     config.OperationID,
		now:             config.Now,
		sink:            config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, restoreRunRouteResolver{route: route}, config.AllowedCallers, config.AuditSink)
}

type restoreRunRouteResolver struct {
	route RouteMetadata
}

func (resolver restoreRunRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	if strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	_, ok := RoutePathParams(resolver.route.Path, r.URL.Path)
	return resolver.route, ok
}

type restoreRunLeafHandler struct {
	route           RouteMetadata
	repoReader      RepoReader
	namespaceReader NamespaceReader
	bindingReader   NamespaceVolumeBindingReader
	fenceReader     RepoFenceReader
	metadataReader  RestoreRunMetadataReader
	runGate         RestoreRunIntakeGateReader
	intakeStore     RestoreRunOperationIntakeStore
	lookupStore     OperationIdempotencyLookupStore
	operationID     OperationIDGenerator
	now             func() time.Time
	sink            audit.Sink
}

func (handler restoreRunLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeRestoreRunError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	params, ok := RoutePathParams(handler.route.Path, r.URL.Path)
	if !ok {
		writeRestoreRunError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
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
	if handler.metadataReader == nil || handler.runGate == nil || handler.intakeStore == nil || handler.lookupStore == nil || handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.fenceReader == nil {
		writeRestoreRunError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	body, err := decodeRestoreRunRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid restore run request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, body.PreviewOperationID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid preview operation id", []string{"invalid_preview_operation_id"}, handler.sink)
		return
	}
	canonical := restoreRunCanonicalRequest{RepoID: repoID, PreviewOperationID: body.PreviewOperationID}
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
	if !handler.validatePreviewPlanAndRunGate(w, r, namespaceID, repoID, body.PreviewOperationID) {
		return
	}

	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	envelope, intakeErr := CreateOrReuseRestoreRunOperationIntake(r.Context(), handler.intakeStore, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		Resource:            operations.ResourceRef{Type: "repo", ID: repoID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"preview_operation_id": body.PreviewOperationID},
		Phase:               operations.OperationPhaseRestoreRunValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler restoreRunLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, requestContext auth.RequestContext, namespaceID string, canonical any) bool {
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		writeRestoreRunError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, operations.OperationRestoreRun, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return true
	}
	if record.RequestHash != requestHash {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return true
	}
	_ = writeJSON(w, http.StatusAccepted, operationEnvelopeFromRecord(record))
	return true
}

func (handler restoreRunLeafHandler) validatePreviewPlanAndRunGate(w http.ResponseWriter, r *http.Request, namespaceID, repoID, previewOperationID string) bool {
	preview, err := handler.metadataReader.GetOperation(r.Context(), previewOperationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRestoreRunError(w, r, http.StatusNotFound, CodeOperationNotFound, "preview operation was not found", false)
			return false
		}
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if preview.Type != operations.OperationRestorePreview || preview.State != operations.OperationStateSucceeded || preview.Phase != operations.OperationPhaseRestorePreviewCommitted || preview.NamespaceID != namespaceID || preview.RepoID != repoID || preview.Resource.Type != "repo" || preview.Resource.ID != repoID {
		writeRestoreRunError(w, r, http.StatusNotFound, CodeOperationNotFound, "preview operation was not found", false)
		return false
	}
	plan, err := handler.metadataReader.GetRestorePlanByPreviewOperation(r.Context(), previewOperationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRestoreRunError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "restore preview plan requires operator recovery", true)
			return false
		}
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if plan.NamespaceID != namespaceID || plan.RepoID != repoID || plan.PreviewOperationID != previewOperationID || plan.Status != restoreplan.StatusPending {
		writeRestoreRunError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "restore preview plan is not pending", true)
		return false
	}
	if err := plan.Validate(); err != nil {
		writeRestoreRunError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "restore preview plan requires operator recovery", true)
		return false
	}
	if !restoreRunPreviewMetadataMatchesPlan(preview, plan) {
		writeRestoreRunError(w, r, http.StatusConflict, CodeOperationRecoveryRequired, "restore preview metadata does not match durable plan", true)
		return false
	}
	exists, err := handler.runGate.RestoreRunExistsForPreviewOperation(r.Context(), namespaceID, repoID, previewOperationID)
	if err != nil {
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if exists {
		writeRestoreRunError(w, r, http.StatusConflict, CodeRepoJVSMutationInProgress, "restore run is already queued for this preview", true)
		return false
	}
	return true
}

func (handler restoreRunLeafHandler) loadMetadata(w http.ResponseWriter, r *http.Request, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRestoreRunError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
			return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
		}
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	if repo.NamespaceID != namespaceID {
		writeRestoreRunError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	namespace, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	binding, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	held, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		writeRestoreRunError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	return repo, namespace, binding, repoAccessFencesFromStore(held), true
}

func restoreRunPreviewMetadataMatchesPlan(preview operations.OperationRecord, plan restoreplan.Plan) bool {
	seenPlanID, seenSourceSavePointID := false, false
	for _, value := range restoreRunPreviewSafeMetadataValues(preview, "restore_plan_id") {
		seenPlanID = true
		if value != plan.ID || restoreplan.ValidateID(value) != nil {
			return false
		}
	}
	for _, value := range restoreRunPreviewSafeMetadataValues(preview, "source_save_point_id") {
		seenSourceSavePointID = true
		if value != plan.SourceSavePointID || operations.ValidateSavePointID(value) != nil {
			return false
		}
	}
	return seenPlanID && seenSourceSavePointID
}

func restoreRunPreviewSafeMetadataValues(preview operations.OperationRecord, key string) []string {
	values := []string{}
	for _, source := range []any{preview.ExternalResourceIDs, preview.VerificationResult, preview.JVSJSONOutput} {
		switch typed := source.(type) {
		case map[string]string:
			value := strings.TrimSpace(typed[key])
			if value != "" && value != redactedDetailValue {
				values = append(values, value)
			}
		case map[string]any:
			value, _ := typed[key].(string)
			value = strings.TrimSpace(value)
			if value != "" && value != redactedDetailValue {
				values = append(values, value)
			}
		}
	}
	return values
}

func decodeRestoreRunRequest(r *http.Request) (restoreRunRequestDTO, error) {
	var body restoreRunRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return restoreRunRequestDTO{}, err
	}
	body.PreviewOperationID = strings.TrimSpace(body.PreviewOperationID)
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return restoreRunRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return restoreRunRequestDTO{}, err
	}
	return body, nil
}

func writeRestoreRunError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
