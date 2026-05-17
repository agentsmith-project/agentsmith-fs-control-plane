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
)

type TemplateOperationIntakeStore interface {
	CreateOrReuseTemplateCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
	CreateOrReuseTemplateCloneOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

type RepoTemplateHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	FenceReader       RepoFenceReader
	MutationGate      RepoJVSMutationGateStatusReader
	IntakeStore       TemplateOperationIntakeStore
	IntakeLookupStore OperationIdempotencyLookupStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AdmissionDisabled bool
	AuditSink         audit.Sink
}

type createRepoTemplateRequestDTO struct {
	NamespaceID      string `json:"namespace_id"`
	SourceRepoID     string `json:"source_repo_id"`
	TargetTemplateID string `json:"target_template_id"`
	CloneHistoryMode string `json:"clone_history_mode"`
}

type cloneRepoTemplateRequestDTO struct {
	NamespaceID       string `json:"namespace_id"`
	SourceNamespaceID string `json:"source_namespace_id,omitempty"`
	TemplateID        string `json:"template_id"`
	TargetRepoID      string `json:"target_repo_id"`
}

type createRepoTemplateCanonicalRequest struct {
	NamespaceID      string `json:"namespace_id"`
	SourceRepoID     string `json:"source_repo_id"`
	TargetTemplateID string `json:"target_template_id"`
	CloneHistoryMode string `json:"clone_history_mode"`
}

type cloneRepoTemplateCanonicalRequest struct {
	NamespaceID       string `json:"namespace_id"`
	SourceNamespaceID string `json:"source_namespace_id"`
	TemplateID        string `json:"template_id"`
	TargetRepoID      string `json:"target_repo_id"`
}

func RepoTemplateHandler(config RepoTemplateHandlerConfig) http.Handler {
	createRoute, _ := RouteMetadataByOperationID("createRepoTemplate")
	cloneRoute, _ := RouteMetadataByOperationID("cloneRepoTemplate")
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
	leaf := repoTemplateLeafHandler{
		createRoute:       createRoute,
		cloneRoute:        cloneRoute,
		repoReader:        config.RepoReader,
		namespaceReader:   config.NamespaceReader,
		bindingReader:     config.BindingReader,
		fenceReader:       config.FenceReader,
		mutationGate:      mutationGate,
		intakeStore:       config.IntakeStore,
		lookupStore:       lookupStore,
		operationID:       config.OperationID,
		now:               config.Now,
		admissionDisabled: config.AdmissionDisabled,
		sink:              config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, repoTemplateRouteResolver{createRoute: createRoute, cloneRoute: cloneRoute}, config.AllowedCallers, config.AuditSink)
}

type repoTemplateRouteResolver struct {
	createRoute RouteMetadata
	cloneRoute  RouteMetadata
}

func (resolver repoTemplateRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range []RouteMetadata{resolver.createRoute, resolver.cloneRoute} {
		if route.Method == method {
			if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
				return route, true
			}
		}
	}
	return RouteMetadata{}, false
}

type repoTemplateLeafHandler struct {
	createRoute       RouteMetadata
	cloneRoute        RouteMetadata
	repoReader        RepoReader
	namespaceReader   NamespaceReader
	bindingReader     NamespaceVolumeBindingReader
	fenceReader       RepoFenceReader
	mutationGate      RepoJVSMutationGateStatusReader
	intakeStore       TemplateOperationIntakeStore
	lookupStore       OperationIdempotencyLookupStore
	operationID       OperationIDGenerator
	now               func() time.Time
	admissionDisabled bool
	sink              audit.Sink
}

func (handler repoTemplateLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeRepoTemplateError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	route, ok := handler.routeForRequest(r)
	if !ok {
		writeRepoTemplateError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	if route.OperationID == "createRepoTemplate" {
		handler.serveCreate(w, r, route, requestContext)
		return
	}
	handler.serveClone(w, r, route, requestContext)
}

func (handler repoTemplateLeafHandler) routeForRequest(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range []RouteMetadata{handler.createRoute, handler.cloneRoute} {
		if route.Method == method {
			if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
				return route, true
			}
		}
	}
	return RouteMetadata{}, false
}

