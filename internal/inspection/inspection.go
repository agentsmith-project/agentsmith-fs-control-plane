package inspection

import (
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
	ReadOperation(operationID string) (operations.OperationRecord, error)
}

type Request struct {
	OperationID  string
	RouteClass   auth.RouteClass
	NamespaceID  string
	RequiredRole auth.Role
	Caller       auth.AllowedCaller
}

type Service struct {
	Reader OperationReader
}

func (service Service) InspectOperation(request Request) (operations.OperationRecord, error) {
	return InspectOperation(service.Reader, request)
}

func InspectOperation(reader OperationReader, request Request) (operations.OperationRecord, error) {
	if reader == nil {
		return operations.OperationRecord{}, ErrMissingOperationReader
	}

	operationID := strings.TrimSpace(request.OperationID)
	if operationID == "" {
		return operations.OperationRecord{}, ErrMissingOperationID
	}

	record, err := reader.ReadOperation(operationID)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if !canInspect(request, record) {
		return operations.OperationRecord{}, ErrInspectionDenied
	}

	return record.Sanitized(), nil
}

func canInspect(request Request, record operations.OperationRecord) bool {
	if hasGlobalInspectionCapability(request.Caller) {
		return true
	}
	if !hasNamespacedInspectionCapability(request.Caller, request.RequiredRole) {
		return false
	}

	if strings.TrimSpace(record.NamespaceID) == "" {
		return false
	}
	if !routeCanCarryNamespaceInspection(request.RouteClass) {
		return false
	}

	return !auth.NamespaceBoundMismatch(request.NamespaceID, record.NamespaceID)
}

func hasNamespacedInspectionCapability(caller auth.AllowedCaller, requiredRole auth.Role) bool {
	if requiredRole != auth.RoleOperationInspector {
		return false
	}

	return !auth.CallerNotAllowed(caller.CallerService, requiredRole, []auth.AllowedCaller{caller})
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
