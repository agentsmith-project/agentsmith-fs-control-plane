package store

import (
	"context"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
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

// OperationRecoveryReader is the read-only durable operation recovery candidate boundary.
// It must not claim, recover, finalize, or mutate operation state.
type OperationRecoveryReader interface {
	ListOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
}

// OperationLeaseStore owns atomic worker/recovery lease transitions by operation_id.
//
// Implementations must not implement these methods as GetOperation followed by
// UpdateOperation. Claim/reclaim/recover/finalize, renew, and worker-owned
// progress/terminal writes must be single conditional durable mutations that
// return the updated redacted operation record only when the operation was
// still eligible at the database boundary.
type OperationLeaseStore interface {
	AcquireOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	RenewOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	UpdateOperationWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error)
}

// OperationWorkerCommitStore atomically commits a lease-fenced operation update and
// its audit outbox event. The audit event OperationID must match the operation
// being updated. Implementations must commit both the operation update and audit
// outbox append in the same durable boundary, never leaving one without the other.
type OperationWorkerCommitStore interface {
	CommitOperationWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
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

// AuditOutboxDeliveryStore owns DB-only, at-least-once audit outbox state transitions.
//
// The current audit_outbox schema has no delivery_owner column. ClaimDueAuditOutboxRecords
// therefore validates owner for caller discipline but does not persist it and does not provide
// owner fencing; callers must treat claimed records as at-least-once work.
type AuditOutboxDeliveryStore interface {
	ListDueAuditOutboxRecords(ctx context.Context, now time.Time, limit int) ([]audit.OutboxRecord, error)
	ClaimDueAuditOutboxRecords(ctx context.Context, owner string, now time.Time, limit int) ([]audit.OutboxRecord, error)
	MarkAuditOutboxDelivered(ctx context.Context, eventID string, now time.Time) error
	MarkAuditOutboxDeliveryFailed(ctx context.Context, eventID string, failure audit.DeliveryFailure) error
}

// RepoFenceReader is the read side of the durable repo fence boundary.
type RepoFenceReader interface {
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
}

// RepoFenceWriter is the write side of the durable repo fence boundary.
type RepoFenceWriter interface {
	CreateRepoFence(ctx context.Context, fence fences.Fence) error
	ReleaseRepoFence(ctx context.Context, repoID, fenceID string) error
}

// RepoFenceStore is the complete durable repo fence boundary for callers that need read and write access.
type RepoFenceStore interface {
	RepoFenceReader
	RepoFenceWriter
}

// RepoRecoveryInspectionReader is the read-only durable metadata boundary for
// composing repo recovery inspections. It must not claim, recover, release, or
// mutate repo/fence/operation state.
type RepoRecoveryInspectionReader interface {
	GetRepo(ctx context.Context, repoID string) (resources.Repo, error)
	ListReposForRecoveryInspection(ctx context.Context) ([]resources.Repo, error)
	ListAllHeldRepoFences(ctx context.Context) ([]fences.Fence, error)
}

type VolumeStore interface {
	UpsertVolume(ctx context.Context, volume resources.Volume) error
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListActiveVolumes(ctx context.Context) ([]resources.Volume, error)
}

type NamespaceStore interface {
	UpsertNamespace(ctx context.Context, namespace resources.Namespace) error
	DisableNamespace(ctx context.Context, namespaceID, reason string) (resources.Namespace, error)
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
}

type NamespaceVolumeBindingStore interface {
	PutNamespaceVolumeBinding(ctx context.Context, binding resources.NamespaceVolumeBinding) error
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
}

// RepoReader is the read side of the durable repo metadata boundary.
type RepoReader interface {
	GetRepo(ctx context.Context, repoID string) (resources.Repo, error)
	ListReposByNamespace(ctx context.Context, namespaceID string) ([]resources.Repo, error)
}

// RepoWriter is the write side of the durable repo metadata boundary.
type RepoWriter interface {
	CreateRepo(ctx context.Context, repo resources.Repo) error
	UpdateRepoLifecycle(ctx context.Context, repoID string, lifecycle resources.RepoLifecycle) (resources.Repo, error)
}

// RepoStore is the complete durable repo metadata boundary for callers that need both read and write access.
type RepoStore interface {
	RepoReader
	RepoWriter
}
