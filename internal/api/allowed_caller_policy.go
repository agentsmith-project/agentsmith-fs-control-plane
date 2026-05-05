package api

import (
	"net/http"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
)

type StaticAllowedCallerPolicy struct {
	callers []auth.AllowedCaller
}

func NewStaticAllowedCallerPolicy(callers []auth.AllowedCaller) StaticAllowedCallerPolicy {
	return StaticAllowedCallerPolicy{callers: cloneAllowedCallers(callers)}
}

func (policy StaticAllowedCallerPolicy) AllowedCallers(*http.Request) ([]auth.AllowedCaller, error) {
	return cloneAllowedCallers(policy.callers), nil
}

type RouteAwareAllowedCallerPolicy struct {
	DeploymentGlobal    AllowedCallerPolicy
	DeploymentNamespace AllowedCallerPolicy
	NamespaceBinding    AllowedCallerPolicy
}

func (policy RouteAwareAllowedCallerPolicy) AllowedCallers(r *http.Request) ([]auth.AllowedCaller, error) {
	route, ok := RouteMetadataForRequest(r)
	if !ok {
		return nil, routePolicyInternalError("route_not_recognized")
	}

	selected, reason := policy.policyForRoute(route)
	if selected == nil {
		if reason == "" {
			reason = "allowed_caller_policy_not_configured"
		}
		return nil, routePolicyInternalError(reason)
	}
	return selected.AllowedCallers(r)
}

func (policy RouteAwareAllowedCallerPolicy) policyForRoute(route RouteMetadata) (AllowedCallerPolicy, string) {
	switch route.OperationID {
	case "ensureVolume", "getVolumeHealth":
		return policy.DeploymentGlobal, "deployment_global_policy_not_configured"
	case "upsertNamespace", "disableNamespace", "putNamespaceVolumeBinding":
		return policy.DeploymentNamespace, "deployment_namespace_policy_not_configured"
	case "getOperation":
		return nil, "operation_inspection_policy_not_configured"
	}

	if route.Class == auth.RouteClassNamespaceBound {
		return policy.NamespaceBinding, "namespace_binding_policy_not_configured"
	}
	return nil, "allowed_caller_policy_not_configured"
}

func routePolicyInternalError(reason string) *AllowedCallerPolicyError {
	return NewAllowedCallerPolicyError(CodeInternalError, http.StatusInternalServerError, false, "internal server error", reason)
}

func cloneAllowedCallers(callers []auth.AllowedCaller) []auth.AllowedCaller {
	if callers == nil {
		return nil
	}
	cloned := make([]auth.AllowedCaller, len(callers))
	for i, caller := range callers {
		cloned[i] = caller
		cloned[i].Roles = append([]auth.Role(nil), caller.Roles...)
	}
	return cloned
}
