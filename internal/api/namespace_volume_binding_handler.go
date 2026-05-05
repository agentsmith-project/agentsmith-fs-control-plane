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

type NamespaceVolumeBindingReader interface {
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
}

type NamespaceVolumeBindingHandlerConfig struct {
	Reader            NamespaceVolumeBindingReader
	IntakeStore       OperationIntakeStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

func NamespaceVolumeBindingHandler(config NamespaceVolumeBindingHandlerConfig) http.Handler {
	getRoute, _ := RouteMetadataByOperationID("getNamespaceVolumeBinding")
	putRoute, _ := RouteMetadataByOperationID("putNamespaceVolumeBinding")
	leaf := namespaceVolumeBindingLeafHandler{
		reader:      config.Reader,
		intakeStore: config.IntakeStore,
		getRoute:    getRoute,
		putRoute:    putRoute,
		operationID: config.OperationID,
		now:         config.Now,
		sink:        config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, namespaceVolumeBindingRouteResolver{getRoute: getRoute, putRoute: putRoute}, config.AllowedCallers, config.AuditSink)
}

type namespaceVolumeBindingRouteResolver struct {
	getRoute RouteMetadata
	putRoute RouteMetadata
}

func (resolver namespaceVolumeBindingRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range []RouteMetadata{resolver.getRoute, resolver.putRoute} {
		if route.Method == "" || method != route.Method {
			continue
		}
		if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
			return route, true
		}
	}
	return RouteMetadata{}, false
}

type namespaceVolumeBindingLeafHandler struct {
	reader      NamespaceVolumeBindingReader
	intakeStore OperationIntakeStore
	getRoute    RouteMetadata
	putRoute    RouteMetadata
	operationID OperationIDGenerator
	now         func() time.Time
	sink        audit.Sink
}

