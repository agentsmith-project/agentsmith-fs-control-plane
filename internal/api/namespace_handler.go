package api

import (
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

type NamespaceUpsertHandlerConfig struct {
	IntakeStore       OperationIntakeStore
	PrincipalResolver PrincipalResolver
	DeploymentPolicy  AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type upsertNamespaceRequestBody struct {
	NamespaceID string `json:"namespace_id"`
}

type DisableNamespaceHandlerConfig NamespaceUpsertHandlerConfig

type disableNamespaceRequestBody struct {
	Reason string `json:"reason"`
}

// NamespaceUpsertHandler accepts/reuses namespace upsert operations only; it does not mutate namespace metadata.
func NamespaceUpsertHandler(config NamespaceUpsertHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("upsertNamespace")
	leaf := namespaceUpsertLeafHandler{
		route:       route,
		intakeStore: config.IntakeStore,
		operationID: config.OperationID,
		now:         config.Now,
		sink:        config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, namespaceUpsertRouteResolver{route: route}, config.DeploymentPolicy, config.AuditSink)
}

func DisableNamespaceHandler(config DisableNamespaceHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("disableNamespace")
	leaf := namespaceDisableLeafHandler{
		route:       route,
		intakeStore: config.IntakeStore,
		operationID: config.OperationID,
		now:         config.Now,
		sink:        config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, namespaceUpsertRouteResolver{route: route}, config.DeploymentPolicy, config.AuditSink)
}

type namespaceDisableLeafHandler struct {
	route       RouteMetadata
	intakeStore OperationIntakeStore
	operationID OperationIDGenerator
	now         func() time.Time
	sink        audit.Sink
}

func (handler namespaceDisableLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	namespaceID, ok := namespaceUpsertPathNamespaceID(r, handler.route)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if requestContext.NamespaceID == "" || requestContext.NamespaceID != namespaceID {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeResourceNamespaceMismatch, "request namespace does not match route namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
	body, err := decodeDisableNamespaceRequest(r)
	if err != nil {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid namespace disable request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	canonical := disableNamespaceRequestBody{Reason: body.Reason}
	envelope, err := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		NamespaceID:         namespaceID,
		Resource:            operations.ResourceRef{Type: "namespace", ID: namespaceID},
		CanonicalRequest:    canonical,
		InputSummary:        map[string]any{"namespace_id": namespaceID, "reason": body.Reason},
		Phase:               operations.OperationPhaseNamespaceDisableValidate,
		GenerateOperationID: handler.operationID,
		Now:                 handler.now,
	})
	if err != nil {
		writeOperationIntakeHTTPError(w, r, err)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

type namespaceUpsertRouteResolver struct {
	route RouteMetadata
}

func (resolver namespaceUpsertRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	if strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	if _, ok := RoutePathParams(resolver.route.Path, r.URL.Path); !ok {
		return RouteMetadata{}, false
	}
	return resolver.route, true
}

type namespaceUpsertLeafHandler struct {
	route       RouteMetadata
	intakeStore OperationIntakeStore
	operationID OperationIDGenerator
	now         func() time.Time
	sink        audit.Sink
}

func (handler namespaceUpsertLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}

	namespaceID, ok := namespaceUpsertPathNamespaceID(r, handler.route)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if requestContext.NamespaceID == "" || requestContext.NamespaceID != namespaceID {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeResourceNamespaceMismatch, "request namespace does not match route namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}

	body, err := decodeUpsertNamespaceRequest(r)
	if err != nil {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid namespace upsert request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, body.NamespaceID); err != nil {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if body.NamespaceID != namespaceID {
		writeNamespaceUpsertValidationError(w, r, handler.route, requestContext, CodeResourceNamespaceMismatch, "request namespace does not match route namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}

	envelope, err := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		NamespaceID:         namespaceID,
		Resource:            operations.ResourceRef{Type: "namespace", ID: namespaceID},
		CanonicalRequest:    body,
		InputSummary:        map[string]any{"namespace_id": namespaceID},
		Phase:               operations.OperationPhaseNamespaceUpsertValidate,
		GenerateOperationID: handler.operationID,
		Now:                 handler.now,
	})
	if err != nil {
		writeOperationIntakeHTTPError(w, r, err)
		return
	}
	_ = writeJSON(w, http.StatusOK, envelope)
}

func namespaceUpsertPathNamespaceID(r *http.Request, route RouteMetadata) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		return "", false
	}
	namespaceID := params["namespaceId"]
	return namespaceID, namespaceID != ""
}

func decodeUpsertNamespaceRequest(r *http.Request) (upsertNamespaceRequestBody, error) {
	var body upsertNamespaceRequestBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return upsertNamespaceRequestBody{}, err
	}
	if strings.TrimSpace(body.NamespaceID) == "" {
		return upsertNamespaceRequestBody{}, errors.New("missing namespace_id")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return upsertNamespaceRequestBody{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return upsertNamespaceRequestBody{}, err
	}
	return body, nil
}

func decodeDisableNamespaceRequest(r *http.Request) (disableNamespaceRequestBody, error) {
	var body disableNamespaceRequestBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return disableNamespaceRequestBody{}, err
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" || len(body.Reason) > 1024 {
		return disableNamespaceRequestBody{}, errors.New("invalid reason")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return disableNamespaceRequestBody{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return disableNamespaceRequestBody{}, err
	}
	return body, nil
}

func writeNamespaceUpsertValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, code ErrorCode, message string, labels []string, sink audit.Sink) {
	writeValidationErrorWithAudit(w, r, route, requestContext, code, http.StatusBadRequest, message, labels, sink)
}

func writeOperationIntakeHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	var intakeErr *OperationIntakeError
	if !errors.As(err, &intakeErr) || intakeErr == nil {
		intakeErr = internalOperationIntakeError()
	}
	envelope := NewErrorEnvelope(intakeErr.Code, intakeErr.Message, intakeErr.Retryable, CorrelationIDFromRequest(r), nil, intakeErr.Details)
	_ = WriteErrorEnvelope(w, intakeErr.Status, envelope)
}