func (handler repoTemplateLeafHandler) serveCreate(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext) {
	body, err := decodeCreateRepoTemplateRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid repo template create request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	headerNamespace := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if headerNamespace == "" || body.NamespaceID != headerNamespace {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace does not match body namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, body.NamespaceID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, body.SourceRepoID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid source repo id", []string{"invalid_source_repo_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.TemplateID, body.TargetTemplateID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid target template id", []string{"invalid_target_template_id"}, handler.sink)
		return
	}
	if body.CloneHistoryMode != "main" {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid clone history mode", []string{"invalid_clone_history_mode"}, handler.sink)
		return
	}
	canonical := createRepoTemplateCanonicalRequest{NamespaceID: body.NamespaceID, SourceRepoID: body.SourceRepoID, TargetTemplateID: body.TargetTemplateID, CloneHistoryMode: body.CloneHistoryMode}
	if handler.lookupStore == nil {
		if handler.admissionDisabled {
			writeRepoTemplateAdmissionDisabled(w, r, route, requestContext, handler.sink)
			return
		}
		writeRepoTemplateError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	if handler.writeExistingIdempotentOperation(w, r, route, requestContext, body.NamespaceID, operations.OperationTemplateCreate, canonical) {
		return
	}
	if handler.admissionDisabled {
		writeRepoTemplateAdmissionDisabled(w, r, route, requestContext, handler.sink)
		return
	}
	if handler.missingMutationDeps() {
		writeRepoTemplateError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	repo, namespace, binding, held, ok := handler.loadOrdinaryRepoMetadata(w, r, body.NamespaceID, body.SourceRepoID)
	if !ok {
		return
	}
	if namespace.Status != resources.NamespaceStatusActive || binding.Status != resources.NamespaceStatusActive {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeNamespaceDisabled, http.StatusConflict, false, "namespace or namespace binding is not active", []string{"namespace_disabled"}, handler.sink)
		return
	}
	if !templatePolicyEnabled(binding) {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeCapabilityDenied, http.StatusForbidden, false, "repo templates are disabled for namespace", []string{"template_policy_disabled"}, handler.sink)
		return
	}
	if repo.VolumeID != binding.DefaultVolumeID {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeVolumeMismatchRequiresImport, http.StatusConflict, false, "template source repo volume does not match namespace default volume", []string{"volume_mismatch_requires_import"}, handler.sink)
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: held, Intent: repoaccess.IntentTemplateCreateFromRepo, Mode: repoaccess.ModeReadOnly})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, route, requestContext, decision, handler.sink)
		return
	}
	if !handler.checkJVSMutationGate(w, r, body.SourceRepoID) {
		return
	}
	envelope, intakeErr := handler.createTemplateCreateIntake(r.Context(), requestContext, route, canonical)
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler repoTemplateLeafHandler) serveClone(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext) {
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		writeRepoTemplateError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	templateID := strings.TrimSpace(params["templateId"])
	if err := pathresolver.ValidateID(pathresolver.TemplateID, templateID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid template id", []string{"invalid_template_id"}, handler.sink)
		return
	}
	body, err := decodeCloneRepoTemplateRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid repo template clone request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	headerNamespace := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if headerNamespace == "" || body.NamespaceID != headerNamespace {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace does not match body namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
	if body.SourceNamespaceID == "" {
		body.SourceNamespaceID = body.NamespaceID
	}
	if body.TemplateID != templateID {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request template id does not match route template id", []string{"template_id_mismatch"}, handler.sink)
		return
	}
	if body.SourceNamespaceID != body.NamespaceID {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeResourceNamespaceMismatch, http.StatusForbidden, false, "cross-namespace template clone is not allowed", []string{"cross_namespace_template_clone_denied"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, body.NamespaceID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, body.TargetRepoID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid target repo id", []string{"invalid_target_repo_id"}, handler.sink)
		return
	}
	canonical := cloneRepoTemplateCanonicalRequest{NamespaceID: body.NamespaceID, SourceNamespaceID: body.SourceNamespaceID, TemplateID: templateID, TargetRepoID: body.TargetRepoID}
	if handler.lookupStore == nil {
		if handler.admissionDisabled {
			writeRepoTemplateAdmissionDisabled(w, r, route, requestContext, handler.sink)
			return
		}
		writeRepoTemplateError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	if handler.writeExistingIdempotentOperation(w, r, route, requestContext, body.NamespaceID, operations.OperationTemplateClone, canonical) {
		return
	}
	if handler.admissionDisabled {
		writeRepoTemplateAdmissionDisabled(w, r, route, requestContext, handler.sink)
		return
	}
	if handler.missingMutationDeps() {
		writeRepoTemplateError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	template, namespace, binding, ok := handler.loadTemplateMetadata(w, r, body.NamespaceID, templateID)
	if !ok {
		return
	}
	if namespace.Status != resources.NamespaceStatusActive || binding.Status != resources.NamespaceStatusActive {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeNamespaceDisabled, http.StatusConflict, false, "namespace or namespace binding is not active", []string{"namespace_disabled"}, handler.sink)
		return
	}
	if !templatePolicyEnabled(binding) {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeCapabilityDenied, http.StatusForbidden, false, "repo templates are disabled for namespace", []string{"template_policy_disabled"}, handler.sink)
		return
	}
	if template.VolumeID != binding.DefaultVolumeID {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeVolumeMismatchRequiresImport, http.StatusConflict, false, "template volume does not match namespace default volume", []string{"volume_mismatch_requires_import"}, handler.sink)
		return
	}
	if template.Kind != resources.RepoKindTemplate || template.Status != resources.RepoStatusActive {
		writeRepoTemplateError(w, r, http.StatusConflict, CodeRepoLifecycleInvalidState, "template clone source is not active", false)
		return
	}
	envelope, intakeErr := handler.createTemplateCloneIntake(r.Context(), requestContext, route, canonical)
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler repoTemplateLeafHandler) missingMutationDeps() bool {
	return handler.intakeStore == nil || handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.fenceReader == nil || handler.mutationGate == nil
}

func (handler repoTemplateLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID string, operationType operations.OperationType, canonical any) bool {
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		writeRepoTemplateError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, operationType, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", []string{"idempotency_lookup_unavailable"}, handler.sink)
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

func (handler repoTemplateLeafHandler) createTemplateCreateIntake(ctx context.Context, requestContext auth.RequestContext, route RouteMetadata, canonical createRepoTemplateCanonicalRequest) (OperationEnvelope, error) {
	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	return createOrReuseTemplateOperationIntake(ctx, handler.intakeStore.CreateOrReuseTemplateCreateOperation, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               route,
		NamespaceID:         canonical.NamespaceID,
		RepoID:              canonical.SourceRepoID,
		TemplateID:          canonical.TargetTemplateID,
		Resource:            operations.ResourceRef{Type: "repo_template", ID: canonical.TargetTemplateID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"source_repo_id": canonical.SourceRepoID, "target_template_id": canonical.TargetTemplateID, "clone_history_mode": canonical.CloneHistoryMode},
		Phase:               operations.OperationPhaseTemplateCreateValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	}, operations.OperationTemplateCreate)
}

func (handler repoTemplateLeafHandler) createTemplateCloneIntake(ctx context.Context, requestContext auth.RequestContext, route RouteMetadata, canonical cloneRepoTemplateCanonicalRequest) (OperationEnvelope, error) {
	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	return createOrReuseTemplateOperationIntake(ctx, handler.intakeStore.CreateOrReuseTemplateCloneOperation, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               route,
		NamespaceID:         canonical.NamespaceID,
		RepoID:              canonical.TargetRepoID,
		TemplateID:          canonical.TemplateID,
		Resource:            operations.ResourceRef{Type: "repo", ID: canonical.TargetRepoID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"template_id": canonical.TemplateID, "target_repo_id": canonical.TargetRepoID, "clone_history_mode": "main"},
		Phase:               operations.OperationPhaseTemplateCloneValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	}, operations.OperationTemplateClone)
}

