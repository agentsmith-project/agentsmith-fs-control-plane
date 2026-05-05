package operationinspect

import (
	"context"
	"errors"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

var (
	ErrInspectionDenied                         = errors.New("operation inspection denied")
	ErrMissingOperationID                       = errors.New("missing operation id")
	ErrMissingOperationReader                   = errors.New("missing operation reader")
	ErrStoredNamespaceAuthorizationUnavailable  = errors.New("stored namespace operation inspection authorization unavailable")
	ErrInvalidStoredNamespaceAuthorizationState = errors.New("invalid stored namespace operation inspection authorization state")
)

type OperationReader interface {
	ReadOperation(ctx context.Context, operationID string) (operations.OperationRecord, error)
}

type StoredNamespaceAuthorizer interface {
	AllowsOperationInspection(ctx context.Context, namespaceID string, caller auth.AllowedCaller) bool
}

type StoredNamespaceAuthorizerWithError interface {
	AllowsOperationInspectionWithError(ctx context.Context, namespaceID string, caller auth.AllowedCaller) (bool, error)
}

type Request struct {
	OperationID  string
	RouteClass   auth.RouteClass
	NamespaceID  string
	RequiredRole auth.Role
	Caller       auth.AllowedCaller
}

func InspectOperation(ctx context.Context, reader OperationReader, authorizer StoredNamespaceAuthorizer, request Request) (operations.OperationRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if reader == nil {
		return operations.OperationRecord{}, ErrMissingOperationReader
	}
	operationID := strings.TrimSpace(request.OperationID)
	if operationID == "" {
		return operations.OperationRecord{}, ErrMissingOperationID
	}
	record, err := reader.ReadOperation(ctx, operationID)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	allowed, err := canInspect(ctx, request, authorizer, record)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if !allowed {
		return operations.OperationRecord{}, ErrInspectionDenied
	}
	return record.Sanitized(), nil
}

func canInspect(ctx context.Context, request Request, authorizer StoredNamespaceAuthorizer, record operations.OperationRecord) (bool, error) {
	if !routeCanCarryNamespaceInspection(request.RouteClass) {
		return false, nil
	}
	if hasGlobalInspectionCapability(request.Caller) {
		return true, nil
	}
	if request.RequiredRole != auth.RoleOperationInspector {
		return false, nil
	}
	if request.Caller.Kind != auth.CallerKindProduct {
		return false, nil
	}
	storedNamespaceID := strings.TrimSpace(record.NamespaceID)
	if storedNamespaceID == "" {
		return false, nil
	}
	if requestNamespaceID := strings.TrimSpace(request.NamespaceID); requestNamespaceID != "" && auth.NamespaceMismatch(requestNamespaceID, storedNamespaceID) {
		return false, nil
	}
	if authorizer == nil {
		return false, nil
	}
	return allowsStoredNamespaceInspection(ctx, authorizer, storedNamespaceID, request.Caller)
}

func allowsStoredNamespaceInspection(ctx context.Context, authorizer StoredNamespaceAuthorizer, namespaceID string, caller auth.AllowedCaller) (bool, error) {
	if authorizerWithError, ok := authorizer.(StoredNamespaceAuthorizerWithError); ok {
		return authorizerWithError.AllowsOperationInspectionWithError(ctx, namespaceID, caller)
	}
	return authorizer.AllowsOperationInspection(ctx, namespaceID, caller), nil
}

func routeCanCarryNamespaceInspection(class auth.RouteClass) bool {
	switch class {
	case auth.RouteClassNamespaceBound, auth.RouteClassOperationInspection:
		return true
	default:
		return false
	}
}

func hasGlobalInspectionCapability(caller auth.AllowedCaller) bool {
	switch caller.Kind {
	case auth.CallerKindAdmin, auth.CallerKindOperator:
	default:
		return false
	}
	return !auth.CallerNotAllowed(caller.CallerService, auth.RoleOperatorAdmin, []auth.AllowedCaller{caller})
}
