package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operationinspect"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

type OperationInspectionReader interface {
	ReadOperation(ctx context.Context, operationID string) (operations.OperationRecord, error)
}

type OperationInspectionStoreReader interface {
	GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error)
}

type OperationInspectionHandlerConfig struct {
	Reader                    OperationInspectionReader
	StoreReader               OperationInspectionStoreReader
	StoredNamespaceAuthorizer operationinspect.StoredNamespaceAuthorizer
	PrincipalResolver         PrincipalResolver
	AllowedCallers            AllowedCallerPolicy
	AuditSink                 audit.Sink
}

type OperationInspectionPreflightPolicy struct {
	DeploymentGlobal AllowedCallerPolicy
}

func (policy OperationInspectionPreflightPolicy) AllowedCallers(r *http.Request) ([]auth.AllowedCaller, error) {
	callerService := strings.TrimSpace(r.Header.Get(auth.HeaderCallerService))
	callers := []auth.AllowedCaller{}
	if policy.DeploymentGlobal != nil {
		global, err := policy.DeploymentGlobal.AllowedCallers(r)
		if err != nil {
			return nil, err
		}
		callers = append(callers, global...)
	}
	if callerService != "" {
		callers = append(callers, auth.AllowedCaller{
			CallerService: callerService,
			Kind:          auth.CallerKindProduct,
			Roles:         []auth.Role{auth.RoleOperationInspector},
		})
	}
	if len(callers) == 0 {
		return nil, routePolicyInternalError("operation_inspection_policy_not_configured")
	}
	return callers, nil
}

func OperationInspectionHandler(config OperationInspectionHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("getOperation")
	reader := config.Reader
	if reader == nil && config.StoreReader != nil {
		reader = operationInspectionStoreReader{reader: config.StoreReader}
	}
	allowedCallers := config.AllowedCallers
	if allowedCallers == nil {
		allowedCallers = operationInspectionMissingPolicy{}
	}
	leaf := operationInspectionLeafHandler{
		route:                     route,
		reader:                    reader,
		storedNamespaceAuthorizer: config.StoredNamespaceAuthorizer,
		allowedCallers:            allowedCallers,
		sink:                      config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, operationInspectionRouteResolver{route: route}, allowedCallers, config.AuditSink)
}

type operationInspectionMissingPolicy struct{}

func (operationInspectionMissingPolicy) AllowedCallers(*http.Request) ([]auth.AllowedCaller, error) {
	return nil, routePolicyInternalError("operation_inspection_policy_not_configured")
}

type operationInspectionStoreReader struct {
	reader OperationInspectionStoreReader
}

func (reader operationInspectionStoreReader) ReadOperation(ctx context.Context, operationID string) (operations.OperationRecord, error) {
	if reader.reader == nil {
		return operations.OperationRecord{}, operationinspect.ErrMissingOperationReader
	}
	return reader.reader.GetOperation(ctx, operationID)
}

type operationInspectionRouteResolver struct {
	route RouteMetadata
}

func (resolver operationInspectionRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil || strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	if _, ok := RoutePathParams(resolver.route.Path, r.URL.Path); !ok {
		return RouteMetadata{}, false
	}
	return resolver.route, true
}

type operationInspectionLeafHandler struct {
	route                     RouteMetadata
	reader                    OperationInspectionReader
	storedNamespaceAuthorizer operationinspect.StoredNamespaceAuthorizer
	allowedCallers            AllowedCallerPolicy
	sink                      audit.Sink
}