func (handler namespaceVolumeBindingLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := handler.routeForRequest(r)
	if !ok {
		writeNamespaceBindingError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	namespaceID, ok := namespaceVolumeBindingNamespaceID(r, route)
	if !ok {
		writeNamespaceBindingError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeNamespaceBindingValidationError(w, r, route, CodeInvalidID, http.StatusBadRequest, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	requestNamespaceID := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if requestNamespaceID == "" || requestNamespaceID != namespaceID {
		writeNamespaceBindingValidationError(w, r, route, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace does not match route namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
	if route.OperationID == "putNamespaceVolumeBinding" {
		handler.servePut(w, r, route, namespaceID)
		return
	}
	handler.serveGet(w, r, namespaceID)
}

func (handler namespaceVolumeBindingLeafHandler) routeForRequest(r *http.Request) (RouteMetadata, bool) {
	if r == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	switch method {
	case http.MethodGet:
		return handler.getRoute, handler.getRoute.Method != ""
	case http.MethodPut:
		return handler.putRoute, handler.putRoute.Method != ""
	default:
		return RouteMetadata{}, false
	}
}

func (handler namespaceVolumeBindingLeafHandler) serveGet(w http.ResponseWriter, r *http.Request, namespaceID string) {
	if handler.reader == nil {
		writeNamespaceBindingError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}

	binding, err := handler.reader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeNamespaceBindingError(w, r, http.StatusNotFound, CodeNamespaceNotFound, "namespace volume binding was not found", false)
			return
		}
		writeNamespaceBindingError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return
	}
	if binding.NamespaceID != namespaceID {
		writeNamespaceBindingError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}

	body, err := json.Marshal(NamespaceVolumeBindingResponseFromResource(binding))
	if err != nil {
		writeNamespaceBindingError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (handler namespaceVolumeBindingLeafHandler) servePut(w http.ResponseWriter, r *http.Request, route RouteMetadata, namespaceID string) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}

	body, err := decodeNamespaceVolumeBindingRequest(r)
	if err != nil {
		writeNamespaceBindingValidationError(w, r, route, CodeInvalidID, http.StatusBadRequest, "invalid namespace volume binding request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if body.NamespaceID != namespaceID {
		writeNamespaceBindingValidationError(w, r, route, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace does not match route namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	binding := body.toResource(now)
	if err := binding.Validate(); err != nil {
		writeNamespaceBindingValidationError(w, r, route, CodeInvalidID, http.StatusBadRequest, "invalid namespace volume binding request", []string{"invalid_binding"}, handler.sink)
		return
	}

	envelope, err := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               route,
		NamespaceID:         namespaceID,
		Resource:            operations.ResourceRef{Type: "namespace_volume_binding", ID: namespaceID},
		CanonicalRequest:    body,
		InputSummary:        namespaceVolumeBindingInputSummary(binding),
		Phase:               operations.OperationPhaseNamespaceVolumeBindingPutValidate,
		GenerateOperationID: handler.operationID,
		Now:                 handler.now,
	})
	if err != nil {
		writeOperationIntakeHTTPError(w, r, err)
		return
	}
	_ = writeJSON(w, http.StatusOK, envelope)
}

func namespaceVolumeBindingNamespaceID(r *http.Request, route RouteMetadata) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		return "", false
	}
	namespaceID := params["namespaceId"]
	if namespaceID == "" {
		return "", false
	}
	return namespaceID, true
}

type namespaceVolumeBindingRequestBody struct {
	NamespaceID       string                    `json:"namespace_id"`
	DefaultVolumeID   string                    `json:"default_volume_id"`
	AllowedCallers    []resources.AllowedCaller `json:"allowed_callers"`
	QuotaBytesDefault int64                     `json:"quota_bytes_default"`
	ExportPolicy      map[string]any            `json:"export_policy"`
	LifecyclePolicy   map[string]any            `json:"lifecycle_policy"`
	MountPolicy       map[string]any            `json:"mount_policy"`
	TemplatePolicy    map[string]any            `json:"template_policy"`
	Status            resources.NamespaceStatus `json:"status"`
}

func decodeNamespaceVolumeBindingRequest(r *http.Request) (namespaceVolumeBindingRequestBody, error) {
	var body namespaceVolumeBindingRequestBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return namespaceVolumeBindingRequestBody{}, err
	}
	if strings.TrimSpace(body.NamespaceID) == "" {
		return namespaceVolumeBindingRequestBody{}, errors.New("missing namespace_id")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return namespaceVolumeBindingRequestBody{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return namespaceVolumeBindingRequestBody{}, err
	}
	return body, nil
}

func (body namespaceVolumeBindingRequestBody) toResource(now time.Time) resources.NamespaceVolumeBinding {
	return resources.NamespaceVolumeBinding{
		NamespaceID:       body.NamespaceID,
		DefaultVolumeID:   body.DefaultVolumeID,
		AllowedCallers:    cloneResourceAllowedCallers(body.AllowedCallers),
		QuotaBytesDefault: body.QuotaBytesDefault,
		ExportPolicy:      cloneAnyMap(body.ExportPolicy),
		LifecyclePolicy:   cloneAnyMap(body.LifecyclePolicy),
		MountPolicy:       cloneAnyMap(body.MountPolicy),
		TemplatePolicy:    cloneAnyMap(body.TemplatePolicy),
		Status:            body.Status,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func namespaceVolumeBindingInputSummary(binding resources.NamespaceVolumeBinding) map[string]any {
	return map[string]any{
		"namespace_id":        binding.NamespaceID,
		"default_volume_id":   binding.DefaultVolumeID,
		"allowed_callers":     cloneResourceAllowedCallers(binding.AllowedCallers),
		"quota_bytes_default": binding.QuotaBytesDefault,
		"export_policy":       cloneAnyMap(binding.ExportPolicy),
		"lifecycle_policy":    cloneAnyMap(binding.LifecyclePolicy),
		"mount_policy":        cloneAnyMap(binding.MountPolicy),
		"template_policy":     cloneAnyMap(binding.TemplatePolicy),
		"status":              string(binding.Status),
	}
}

func cloneResourceAllowedCallers(callers []resources.AllowedCaller) []resources.AllowedCaller {
	if callers == nil {
		return nil
	}
	out := make([]resources.AllowedCaller, len(callers))
	for idx, caller := range callers {
		out[idx] = caller
		out[idx].Roles = append([]resources.CallerRole(nil), caller.Roles...)
	}
	return out
}

func writeNamespaceBindingError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}

func writeNamespaceBindingValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, code ErrorCode, status int, message string, validationErrors []string, sink audit.Sink) {
	writeValidationErrorWithAudit(w, r, route, auth.ParseRequestContext(r), code, status, message, validationErrors, sink)
}
