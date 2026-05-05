package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceauth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type NamespaceVolumeBindingAllowedCallerPolicy struct {
	Reader NamespaceVolumeBindingReader
}

func (policy NamespaceVolumeBindingAllowedCallerPolicy) AllowedCallers(r *http.Request) ([]auth.AllowedCaller, error) {
	route, routeOK := RouteMetadataForRequest(r)
	namespaceID, err := namespaceIDForBindingPolicy(r, route, routeOK)
	if err != nil {
		return nil, err
	}
	if policy.Reader == nil {
		return nil, NewAllowedCallerPolicyError(CodeInternalError, http.StatusInternalServerError, false, "internal server error", "allowed_caller_policy_not_configured")
	}

	binding, err := policy.Reader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, NewAllowedCallerPolicyError(CodeNamespaceNotFound, http.StatusNotFound, false, "namespace was not found", "namespace_not_found")
		}
		return nil, NewAllowedCallerPolicyError(CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", "store_unavailable")
	}
	if strings.TrimSpace(binding.NamespaceID) != namespaceID {
		return nil, NewAllowedCallerPolicyError(CodeInternalError, http.StatusInternalServerError, false, "internal server error", "binding_namespace_mismatch")
	}
	if err := binding.Validate(); err != nil {
		return nil, NewAllowedCallerPolicyError(CodeInternalError, http.StatusInternalServerError, false, "internal server error", "stored_binding_invalid")
	}
	if binding.Status == resources.NamespaceStatusDisabled && routeOK && route.Class == auth.RouteClassNamespaceBound && route.Mutating {
		return nil, NewAllowedCallerPolicyError(CodeNamespaceDisabled, http.StatusForbidden, false, "namespace is disabled", "namespace_disabled")
	}

	callers := make([]auth.AllowedCaller, 0, len(binding.AllowedCallers))
	for _, caller := range binding.AllowedCallers {
		mapped, ok := namespaceauth.MapAllowedCaller(caller)
		if !ok {
			return nil, NewAllowedCallerPolicyError(CodeInternalError, http.StatusInternalServerError, false, "internal server error", "stored_caller_invalid")
		}
		callers = append(callers, mapped)
	}
	return callers, nil
}

func namespaceIDForBindingPolicy(r *http.Request, route RouteMetadata, routeOK bool) (string, error) {
	headerNamespaceID := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if headerNamespaceID != "" {
		if err := pathresolver.ValidateID(pathresolver.NamespaceID, headerNamespaceID); err != nil {
			return "", NewAllowedCallerPolicyError(CodeInvalidID, http.StatusBadRequest, false, "invalid namespace id", "invalid_namespace_id")
		}
	}

	if routeOK && strings.Contains(route.Path, "{namespaceId}") {
		params, ok := RoutePathParams(route.Path, r.URL.Path)
		if !ok {
			return "", NewAllowedCallerPolicyError(CodeResourceNamespaceMismatch, http.StatusBadRequest, false, "request namespace does not match route namespace", "namespace_mismatch")
		}
		pathNamespaceID := params["namespaceId"]
		if err := pathresolver.ValidateID(pathresolver.NamespaceID, pathNamespaceID); err != nil {
			return "", NewAllowedCallerPolicyError(CodeInvalidID, http.StatusBadRequest, false, "invalid namespace id", "invalid_namespace_id")
		}
		if headerNamespaceID != "" && headerNamespaceID != pathNamespaceID {
			return "", NewAllowedCallerPolicyError(CodeResourceNamespaceMismatch, http.StatusBadRequest, false, "request namespace does not match route namespace", "namespace_mismatch")
		}
		return pathNamespaceID, nil
	}

	return headerNamespaceID, nil
}
