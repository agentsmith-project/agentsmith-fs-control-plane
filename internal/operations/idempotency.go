package operations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrIdempotencyConflict       = errors.New("idempotency conflict")
	ErrRepoAlreadyExists         = errors.New("repo already exists")
	ErrMissingOperationBoundary  = errors.New("missing durable operation boundary")
	ErrRepoJVSMutationInProgress = errors.New("repo jvs mutation in progress")
)

type IdempotencyScope struct {
	CallerService string `json:"caller_service"`
	// NamespaceID may be empty for volume-global/operator operations. The empty
	// value is still an explicit namespace_id component in ConstraintKey/String.
	NamespaceID    string        `json:"namespace_id"`
	OperationType  OperationType `json:"operation_type"`
	IdempotencyKey string        `json:"idempotency_key"`
}

func NewIdempotencyScope(callerService, namespaceID string, operationType OperationType, idempotencyKey string) IdempotencyScope {
	return IdempotencyScope{
		CallerService:  callerService,
		NamespaceID:    namespaceID,
		OperationType:  operationType,
		IdempotencyKey: idempotencyKey,
	}
}

func (scope IdempotencyScope) String() string {
	return fmt.Sprintf("%s:%s:%s:%s", scope.CallerService, scope.NamespaceID, scope.OperationType, scope.IdempotencyKey)
}

func (scope IdempotencyScope) Equal(other IdempotencyScope) bool {
	return scope.CallerService == other.CallerService &&
		scope.NamespaceID == other.NamespaceID &&
		scope.OperationType == other.OperationType &&
		scope.IdempotencyKey == other.IdempotencyKey
}

type IdempotencyConstraintKey struct {
	CallerService  string
	NamespaceID    string
	OperationType  OperationType
	IdempotencyKey string
}

// ConstraintKey is the pure key a durable store must enforce atomically.
func (scope IdempotencyScope) ConstraintKey() IdempotencyConstraintKey {
	return IdempotencyConstraintKey{
		CallerService:  scope.CallerService,
		NamespaceID:    scope.NamespaceID,
		OperationType:  scope.OperationType,
		IdempotencyKey: scope.IdempotencyKey,
	}
}

func (key IdempotencyConstraintKey) Columns() []string {
	return []string{"caller_service", "namespace_id", "operation_type", "idempotency_key"}
}

type RequestHash string

func HashRequest(request any) (RequestHash, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(encoded)
	return RequestHash("sha256:" + hex.EncodeToString(sum[:])), nil
}

func ValidateSavePointID(id string) error {
	if !safeOpaqueID(id) {
		return fmt.Errorf("invalid save_point_id %q", id)
	}
	return nil
}

func safeOpaqueID(id string) bool {
	if len(id) == 0 || len(id) > 128 || strings.TrimSpace(id) != id {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if i == 0 {
			if !asciiAlphaNum(b) {
				return false
			}
			continue
		}
		if !asciiAlphaNum(b) && b != '_' && b != '-' && b != '.' && b != ':' {
			return false
		}
	}
	return true
}

func asciiAlphaNum(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

type IdempotencyResolution struct {
	Operation OperationRecord
	Existing  bool
	Reused    bool
}

// CompareIdempotency is comparison-only: it can classify an already-read set
// of records, but it does not create or reserve a durable operation boundary.
// Stores must enforce IdempotencyConstraintKey atomically when creating queued
// operations.
func CompareIdempotency(existing []OperationRecord, scope IdempotencyScope, requestHash RequestHash) (IdempotencyResolution, error) {
	for _, operation := range existing {
		if operation.IdempotencyScope != scope.String() {
			continue
		}

		if operation.RequestHash != requestHash {
			return IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", ErrIdempotencyConflict, scope.String())
		}

		return IdempotencyResolution{
			Operation: operation.Sanitized(),
			Existing:  true,
			Reused:    true,
		}, nil
	}

	return IdempotencyResolution{}, nil
}

// ResolveIdempotency is comparison-only and must not be used as a
// list-then-create flow. Prefer CompareIdempotency for in-memory comparison
// helpers and store.IdempotencyStore.CreateOrReuseOperation for durable writes.
func ResolveIdempotency(existing []OperationRecord, scope IdempotencyScope, requestHash RequestHash) (IdempotencyResolution, error) {
	return CompareIdempotency(existing, scope, requestHash)
}

type QueuedOperationSpec struct {
	OperationID         string
	Scope               IdempotencyScope
	RequestHash         RequestHash
	Phase               string
	CorrelationID       string
	CallerService       string
	AuthorizedActor     Actor
	Resource            ResourceRef
	NamespaceID         string
	RepoID              string
	TemplateID          string
	ExportID            string
	MountBindingID      string
	SessionFenceID      string
	ExternalResourceIDs map[string]string
	InputSummary        map[string]any
	CreatedAt           time.Time
}

func NewQueuedOperationRecord(spec QueuedOperationSpec) (OperationRecord, error) {
	if spec.OperationID == "" {
		return OperationRecord{}, fmt.Errorf("%w: operation_id", ErrMissingOperationBoundary)
	}
	if spec.Scope.CallerService == "" || spec.Scope.OperationType == "" || spec.Scope.IdempotencyKey == "" {
		return OperationRecord{}, fmt.Errorf("%w: idempotency scope", ErrMissingOperationBoundary)
	}
	if spec.RequestHash == "" {
		return OperationRecord{}, fmt.Errorf("%w: request_hash", ErrMissingOperationBoundary)
	}
	if spec.Phase == "" {
		return OperationRecord{}, fmt.Errorf("%w: phase", ErrMissingOperationBoundary)
	}
	if spec.CorrelationID == "" {
		return OperationRecord{}, fmt.Errorf("%w: correlation_id", ErrMissingOperationBoundary)
	}
	if spec.CallerService == "" {
		return OperationRecord{}, fmt.Errorf("%w: caller_service", ErrMissingOperationBoundary)
	}
	if spec.AuthorizedActor.Type == "" || spec.AuthorizedActor.ID == "" {
		return OperationRecord{}, fmt.Errorf("%w: authorized_actor", ErrMissingOperationBoundary)
	}
	if spec.Resource.Type == "" || spec.Resource.ID == "" {
		return OperationRecord{}, fmt.Errorf("%w: resource", ErrMissingOperationBoundary)
	}
	if spec.CreatedAt.IsZero() {
		return OperationRecord{}, fmt.Errorf("%w: created_at", ErrMissingOperationBoundary)
	}

	return OperationRecord{
		ID:                  spec.OperationID,
		Type:                spec.Scope.OperationType,
		State:               OperationStateQueued,
		Phase:               spec.Phase,
		Attempt:             0,
		IdempotencyScope:    spec.Scope.String(),
		IdempotencyKey:      spec.Scope.IdempotencyKey,
		RequestHash:         spec.RequestHash,
		CorrelationID:       spec.CorrelationID,
		CallerService:       spec.CallerService,
		AuthorizedActor:     spec.AuthorizedActor,
		Resource:            spec.Resource,
		NamespaceID:         spec.NamespaceID,
		RepoID:              spec.RepoID,
		TemplateID:          spec.TemplateID,
		ExportID:            spec.ExportID,
		MountBindingID:      spec.MountBindingID,
		SessionFenceID:      spec.SessionFenceID,
		ExternalResourceIDs: cloneStringMap(spec.ExternalResourceIDs),
		InputSummary:        cloneAnyMap(spec.InputSummary),
		Error:               nil,
		CreatedAt:           spec.CreatedAt,
	}, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
