package inspection

import (
	"context"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operationinspect"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

var (
	ErrInspectionDenied                         = operationinspect.ErrInspectionDenied
	ErrMissingOperationID                       = operationinspect.ErrMissingOperationID
	ErrMissingOperationReader                   = operationinspect.ErrMissingOperationReader
	ErrStoredNamespaceAuthorizationUnavailable  = operationinspect.ErrStoredNamespaceAuthorizationUnavailable
	ErrInvalidStoredNamespaceAuthorizationState = operationinspect.ErrInvalidStoredNamespaceAuthorizationState
)

type OperationReader = operationinspect.OperationReader
type StoredNamespaceAuthorizer = operationinspect.StoredNamespaceAuthorizer
type StoredNamespaceAuthorizerWithError = operationinspect.StoredNamespaceAuthorizerWithError
type Request = operationinspect.Request

type Service struct {
	Reader                    OperationReader
	StoredNamespaceAuthorizer StoredNamespaceAuthorizer
}

func (service Service) InspectOperation(ctx context.Context, request Request) (operations.OperationRecord, error) {
	return InspectOperation(ctx, service.Reader, service.StoredNamespaceAuthorizer, request)
}

func InspectOperation(ctx context.Context, reader OperationReader, authorizer StoredNamespaceAuthorizer, request Request) (operations.OperationRecord, error) {
	return operationinspect.InspectOperation(ctx, reader, authorizer, request)
}
