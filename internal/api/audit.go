package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
)

var auditEventCounter uint64

type deniedAuditEvent struct {
	Type             audit.EventType
	Route            RouteMetadata
	Status           int
	Code             ErrorCode
	Reason           string
	ValidationErrors []string
	RequestContext   auth.RequestContext
	Details          map[string]any
}

func emitDeniedAuditEvent(ctx context.Context, sink audit.Sink, r *http.Request, event deniedAuditEvent) {
	if sink == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	requestContext := mergeRequestContext(auth.ParseRequestContext(r), event.RequestContext)
	routeID := strings.TrimSpace(event.Route.Path)
	if routeID == "" {
		routeID = "unmatched"
	}

	details := deniedAuditDetails(r, event)
	auditEvent := audit.NewEvent(audit.Event{
		EventID:       newAuditEventID(),
		Type:          event.Type,
		Time:          time.Now().UTC(),
		CallerService: requestContext.CallerService,
		AuthorizedActor: audit.Actor{
			Type: requestContext.Actor.Type,
			ID:   requestContext.Actor.ID,
		},
		CorrelationID: CorrelationIDFromRequest(r),
		OperationID:   "",
		Resource: audit.Resource{
			Type:        "route",
			ID:          routeID,
			NamespaceID: requestContext.NamespaceID,
			Path:        requestPath(r),
		},
		Outcome: audit.OutcomeDenied,
		Reason:  event.Reason,
		Details: details,
	})

	// Denied responses are already fail-closed. Preserve the caller-visible
	// denial if the non-durable audit sink is unavailable.
	_ = sink.Emit(ctx, auditEvent)
}

func deniedAuditDetails(r *http.Request, event deniedAuditEvent) map[string]any {
	details := map[string]any{
		"method": requestMethod(r),
		"path":   requestPath(r),
		"route":  "unmatched",
		"status": event.Status,
	}
	if event.Route.Path != "" {
		details["route"] = event.Route.Path
	}
	if event.Route.OperationID != "" {
		details["route_operation_id"] = event.Route.OperationID
	}
	if event.Route.Class != "" {
		details["route_class"] = string(event.Route.Class)
	}
	if event.Route.RequiredRole != "" {
		details["required_role"] = string(event.Route.RequiredRole)
	}
	if event.Code != "" {
		details["error_code"] = string(event.Code)
	}
	if len(event.ValidationErrors) > 0 {
		details["validation_errors"] = event.ValidationErrors
	}
	for key, value := range event.Details {
		details[key] = value
	}
	return details
}

func mergeRequestContext(parsed auth.RequestContext, override auth.RequestContext) auth.RequestContext {
	if override.Authorization != "" {
		parsed.Authorization = override.Authorization
	}
	if override.IdempotencyKey != "" {
		parsed.IdempotencyKey = override.IdempotencyKey
	}
	if override.CorrelationID != "" {
		parsed.CorrelationID = override.CorrelationID
	}
	if override.NamespaceID != "" {
		parsed.NamespaceID = override.NamespaceID
	}
	if override.Actor.Type != "" {
		parsed.Actor.Type = override.Actor.Type
	}
	if override.Actor.ID != "" {
		parsed.Actor.ID = override.Actor.ID
	}
	if override.CallerService != "" {
		parsed.CallerService = override.CallerService
	}
	return parsed
}

func requestMethod(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Method
}

func requestPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.URL.Path
}

func newAuditEventID() string {
	counter := atomic.AddUint64(&auditEventCounter, 1)
	return fmt.Sprintf("evt_%d_%d", time.Now().UTC().UnixNano(), counter)
}
