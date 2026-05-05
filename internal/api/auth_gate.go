package api

import (
	"errors"
	"net/http"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
)

type PrincipalResolver interface {
	ResolvePrincipal(*http.Request) (auth.AuthenticatedPrincipal, error)
}

type RouteClassResolver interface {
	ResolveRouteClass(*http.Request) (RouteMetadata, bool)
}

type AllowedCallerPolicy interface {
	AllowedCallers(*http.Request) ([]auth.AllowedCaller, error)
}

func AuthGate(next http.Handler, principalResolver PrincipalResolver, routeResolver RouteClassResolver, callerPolicy AllowedCallerPolicy) http.Handler {
	if next == nil {
		next = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := resolveRouteClass(routeResolver, r)
		if !ok {
			PathDeniedHandler().ServeHTTP(w, r)
			return
		}

		principal, err := resolvePrincipal(principalResolver, r)
		if err != nil {
			writeValidationError(w, r, route, CodeAuthenticationFailed, http.StatusUnauthorized, "authenticated principal validation failed", []string{"principal_resolver_failed"})
			return
		}

		requestContext, err := auth.ValidateAuthenticatedRequestForRoute(r, principal, auth.RouteValidation{
			Class:    route.Class,
			Mutating: route.Mutating,
		})
		if err != nil {
			writeAuthGateValidationError(w, r, route, err)
			return
		}

		if denied, labels := requiredRoleDenied(r, requestContext, route, callerPolicy); denied {
			writeValidationError(w, r, route, CodeCapabilityDenied, http.StatusForbidden, "caller is not allowed for required route role", labels)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func resolveRouteClass(resolver RouteClassResolver, r *http.Request) (RouteMetadata, bool) {
	if resolver == nil {
		return RouteMetadata{}, false
	}
	return resolver.ResolveRouteClass(r)
}

func resolvePrincipal(resolver PrincipalResolver, r *http.Request) (auth.AuthenticatedPrincipal, error) {
	if resolver == nil {
		return auth.AuthenticatedPrincipal{}, auth.ErrMissingAuthenticatedPrincipal
	}
	return resolver.ResolvePrincipal(r)
}

func requiredRoleDenied(r *http.Request, requestContext auth.RequestContext, route RouteMetadata, policy AllowedCallerPolicy) (bool, []string) {
	if route.RequiredRole == "" {
		return false, nil
	}
	if policy == nil {
		return true, []string{"allowed_caller_policy_missing"}
	}

	allowedCallers, err := policy.AllowedCallers(r)
	if err != nil {
		return true, []string{"allowed_caller_policy_failed"}
	}
	if auth.CallerNotAllowed(requestContext.CallerService, route.RequiredRole, allowedCallers) {
		return true, []string{"required_role_not_allowed"}
	}

	return false, nil
}

func writeAuthGateValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, err error) {
	code := CodeAuthenticationFailed
	status := http.StatusBadRequest
	message := "authenticated request validation failed"

	if authenticationFailed(err) {
		status = http.StatusUnauthorized
	} else if errors.Is(err, auth.ErrMissingNamespaceID) {
		code = CodeResourceNamespaceMismatch
		message = "namespace header is required for namespace-bound route"
	}

	writeValidationError(w, r, route, code, status, message, validationErrorLabels(err))
}

func authenticationFailed(err error) bool {
	return errors.Is(err, auth.ErrMissingAuthorization) ||
		errors.Is(err, auth.ErrMissingAuthenticatedPrincipal) ||
		errors.Is(err, auth.ErrCallerServiceMismatch)
}

func writeValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, code ErrorCode, status int, message string, validationErrors []string) {
	var operationID *string
	if route.OperationID != "" {
		operationID = &route.OperationID
	}

	details := map[string]any{}
	if len(validationErrors) > 0 {
		details["validation_errors"] = validationErrors
	}

	envelope := NewErrorEnvelope(
		code,
		message,
		false,
		CorrelationIDFromRequest(r),
		operationID,
		details,
	)

	_ = WriteErrorEnvelope(w, status, envelope)
}

func validationErrorLabels(err error) []string {
	known := []struct {
		err   error
		label string
	}{
		{err: auth.ErrMissingAuthorization, label: "missing_authorization"},
		{err: auth.ErrMissingCorrelationID, label: "missing_correlation_id"},
		{err: auth.ErrMissingCallerService, label: "missing_caller_service"},
		{err: auth.ErrMissingIdempotencyKey, label: "missing_idempotency_key"},
		{err: auth.ErrMissingNamespaceID, label: "missing_namespace_id"},
		{err: auth.ErrMissingActor, label: "missing_actor"},
		{err: auth.ErrMissingAuthenticatedPrincipal, label: "missing_authenticated_principal"},
		{err: auth.ErrCallerServiceMismatch, label: "caller_service_mismatch"},
		{err: auth.ErrUnknownRouteClass, label: "unknown_route_class"},
	}

	labels := make([]string, 0, len(known))
	for _, item := range known {
		if errors.Is(err, item.err) {
			labels = append(labels, item.label)
		}
	}
	if len(labels) == 0 && err != nil {
		labels = append(labels, "validation_failed")
	}
	return labels
}
