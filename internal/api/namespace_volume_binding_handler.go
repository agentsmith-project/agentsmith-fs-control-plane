package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type NamespaceVolumeBindingReader interface {
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
}

type NamespaceVolumeBindingHandlerConfig struct {
	Reader            NamespaceVolumeBindingReader
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	AuditSink         audit.Sink
}

func NamespaceVolumeBindingHandler(config NamespaceVolumeBindingHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("getNamespaceVolumeBinding")
	leaf := namespaceVolumeBindingLeafHandler{
		reader: config.Reader,
		route:  route,
		sink:   config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, namespaceVolumeBindingRouteResolver{route: route}, config.AllowedCallers, config.AuditSink)
}

type namespaceVolumeBindingRouteResolver struct {
	route RouteMetadata
}

func (resolver namespaceVolumeBindingRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method != resolver.route.Method {
		return RouteMetadata{}, false
	}
	if _, ok := RoutePathParams(resolver.route.Path, r.URL.Path); !ok {
		return RouteMetadata{}, false
	}
	return resolver.route, true
}

type namespaceVolumeBindingLeafHandler struct {
	reader NamespaceVolumeBindingReader
	route  RouteMetadata
	sink   audit.Sink
}

func (handler namespaceVolumeBindingLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	namespaceID, ok := namespaceVolumeBindingNamespaceID(r)
	if !ok {
		writeNamespaceBindingError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeNamespaceBindingValidationError(w, r, handler.route, CodeInvalidID, http.StatusBadRequest, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	requestNamespaceID := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if requestNamespaceID == "" || requestNamespaceID != namespaceID {
		writeNamespaceBindingValidationError(w, r, handler.route, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace does not match route namespace", []string{"namespace_mismatch"}, handler.sink)
		return
	}
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

func namespaceVolumeBindingNamespaceID(r *http.Request) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	if strings.ToUpper(strings.TrimSpace(r.Method)) != http.MethodGet {
		return "", false
	}
	metadata, ok := RouteMetadataByOperationID("getNamespaceVolumeBinding")
	if !ok {
		return "", false
	}
	params, ok := RoutePathParams(metadata.Path, r.URL.Path)
	if !ok {
		return "", false
	}
	namespaceID := params["namespaceId"]
	if namespaceID == "" {
		return "", false
	}
	return namespaceID, true
}

func writeNamespaceBindingError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}

func writeNamespaceBindingValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, code ErrorCode, status int, message string, validationErrors []string, sink audit.Sink) {
	writeValidationErrorWithAudit(w, r, route, auth.ParseRequestContext(r), code, status, message, validationErrors, sink)
}
