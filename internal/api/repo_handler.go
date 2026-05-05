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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type RepoReader interface {
	GetRepo(ctx context.Context, repoID string) (resources.Repo, error)
	GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error)
	ListReposByNamespace(ctx context.Context, namespaceID string) ([]resources.Repo, error)
}

type RepoCreateOperationIntakeStore interface {
	CreateOrReuseRepoCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

type RepoReadHandlerConfig struct {
	Reader            RepoReader
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	AuditSink         audit.Sink
}

type CreateRepoHandlerConfig struct {
	IntakeStore       RepoCreateOperationIntakeStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type createRepoRequestDTO struct {
	NamespaceID  string `json:"namespace_id"`
	TargetRepoID string `json:"target_repo_id"`
}

func RepoReadHandler(config RepoReadHandlerConfig) http.Handler {
	getRoute, _ := RouteMetadataByOperationID("getRepo")
	listRoute, _ := RouteMetadataByOperationID("listRepos")
	leaf := repoReadLeafHandler{
		reader:    config.Reader,
		getRoute:  getRoute,
		listRoute: listRoute,
		sink:      config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, repoReadRouteResolver{getRoute: getRoute, listRoute: listRoute}, config.AllowedCallers, config.AuditSink)
}

func CreateRepoHandler(config CreateRepoHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("createRepo")
	leaf := createRepoLeafHandler{
		route:       route,
		intakeStore: config.IntakeStore,
		operationID: config.OperationID,
		now:         config.Now,
		sink:        config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, createRepoRouteResolver{route: route}, config.AllowedCallers, config.AuditSink)
}

type repoReadRouteResolver struct {
	getRoute  RouteMetadata
	listRoute RouteMetadata
}

func (resolver repoReadRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range []RouteMetadata{resolver.getRoute, resolver.listRoute} {
		if route.Method == "" || method != route.Method {
			continue
		}
		if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
			return route, true
		}
	}
	return RouteMetadata{}, false
}

type createRepoRouteResolver struct{ route RouteMetadata }

func (resolver createRepoRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil || strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	if _, ok := RoutePathParams(resolver.route.Path, r.URL.Path); !ok {
		return RouteMetadata{}, false
	}
	return resolver.route, true
}

type repoReadLeafHandler struct {
	reader    RepoReader
	getRoute  RouteMetadata
	listRoute RouteMetadata
	sink      audit.Sink
}

func (handler repoReadLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := handler.routeForRequest(r)
	if !ok {
		writeRepoReadError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	requestNamespace := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if requestNamespace == "" {
		writeRepoReadValidationError(w, r, route, CodeResourceNamespaceMismatch, "request namespace is required", []string{"missing_namespace_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, requestNamespace); err != nil {
		writeRepoReadValidationError(w, r, route, CodeInvalidID, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if handler.reader == nil {
		writeRepoReadError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	if route.OperationID == "listRepos" {
		handler.serveList(w, r, route, requestNamespace)
		return
	}
	handler.serveGet(w, r, route, requestNamespace)
}

func (handler repoReadLeafHandler) routeForRequest(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil || strings.ToUpper(strings.TrimSpace(r.Method)) != http.MethodGet {
		return RouteMetadata{}, false
	}
	if _, ok := RoutePathParams(handler.getRoute.Path, r.URL.Path); ok {
		return handler.getRoute, true
	}
	if _, ok := RoutePathParams(handler.listRoute.Path, r.URL.Path); ok {
		return handler.listRoute, true
	}
	return RouteMetadata{}, false
}

func (handler repoReadLeafHandler) serveGet(w http.ResponseWriter, r *http.Request, route RouteMetadata, namespaceID string) {
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		writeRepoReadError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	repoID := strings.TrimSpace(params["repoId"])
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		writeRepoReadValidationError(w, r, route, CodeInvalidID, "invalid repo id", []string{"invalid_repo_id"}, handler.sink)
		return
	}
	repo, err := handler.reader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeRepoReadError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
			return
		}
		writeRepoReadError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return
	}
	if repo.NamespaceID != namespaceID {
		writeRepoReadError(w, r, http.StatusNotFound, CodeRepoNotFound, "repo was not found", false)
		return
	}
	if err := repo.Validate(); err != nil {
		writeRepoReadError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	_ = writeJSON(w, http.StatusOK, RepoResponseFromResource(repo))
}

func (handler repoReadLeafHandler) serveList(w http.ResponseWriter, r *http.Request, route RouteMetadata, namespaceID string) {
	queryNamespace := strings.TrimSpace(r.URL.Query().Get("namespace_id"))
	if queryNamespace == "" || queryNamespace != namespaceID {
		writeRepoReadValidationError(w, r, route, CodeResourceNamespaceMismatch, "request namespace does not match query namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, queryNamespace); err != nil {
		writeRepoReadValidationError(w, r, route, CodeInvalidID, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	var lifecycleFilter resources.RepoStatus
	if raw := strings.TrimSpace(r.URL.Query().Get("lifecycle_status")); raw != "" {
		lifecycleFilter = resources.RepoStatus(raw)
		if !knownRepoLifecycleStatus(lifecycleFilter) {
			writeRepoReadValidationError(w, r, route, CodeInvalidID, "invalid repo lifecycle status", []string{"invalid_lifecycle_status"}, handler.sink)
			return
		}
	}
	repos, err := handler.reader.ListReposByNamespace(r.Context(), queryNamespace)
	if err != nil {
		writeRepoReadError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return
	}
	filtered := make([]resources.Repo, 0, len(repos))
	for _, repo := range repos {
		if repo.NamespaceID != namespaceID {
			continue
		}
		if lifecycleFilter != "" && repo.Lifecycle.Status != lifecycleFilter {
			continue
		}
		if err := repo.Validate(); err != nil {
			writeRepoReadError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
			return
		}
		filtered = append(filtered, repo)
	}
	_ = writeJSON(w, http.StatusOK, ListReposResponseFromResources(filtered))
}

func knownRepoLifecycleStatus(status resources.RepoStatus) bool {
	switch status {
	case resources.RepoStatusActive,
		resources.RepoStatusArchiving,
		resources.RepoStatusArchived,
		resources.RepoStatusRestoringArchived,
		resources.RepoStatusDeleting,
		resources.RepoStatusTombstoned,
		resources.RepoStatusRestoringTombstoned,
		resources.RepoStatusPurging,
		resources.RepoStatusPurged,
		resources.RepoStatusOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

func writeRepoReadValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, code ErrorCode, message string, labels []string, sink audit.Sink) {
	requestContext, _ := RequestContextFromRequest(r)
	writeValidationErrorWithAudit(w, r, route, requestContext, code, http.StatusBadRequest, message, labels, sink)
}

func writeRepoReadError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}

type createRepoLeafHandler struct {
	route       RouteMetadata
	intakeStore RepoCreateOperationIntakeStore
	operationID OperationIDGenerator
	now         func() time.Time
	sink        audit.Sink
}

func (handler createRepoLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	body, err := decodeCreateRepoRequest(r)
	if err != nil {
		writeCreateRepoValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid repo create request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	headerNamespace := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if headerNamespace == "" || body.NamespaceID != headerNamespace {
		writeCreateRepoValidationError(w, r, handler.route, requestContext, CodeResourceNamespaceMismatch, "request namespace does not match route namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, body.NamespaceID); err != nil {
		writeCreateRepoValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid repo create request", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, body.TargetRepoID); err != nil {
		writeCreateRepoValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid repo create request", []string{"invalid_target_repo_id"}, handler.sink)
		return
	}
	envelope, err := CreateOrReuseRepoCreateOperationIntake(r.Context(), RepoCreateOperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		NamespaceID:         body.NamespaceID,
		RepoID:              body.TargetRepoID,
		Resource:            operations.ResourceRef{Type: "repo", ID: body.TargetRepoID},
		CanonicalRequest:    body,
		InputSummary:        createRepoInputSummary(body),
		Phase:               operations.OperationPhaseRepoCreateValidate,
		GenerateOperationID: handler.operationID,
		Now:                 handler.now,
	})
	if err != nil {
		if intakeErr, ok := err.(*OperationIntakeError); ok && intakeErr.Code == CodeRepoAlreadyExists {
			writePolicyDeniedErrorWithAudit(w, r, handler.route, requestContext, CodeRepoAlreadyExists, http.StatusConflict, false, intakeErr.Message, []string{"repo_already_exists"}, handler.sink)
			return
		}
		writeOperationIntakeHTTPError(w, r, err)
		return
	}
	_ = writeJSON(w, http.StatusOK, envelope)
}

type RepoCreateOperationIntakeConfig struct {
	Store RepoCreateOperationIntakeStore
}

func CreateOrReuseRepoCreateOperationIntake(ctx context.Context, config RepoCreateOperationIntakeConfig, request OperationIntakeRequest) (OperationEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if isNilOperationIntakeValue(config.Store) {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	if request.Route.OperationID != "createRepo" {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	if isNilOperationIntakeValue(request.CanonicalRequest) {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	requestNamespace := strings.TrimSpace(request.NamespaceID)
	contextNamespace := strings.TrimSpace(request.RequestContext.NamespaceID)
	if requestNamespace == "" || contextNamespace == "" || requestNamespace != contextNamespace {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	if request.GenerateOperationID == nil {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	operationID := strings.TrimSpace(request.GenerateOperationID())
	if operationID == "" {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	now := time.Now().UTC()
	if request.Now != nil {
		now = request.Now()
	}
	if now.IsZero() {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	requestHash, err := operations.HashRequest(request.CanonicalRequest)
	if err != nil {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	spec := operations.QueuedOperationSpec{
		OperationID:     operationID,
		Scope:           operations.NewIdempotencyScope(request.RequestContext.CallerService, request.NamespaceID, operations.OperationRepoCreate, request.RequestContext.IdempotencyKey),
		RequestHash:     requestHash,
		Phase:           operations.OperationPhaseRepoCreateValidate,
		CorrelationID:   request.RequestContext.CorrelationID,
		CallerService:   request.RequestContext.CallerService,
		AuthorizedActor: operations.Actor{Type: request.RequestContext.Actor.Type, ID: request.RequestContext.Actor.ID},
		Resource:        request.Resource,
		NamespaceID:     request.NamespaceID,
		RepoID:          request.RepoID,
		InputSummary:    cloneAnyMap(request.InputSummary),
		CreatedAt:       now,
	}
	resolution, err := config.Store.CreateOrReuseRepoCreateOperation(ctx, spec)
	if err != nil {
		return OperationEnvelope{}, mapOperationIntakeError(err)
	}
	return operationEnvelopeFromRecord(resolution.Operation), nil
}

func decodeCreateRepoRequest(r *http.Request) (createRepoRequestDTO, error) {
	var body createRepoRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return createRepoRequestDTO{}, err
	}
	if strings.TrimSpace(body.NamespaceID) == "" || strings.TrimSpace(body.TargetRepoID) == "" {
		return createRepoRequestDTO{}, errors.New("missing required repo create field")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return createRepoRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return createRepoRequestDTO{}, err
	}
	return body, nil
}

func createRepoInputSummary(body createRepoRequestDTO) map[string]any {
	return map[string]any{
		"namespace_id":   body.NamespaceID,
		"target_repo_id": body.TargetRepoID,
	}
}

func writeCreateRepoValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, code ErrorCode, message string, labels []string, sink audit.Sink) {
	writeValidationErrorWithAudit(w, r, route, requestContext, code, http.StatusBadRequest, message, labels, sink)
}
