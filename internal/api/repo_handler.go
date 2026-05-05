package api

import (
	"context"
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
)

type RepoCreateOperationIntakeStore interface {
	CreateOrReuseRepoCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
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