func (handler operationInspectionLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeOperationInspectionError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	operationID, ok := operationInspectionOperationID(r, handler.route)
	if !ok {
		writeOperationInspectionError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, operationID); err != nil {
		writeValidationErrorWithAudit(w, r, handler.route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid operation id", []string{"invalid_operation_id"}, handler.sink)
		return
	}
	if handler.reader == nil || handler.allowedCallers == nil {
		writeOperationInspectionError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	caller, err := handler.inspectionCaller(r, requestContext)
	if err != nil {
		writeOperationInspectionError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	record, err := operationinspect.InspectOperation(r.Context(), handler.reader, handler.storedNamespaceAuthorizer, operationinspect.Request{
		OperationID:  operationID,
		RouteClass:   handler.route.Class,
		NamespaceID:  requestContext.NamespaceID,
		RequiredRole: handler.route.RequiredRole,
		Caller:       caller,
	})
	if err != nil {
		handler.writeInspectionError(w, r, requestContext, caller, err)
		return
	}
	record = pruneOperationInspectionRecord(record)
	_ = writeJSON(w, http.StatusOK, record)
}

func (handler operationInspectionLeafHandler) inspectionCaller(r *http.Request, requestContext auth.RequestContext) (auth.AllowedCaller, error) {
	callers, err := handler.allowedCallers.AllowedCallers(r)
	if err != nil {
		return auth.AllowedCaller{}, err
	}
	for _, caller := range callers {
		if !auth.CallerNotAllowed(requestContext.CallerService, auth.RoleOperationInspector, []auth.AllowedCaller{caller}) {
			return caller, nil
		}
	}
	return auth.AllowedCaller{}, errors.New("operation inspection caller not found")
}

func (handler operationInspectionLeafHandler) writeInspectionError(w http.ResponseWriter, r *http.Request, requestContext auth.RequestContext, caller auth.AllowedCaller, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeOperationInspectionError(w, r, http.StatusNotFound, CodeOperationNotFound, "operation was not found", false)
	case errors.Is(err, operationinspect.ErrInspectionDenied):
		if caller.Kind == auth.CallerKindProduct {
			writePolicyDeniedErrorWithAudit(w, r, handler.route, requestContext, CodeOperationNotFound, http.StatusNotFound, false, "operation was not found", []string{"operation_inspection_denied"}, handler.sink)
			return
		}
		writePolicyDeniedErrorWithAudit(w, r, handler.route, requestContext, CodeRoleNotAllowed, http.StatusForbidden, false, "operation inspection denied", []string{"operation_inspection_denied"}, handler.sink)
	case errors.Is(err, operationinspect.ErrStoredNamespaceAuthorizationUnavailable):
		writeOperationInspectionError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
	case errors.Is(err, operationinspect.ErrInvalidStoredNamespaceAuthorizationState):
		writeOperationInspectionError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
	case errors.Is(err, operationinspect.ErrMissingOperationID), errors.Is(err, operationinspect.ErrMissingOperationReader):
		writeOperationInspectionError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
	default:
		writeOperationInspectionError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
	}
}

func operationInspectionOperationID(r *http.Request, route RouteMetadata) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		return "", false
	}
	operationID := strings.TrimSpace(params["operationId"])
	return operationID, operationID != ""
}

func writeOperationInspectionError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}

func pruneOperationInspectionRecord(record operations.OperationRecord) operations.OperationRecord {
	record = record.Sanitized()
	record.InputSummary = pruneSensitiveAnyMap(record.InputSummary)
	record.JVSJSONOutput = pruneSensitiveAny(record.JVSJSONOutput)
	record.VerificationResult = pruneSensitiveAny(record.VerificationResult)
	if record.Error != nil {
		err := *record.Error
		err.Details = pruneSensitiveAnyMap(err.Details)
		record.Error = &err
	}
	return record
}

func pruneSensitiveAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return pruneSensitiveAnyMap(typed)
	case map[string]string:
		out := map[string]any{}
		for key, value := range typed {
			if operationInspectionSensitiveKey(key) {
				continue
			}
			out[key] = value
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, pruneSensitiveAny(item))
		}
		return out
	default:
		return value
	}
}

func pruneSensitiveAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range values {
		if operationInspectionSensitiveKey(key) {
			continue
		}
		out[key] = pruneSensitiveAny(value)
	}
	return out
}

func operationInspectionSensitiveKey(key string) bool {
	if credentialLikeKey(key) {
		return true
	}
	normalized := normalizeSensitiveKey(key)
	switch normalized {
	case "rawpath", "stdout", "stderr":
		return true
	default:
		return false
	}
}
