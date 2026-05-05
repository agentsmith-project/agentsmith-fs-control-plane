package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
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

type requestContextKey struct{}

func RequestContextFromRequest(r *http.Request) (auth.RequestContext, bool) {
	if r == nil {
		return auth.RequestContext{}, false
	}
	requestContext, ok := r.Context().Value(requestContextKey{}).(auth.RequestContext)
	return requestContext, ok
}

func requestWithBoundRequestContext(r *http.Request, requestContext auth.RequestContext) *http.Request {
	if r == nil {
		return r
	}
	ctx := context.WithValue(r.Context(), requestContextKey{}, requestContext)
	return r.WithContext(ctx)
}

func AuthGate(next http.Handler, principalResolver PrincipalResolver, routeResolver RouteClassResolver, callerPolicy AllowedCallerPolicy) http.Handler {
	return AuthGateWithAuditSink(next, principalResolver, routeResolver, callerPolicy, nil)
}

func AuthGateWithAuditSink(next http.Handler, principalResolver PrincipalResolver, routeResolver RouteClassResolver, callerPolicy AllowedCallerPolicy, sink audit.Sink) http.Handler {
	if next == nil {
		next = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := resolveRouteClass(routeResolver, r)
		if !ok {
			PathDeniedHandler().ServeHTTP(w, r)
			emitDeniedAuditEvent(r.Context(), sink, r, deniedAuditEvent{
				Type:   audit.EventTypePathDenied,
				Status: http.StatusNotFound,
				Code:   CodePathDenied,
				Reason: "path denied",
			})
			return
		}

		principal, err := resolvePrincipal(principalResolver, r)
		if err != nil {
			writeValidationErrorWithAudit(w, r, route, auth.RequestContext{}, CodeAuthenticationFailed, http.StatusUnauthorized, "authenticated principal validation failed", []string{"principal_resolver_failed"}, sink)
			return
		}

		requestContext, err := auth.ValidateAuthenticatedRequestForRoute(r, principal, auth.RouteValidation{
			Class:    route.Class,
			Mutating: route.Mutating,
		})
		if err != nil {
			writeAuthGateValidationError(w, r, route, requestContext, err, sink)
			return
		}

		if denied, code, status, retryable, message, labels := requiredRoleDenied(r, requestContext, route, callerPolicy); denied {
			writePolicyDeniedErrorWithAudit(w, r, route, requestContext, code, status, retryable, message, labels, sink)
			return
		}

		next.ServeHTTP(w, requestWithBoundRequestContext(r, requestContext))
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

type AllowedCallerPolicyError struct {
	Code      ErrorCode
	Status    int
	Retryable bool
	Message   string
	Labels    []string
}

func NewAllowedCallerPolicyError(code ErrorCode, status int, retryable bool, message string, labels ...string) *AllowedCallerPolicyError {
	return &AllowedCallerPolicyError{
		Code:      code,
		Status:    status,
		Retryable: retryable,
		Message:   message,
		Labels:    append([]string(nil), labels...),
	}
}

func (err *AllowedCallerPolicyError) Error() string {
	if err == nil {
		return ""
	}
	return string(err.Code)
}

func requiredRoleDenied(r *http.Request, requestContext auth.RequestContext, route RouteMetadata, policy AllowedCallerPolicy) (bool, ErrorCode, int, bool, string, []string) {
	if route.RequiredRole == "" {
		return false, "", 0, false, "", nil
	}
	if policy == nil {
		return true, CodeCallerNotAllowed, http.StatusForbidden, false, "caller service is not allowed for route", []string{"allowed_caller_policy_missing"}
	}

	allowedCallers, err := policy.AllowedCallers(r)
	if err != nil {
		var policyErr *AllowedCallerPolicyError
		if errors.As(err, &policyErr) && policyErr != nil {
			return true, policyErr.Code, policyErr.Status, policyErr.Retryable, policyErr.Message, policyErr.Labels
		}
		return true, CodeInternalError, http.StatusInternalServerError, false, "internal server error", []string{"allowed_caller_policy_failed"}
	}
	switch auth.CallerRoleDenialReasonFor(requestContext.CallerService, route.RequiredRole, allowedCallers) {
	case auth.CallerRoleAllowed:
		return false, "", 0, false, "", nil
	case auth.CallerServiceNotAllowed:
		return true, CodeCallerNotAllowed, http.StatusForbidden, false, "caller service is not allowed for route", []string{"caller_not_allowed"}
	case auth.CallerRoleNotAllowed:
		return true, CodeRoleNotAllowed, http.StatusForbidden, false, "caller role is not allowed for route", []string{"required_role_not_allowed"}
	default:
		return true, CodeRoleNotAllowed, http.StatusForbidden, false, "caller role is not allowed for route", []string{"required_role_not_allowed"}
	}
}

func writeAuthGateValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, err error, sink audit.Sink) {
	code, status, message, labels := authGateValidationError(err)
	writeValidationErrorWithAudit(w, r, route, requestContext, code, status, message, labels, sink)
}

func authGateValidationError(err error) (ErrorCode, int, string, []string) {
	code := CodeAuthenticationFailed
	status := http.StatusBadRequest
	message := "authenticated request validation failed"

	if authenticationFailed(err) {
		status = http.StatusUnauthorized
	} else if errors.Is(err, auth.ErrMissingNamespaceID) {
		code = CodeResourceNamespaceMismatch
		message = "namespace header is required for namespace-bound route"
	}

	return code, status, message, validationErrorLabels(err)
}

func authenticationFailed(err error) bool {
	return errors.Is(err, auth.ErrMissingAuthorization) ||
		errors.Is(err, auth.ErrMissingAuthenticatedPrincipal) ||
		errors.Is(err, auth.ErrCallerServiceMismatch)
}

func writeValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, code ErrorCode, status int, message string, validationErrors []string) {
	writeValidationErrorWithAudit(w, r, route, auth.RequestContext{}, code, status, message, validationErrors, nil)
}

func writeValidationErrorWithAudit(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, code ErrorCode, status int, message string, validationErrors []string, sink audit.Sink) {
	writePolicyDeniedErrorWithAudit(w, r, route, requestContext, code, status, false, message, validationErrors, sink)
}

func writePolicyDeniedErrorWithAudit(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, code ErrorCode, status int, retryable bool, message string, validationErrors []string, sink audit.Sink) {
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
		retryable,
		CorrelationIDFromRequest(r),
		operationID,
		details,
	)

	_ = WriteErrorEnvelope(w, status, envelope)
	emitDeniedAuditEvent(r.Context(), sink, r, deniedAuditEvent{
		Type:             audit.EventTypeAuthzDenied,
		Route:            route,
		Status:           status,
		Code:             code,
		Reason:           message,
		ValidationErrors: validationErrors,
		RequestContext:   requestContext,
	})
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