func createOrReuseTemplateOperationIntake(ctx context.Context, create func(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error), request OperationIntakeRequest, operationType operations.OperationType) (OperationEnvelope, error) {
	if got, ok := operations.OperationTypeForRouteOperationID(request.Route.OperationID); !ok || got != operationType {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	spec, err := operationIntakeSpec(request)
	if err != nil {
		return OperationEnvelope{}, err
	}
	resolution, err := create(ctx, spec)
	if err != nil {
		return OperationEnvelope{}, mapOperationIntakeError(err)
	}
	return operationEnvelopeFromRecord(resolution.Operation)
}

func (handler repoTemplateLeafHandler) loadOrdinaryRepoMetadata(w http.ResponseWriter, r *http.Request, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRepoTemplateError(w, r, http.StatusNotFound, CodeRepoNotFound, "source repo was not found", false)
			return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
		}
		writeRepoTemplateError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	namespace, binding, ok := handler.loadNamespaceBinding(w, r, namespaceID)
	if !ok {
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	held, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		writeRepoTemplateError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	return repo, namespace, binding, repoAccessFencesFromStore(held), true
}

func (handler repoTemplateLeafHandler) loadTemplateMetadata(w http.ResponseWriter, r *http.Request, namespaceID, templateID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, bool) {
	template, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, templateID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRepoTemplateError(w, r, http.StatusNotFound, CodeRepoNotFound, "template was not found", false)
			return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, false
		}
		writeRepoTemplateError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, false
	}
	namespace, binding, ok := handler.loadNamespaceBinding(w, r, namespaceID)
	return template, namespace, binding, ok
}

