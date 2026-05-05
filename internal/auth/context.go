package auth

import (
	"errors"
	"net/http"
	"strings"
)

const (
	HeaderAuthorization  = "Authorization"
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderCorrelationID  = "X-Correlation-Id"
	HeaderNamespaceID    = "X-AFSCP-Namespace-Id"
	HeaderActorType      = "X-AFSCP-Actor-Type"
	HeaderActorID        = "X-AFSCP-Actor-Id"
	HeaderCallerService  = "X-AFSCP-Caller-Service"
)

var (
	ErrMissingAuthorization  = errors.New("missing authorization")
	ErrMissingCorrelationID  = errors.New("missing correlation id")
	ErrMissingCallerService  = errors.New("missing caller service")
	ErrMissingIdempotencyKey = errors.New("missing idempotency key")
	ErrMissingNamespaceID    = errors.New("missing namespace id")
	ErrMissingActor          = errors.New("missing actor")

	ErrMissingAuthenticatedPrincipal = errors.New("missing authenticated principal")
	ErrCallerServiceMismatch         = errors.New("caller service does not match authenticated principal")
	ErrUnknownRouteClass             = errors.New("unknown route class")
)

type Actor struct {
	Type string
	ID   string
}

type RequestContext struct {
	Authorization  string
	IdempotencyKey string
	CorrelationID  string
	NamespaceID    string
	Actor          Actor
	CallerService  string
}

type AuthenticatedPrincipal struct {
	Subject                string
	CanonicalCallerService string
}

type RouteClass string

const (
	RouteClassNamespaceBound      RouteClass = "namespace_bound"
	RouteClassVolumeGlobal        RouteClass = "volume_global"
	RouteClassOperationInspection RouteClass = "operation_inspection"
)

type RouteValidation struct {
	Class    RouteClass
	Mutating bool
}

func (class RouteClass) RequiresRequestNamespace() bool {
	return class == RouteClassNamespaceBound
}

func ParseRequestContext(r *http.Request) RequestContext {
	if r == nil {
		return RequestContext{}
	}

	return RequestContext{
		Authorization:  headerValue(r, HeaderAuthorization),
		IdempotencyKey: headerValue(r, HeaderIdempotencyKey),
		CorrelationID:  headerValue(r, HeaderCorrelationID),
		NamespaceID:    headerValue(r, HeaderNamespaceID),
		Actor: Actor{
			Type: headerValue(r, HeaderActorType),
			ID:   headerValue(r, HeaderActorID),
		},
		CallerService: headerValue(r, HeaderCallerService),
	}
}

func ValidateAuthenticatedRequest(r *http.Request, principal AuthenticatedPrincipal) (RequestContext, error) {
	ctx := ParseRequestContext(r)
	bound, bindErr := BindAuthenticatedPrincipal(ctx, principal)

	method := ""
	if r != nil {
		method = r.Method
	}

	return bound, errors.Join(bindErr, ValidateRequestContext(bound, method))
}

func ValidateAuthenticatedRequestForRoute(r *http.Request, principal AuthenticatedPrincipal, route RouteValidation) (RequestContext, error) {
	ctx := ParseRequestContext(r)
	bound, bindErr := BindAuthenticatedPrincipal(ctx, principal)

	return bound, errors.Join(bindErr, ValidateRequestContextForRoute(bound, route))
}

func BindAuthenticatedPrincipal(ctx RequestContext, principal AuthenticatedPrincipal) (RequestContext, error) {
	canonicalService := normalize(principal.CanonicalCallerService)
	if canonicalService == "" {
		return ctx, ErrMissingAuthenticatedPrincipal
	}

	callerService := normalize(ctx.CallerService)
	if callerService == "" {
		ctx.CallerService = canonicalService
		return ctx, nil
	}
	if callerService != canonicalService {
		return ctx, ErrCallerServiceMismatch
	}

	ctx.CallerService = canonicalService
	return ctx, nil
}

func ValidateRequestContext(ctx RequestContext, method string) error {
	return errors.Join(validateRequestContext(ctx, IsMutatingMethod(method))...)
}

func ValidateRequestContextForRoute(ctx RequestContext, route RouteValidation) error {
	errs := validateRequestContext(ctx, route.Mutating)

	switch route.Class {
	case RouteClassNamespaceBound:
		if normalize(ctx.NamespaceID) == "" {
			errs = append(errs, ErrMissingNamespaceID)
		}
	case RouteClassVolumeGlobal, RouteClassOperationInspection:
	default:
		errs = append(errs, ErrUnknownRouteClass)
	}

	return errors.Join(errs...)
}

func validateRequestContext(ctx RequestContext, mutating bool) []error {
	var errs []error

	if normalize(ctx.Authorization) == "" {
		errs = append(errs, ErrMissingAuthorization)
	}
	if normalize(ctx.CorrelationID) == "" {
		errs = append(errs, ErrMissingCorrelationID)
	}
	if normalize(ctx.CallerService) == "" {
		errs = append(errs, ErrMissingCallerService)
	}
	if mutating {
		if normalize(ctx.IdempotencyKey) == "" {
			errs = append(errs, ErrMissingIdempotencyKey)
		}
		if normalize(ctx.Actor.Type) == "" || normalize(ctx.Actor.ID) == "" {
			errs = append(errs, ErrMissingActor)
		}
	}

	return errs
}

func IsMutatingMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func headerValue(r *http.Request, name string) string {
	return normalize(r.Header.Get(name))
}

func normalize(value string) string {
	return strings.TrimSpace(value)
}
