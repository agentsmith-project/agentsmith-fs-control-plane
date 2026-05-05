package store

import (
	"context"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

// OperationReader is the read side of the durable operation record boundary.
type OperationReader interface {
	GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error)
}

// OperationWriter is the write side of the durable operation record boundary.
// It accepts only SanitizedOperationRecord to keep future column-wise writes
// from bypassing operation redaction.
type OperationWriter interface {
	CreateOperation(ctx context.Context, record operations.SanitizedOperationRecord) error
	UpdateOperation(ctx context.Context, record operations.SanitizedOperationRecord) error
}

// OperationStore is the complete operation record boundary for callers that need both read and write access.
type OperationStore interface {
	OperationReader
	OperationWriter
}

// IdempotencyStore owns the durable create-or-reuse boundary for queued operations.
//
// Implementations must make CreateOrReuseOperation atomic by enforcing
// spec.Scope.ConstraintKey() as a durable uniqueness constraint in the same
// boundary as operation creation. Reusing an existing operation is valid only
// when the stored request hash matches spec.RequestHash; a different hash for
// the same constraint key must return an error wrapping operations.ErrIdempotencyConflict.
type IdempotencyStore interface {
	CreateOrReuseOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

// AuditSink accepts audit events for append-only or outbox-backed delivery.
type AuditSink interface {
	AppendAuditEvent(ctx context.Context, event audit.Event) error
}