func (handler repoTemplateLeafHandler) loadNamespaceBinding(w http.ResponseWriter, r *http.Request, namespaceID string) (resources.Namespace, resources.NamespaceVolumeBinding, bool) {
	namespace, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		writeRepoTemplateError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Namespace{}, resources.NamespaceVolumeBinding{}, false
	}
	binding, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		writeRepoTemplateError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return resources.Namespace{}, resources.NamespaceVolumeBinding{}, false
	}
	return namespace, binding, true
}

func (handler repoTemplateLeafHandler) checkJVSMutationGate(w http.ResponseWriter, r *http.Request, repoID string) bool {
	status, err := readRepoJVSMutationGateStatus(r.Context(), handler.mutationGate, repoID)
	if err != nil {
		writeRepoTemplateError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return false
	}
	if status.InProgress {
		writeRepoJVSMutationGateError(w, r, status)
		return false
	}
	return true
}

func decodeCreateRepoTemplateRequest(r *http.Request) (createRepoTemplateRequestDTO, error) {
	var body createRepoTemplateRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return createRepoTemplateRequestDTO{}, err
	}
	body.NamespaceID = strings.TrimSpace(body.NamespaceID)
	body.SourceRepoID = strings.TrimSpace(body.SourceRepoID)
	body.TargetTemplateID = strings.TrimSpace(body.TargetTemplateID)
	body.CloneHistoryMode = strings.TrimSpace(body.CloneHistoryMode)
	if body.NamespaceID == "" || body.SourceRepoID == "" || body.TargetTemplateID == "" || body.CloneHistoryMode == "" {
		return createRepoTemplateRequestDTO{}, errors.New("missing required repo template create field")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return createRepoTemplateRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return createRepoTemplateRequestDTO{}, err
	}
	return body, nil
}

func decodeCloneRepoTemplateRequest(r *http.Request) (cloneRepoTemplateRequestDTO, error) {
	var body cloneRepoTemplateRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return cloneRepoTemplateRequestDTO{}, err
	}
	body.NamespaceID = strings.TrimSpace(body.NamespaceID)
	body.SourceNamespaceID = strings.TrimSpace(body.SourceNamespaceID)
	body.TemplateID = strings.TrimSpace(body.TemplateID)
	body.TargetRepoID = strings.TrimSpace(body.TargetRepoID)
	if body.NamespaceID == "" || body.TemplateID == "" || body.TargetRepoID == "" {
		return cloneRepoTemplateRequestDTO{}, errors.New("missing required repo template clone field")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return cloneRepoTemplateRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return cloneRepoTemplateRequestDTO{}, err
	}
	return body, nil
}

func templatePolicyEnabled(binding resources.NamespaceVolumeBinding) bool {
	enabled, _ := binding.TemplatePolicy["namespace_templates_enabled"].(bool)
	return enabled
}

func writeRepoTemplateAdmissionDisabled(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, sink audit.Sink) {
	message := "repo template admission is disabled"
	validationErrors := []string{"repo_template_admission_disabled"}
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

func writeRepoTemplateError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
