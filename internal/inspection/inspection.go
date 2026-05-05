package inspection

import (
	"context"
	"errors"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

var (
	ErrInspectionDenied       = errors.New("operation inspection denied")
	ErrMissingOperationID     = errors.New("missing operation id")
	ErrMissingOperationReader = errors.New("missing operation reader")
)

type OperationReader interface {
	ReadOperation(ctx context.Context, operationID string) (operations.OperationRecord, error)
}

type StoredNamespaceAuthorizer interface {
	AllowsOperationInspection(ctx context.Context, namespaceID string, caller auth.AllowedCaller) bool
}

type Request struct {
	OperationID  string
	RouteClass   auth.RouteClass
	NamespaceID  string
	RequiredRole auth.Role
	Caller       auth.AllowedCaller
}

type Service struct {
	Reader                    OperationReader
	StoredNamespaceAuthorizer StoredNamespaceAuthorizer
}

func (service Service) InspectOperation(ctx context.Context, request Request) (operations.OperationRecord, error) {
	return InspectOperation(ctx, service.Reader, service.StoredNamespaceAuthorizer, request)
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
	if !canInspect(ctx, request, authorizer, record) {
		return operations.OperationRecord{}, ErrInspectionDenied
	}

	return record.Sanitized(), nil
}

func canInspect(ctx context.Context, request Request, authorizer StoredNamespaceAuthorizer, record operations.OperationRecord) bool {
	if !routeCanCarryNamespaceInspection(request.RouteClass) {
		return false
	}
	if hasGlobalInspectionCapability(request.Caller) {
		return true
	}
	if request.RequiredRole != auth.RoleOperationInspector {
		return false
	}
	if request.Caller.Kind != auth.CallerKindProduct {
		return false
	}
	storedNamespaceID := strings.TrimSpace(record.NamespaceID)
	if storedNamespaceID == "" {
		return false
	}
	if requestNamespaceID := strings.TrimSpace(request.NamespaceID); requestNamespaceID != "" && auth.NamespaceMismatch(requestNamespaceID, storedNamespaceID) {
		return false
	}
	if authorizer == nil {
		return false
	}

	return authorizer.AllowsOperationInspection(ctx, storedNamespaceID, request.Caller)
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
