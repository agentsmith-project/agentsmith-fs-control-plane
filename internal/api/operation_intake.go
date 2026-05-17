package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

type OperationIDGenerator func() string

type OperationIntakeStore interface {
	CreateOrReuseOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

type RestoreReconciliationWriteGate interface {
	RestoreReconciliationWriteBlocked(ctx context.Context, namespaceID, repoID string) (bool, error)
}

type RestoreOperationIntakeStore interface {
	CreateOrReuseRestoreOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

type OperationIdempotencyLookupStore interface {
	GetOperationByIdempotencyScope(ctx context.Context, scope operations.IdempotencyScope) (operations.OperationRecord, error)
}

type OperationIntakeConfig struct {
	Store                     OperationIntakeStore
	RestoreReconciliationGate RestoreReconciliationWriteGate
}

type OperationIntakeRequest struct {
	RequestContext      auth.RequestContext
	Route               RouteMetadata
	NamespaceID         string
	RepoID              string
	TemplateID          string
	ExportID            string
	MountBindingID      string
	SessionFenceID      string
	Resource            operations.ResourceRef
	CanonicalRequest    any
	InputSummary        map[string]any
	ExternalResourceIDs map[string]string
	Phase               string
	GenerateOperationID OperationIDGenerator
	Now                 func() time.Time
}

type OperationIntakeError struct {
	Code      ErrorCode
	Status    int
	Retryable bool
	Message   string
	Details   map[string]any
}

func (err *OperationIntakeError) Error() string {
	if err == nil {
		return ""
	}
	return string(err.Code)
}

func CreateOrReuseOperationIntake(ctx context.Context, config OperationIntakeConfig, request OperationIntakeRequest) (OperationEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if idempotencyStoreNil(config.Store) {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	spec, err := operationIntakeSpec(request)
	if err != nil {
		return OperationEnvelope{}, err
	}
	if err := checkRestoreReconciliationIntakeGate(ctx, config.Store, config.RestoreReconciliationGate, request); err != nil {
		return OperationEnvelope{}, err
	}
	resolution, err := config.Store.CreateOrReuseOperation(ctx, spec)
	if err != nil {
		return OperationEnvelope{}, mapOperationIntakeError(err)
	}
	return operationEnvelopeFromRecord(resolution.Operation), nil
}

func CreateOrReuseRestoreOperationIntake(ctx context.Context, store RestoreOperationIntakeStore, request OperationIntakeRequest) (OperationEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if isNilOperationIntakeValue(store) {
		return OperationEnvelope{}, internalOperationIntakeError()
	}
	spec, err := operationIntakeSpec(request)
	if err != nil {
		return OperationEnvelope{}, err
	}
	if err := checkRestoreReconciliationIntakeGate(ctx, store, nil, request); err != nil {
		return OperationEnvelope{}, err
	}
	resolution, err := store.CreateOrReuseRestoreOperation(ctx, spec)
	if err != nil {
		return OperationEnvelope{}, mapOperationIntakeError(err)
	}
	return operationEnvelopeFromRecord(resolution.Operation), nil
}

func checkRestoreReconciliationIntakeGate(ctx context.Context, store any, gate RestoreReconciliationWriteGate, request OperationIntakeRequest) error {
	if !restoreReconciliationDangerousRoute(request.Route.OperationID) {
		return nil
	}
	if gate == nil {
		if typed, ok := store.(RestoreReconciliationWriteGate); ok {
			gate = typed
		}
	}
	if gate == nil {
		return nil
	}
	blocked, err := gate.RestoreReconciliationWriteBlocked(ctx, request.NamespaceID, request.RepoID)
	if err != nil {
		return &OperationIntakeError{Code: CodeStorageUnavailable, Status: http.StatusServiceUnavailable, Retryable: true, Message: "durable metadata store is unavailable"}
	}
	if blocked {
		return restoreReconciliationActiveIntakeError()
	}
	return nil
}

func restoreReconciliationDangerousRoute(operationID string) bool {
	switch operationID {
	case "createRepo",
		"archiveRepo",
		"restoreArchivedRepo",
		"deleteRepo",
		"restoreTombstonedRepo",
		"purgeRepo",
		"createSavePoint",
		"restore",
		"createRepoTemplate",
		"cloneRepoTemplate":
		return true
	default:
		return false
	}
}

func restoreReconciliationActiveIntakeError() *OperationIntakeError {
	return &OperationIntakeError{
		Code:      CodeRestoreReconciliationActive,
		Status:    http.StatusConflict,
		Retryable: true,
		Message:   "restore reconciliation is active",
	}
}

func operationIntakeSpec(request OperationIntakeRequest) (operations.QueuedOperationSpec, error) {
	operationType, ok := operations.OperationTypeForRouteOperationID(request.Route.OperationID)
	if !ok {
		return operations.QueuedOperationSpec{}, internalOperationIntakeError()
	}
	canonicalRoute, ok := RouteMetadataByOperationID(request.Route.OperationID)
	if !ok {
		return operations.QueuedOperationSpec{}, internalOperationIntakeError()
	}
	if isNilOperationIntakeValue(request.CanonicalRequest) {
		return operations.QueuedOperationSpec{}, internalOperationIntakeError()
	}
	if canonicalRoute.Class == auth.RouteClassNamespaceBound {
		requestNamespace := strings.TrimSpace(request.NamespaceID)
		contextNamespace := strings.TrimSpace(request.RequestContext.NamespaceID)
		if requestNamespace == "" || contextNamespace == "" || requestNamespace != contextNamespace {
			return operations.QueuedOperationSpec{}, internalOperationIntakeError()
		}
	}
	if request.GenerateOperationID == nil {
		return operations.QueuedOperationSpec{}, internalOperationIntakeError()
	}
	operationID := strings.TrimSpace(request.GenerateOperationID())
	if operationID == "" {
		return operations.QueuedOperationSpec{}, internalOperationIntakeError()
	}
	now := time.Now().UTC()
	if request.Now != nil {
		now = request.Now()
	}
	if now.IsZero() {
		return operations.QueuedOperationSpec{}, internalOperationIntakeError()
	}

	requestHash, err := operations.HashRequest(request.CanonicalRequest)
	if err != nil {
		return operations.QueuedOperationSpec{}, internalOperationIntakeError()
	}

	spec := operations.QueuedOperationSpec{
		OperationID: operationID,
		Scope: operations.NewIdempotencyScope(
			request.RequestContext.CallerService,
			request.NamespaceID,
			operationType,
			request.RequestContext.IdempotencyKey,
		),
		RequestHash:         requestHash,
		Phase:               request.Phase,
		CorrelationID:       request.RequestContext.CorrelationID,
		CallerService:       request.RequestContext.CallerService,
		AuthorizedActor:     operations.Actor{Type: request.RequestContext.Actor.Type, ID: request.RequestContext.Actor.ID},
		Resource:            request.Resource,
		NamespaceID:         request.NamespaceID,
		RepoID:              request.RepoID,
		TemplateID:          request.TemplateID,
		ExportID:            request.ExportID,
		MountBindingID:      request.MountBindingID,
		SessionFenceID:      request.SessionFenceID,
		ExternalResourceIDs: cloneStringMap(request.ExternalResourceIDs),
		InputSummary:        cloneAnyMap(request.InputSummary),
		CreatedAt:           now,
	}
	return spec, nil
}

func idempotencyStoreNil(store OperationIntakeStore) bool {
	if store == nil {
		return true
	}
	return isNilOperationIntakeValue(store)
}

func isNilOperationIntakeValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func operationEnvelopeFromRecord(record operations.OperationRecord) OperationEnvelope {
	record = record.Sanitized()
	if !validDownstreamOperationProjection(record) {
		return invalidDownstreamOperationProjectionEnvelope(record)
	}
	return NewOperationEnvelope(OperationEnvelopeSpec{
		OperationID:    record.ID,
		OperationState: OperationState(record.State),
		Resource:       ResourceRef{Type: record.Resource.Type, ID: record.Resource.ID},
		Error:          standardErrorFromOperationError(record.Error),
	})
}

func validDownstreamOperationProjection(record operations.OperationRecord) bool {
	if strings.TrimSpace(record.ID) == "" {
		return false
	}
	switch record.State {
	case operations.OperationStateQueued,
		operations.OperationStateRunning,
		operations.OperationStateSucceeded,
		operations.OperationStateFailed,
		operations.OperationStateCancelRequested,
		operations.OperationStateCancelled,
		operations.OperationStateOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

func invalidDownstreamOperationProjectionEnvelope(record operations.OperationRecord) OperationEnvelope {
	correlationID := strings.TrimSpace(record.CorrelationID)
	if correlationID == "" {
		correlationID = "missing"
	}
	return NewOperationEnvelope(OperationEnvelopeSpec{
		OperationID:    strings.TrimSpace(record.ID),
		OperationState: OperationStateFailed,
		Resource:       ResourceRef{Type: record.Resource.Type, ID: record.Resource.ID},
		Error: &StandardError{
			Code:          CodeInternalError,
			Message:       "invalid operation projection",
			Retryable:     false,
			CorrelationID: correlationID,
			Details:       map[string]any{"projection_invalid": true},
		},
	})
}

func standardErrorFromOperationError(operationError *operations.OperationError) *StandardError {
	if operationError == nil {
		return nil
	}
	var operationID *string
	if strings.TrimSpace(operationError.OperationID) != "" {
		id := operationError.OperationID
		operationID = &id
	}
	code := ErrorCode(operationError.Code)
	return &StandardError{
		Code:          code,
		Message:       operationError.Message,
		Retryable:     operationError.Retryable,
		CorrelationID: operationError.CorrelationID,
		OperationID:   operationID,
		Details:       cloneAnyMap(operationError.Details),
	}
}

func mapOperationIntakeError(err error) error {
	switch {
	case errors.Is(err, operations.ErrIdempotencyConflict):
		return &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"}
	case errors.Is(err, operations.ErrRepoAlreadyExists):
		return &OperationIntakeError{Code: CodeRepoAlreadyExists, Status: http.StatusConflict, Retryable: false, Message: "target repo already exists"}
	case errors.Is(err, operations.ErrRepoJVSMutationInProgress):
		code, message, retryable, details := fileLibraryBlockingOperationError(false)
		return &OperationIntakeError{Code: code, Status: http.StatusConflict, Retryable: retryable, Message: message, Details: details}
	case errors.Is(err, operations.ErrMissingOperationBoundary):
		return internalOperationIntakeError()
	default:
		return &OperationIntakeError{Code: CodeStorageUnavailable, Status: http.StatusServiceUnavailable, Retryable: true, Message: "durable metadata store is unavailable"}
	}
}

func internalOperationIntakeError() *OperationIntakeError {
	return &OperationIntakeError{Code: CodeInternalError, Status: http.StatusInternalServerError, Retryable: false, Message: "internal server error"}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
