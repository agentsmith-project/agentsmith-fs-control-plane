package store

import (
	"context"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operatorrepair"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restorereconcile"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
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
// Long-running operation executors use RenewOperationLease as the durable
// heartbeat; callers must not replace it with process-local timers or
// in-memory ownership state.
type OperationLeaseStore interface {
	AcquireOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	RenewOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	UpdateOperationWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error)
}

// OperationWorkerCommitStore atomically commits a lease-fenced operation update and
// its audit outbox event. Generic operation recovery interventions that move an
// unsupported operation to operator_intervention_required must use this boundary,
// not OperationLeaseStore.UpdateOperationWithLease. The audit event OperationID
// must match the operation being updated. Implementations must commit both the
// operation update and audit outbox append in the same durable boundary, never
// leaving one without the other.
type OperationWorkerCommitStore interface {
	CommitOperationWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
}

// OperatorRepairStore owns allowlisted operator repair writes. Implementations
// must use a single conditional durable mutation that both updates the
// operation and appends the audit outbox event; they must not expose generic
// state rewrites or arbitrary SQL.
type OperatorRepairStore interface {
	ReadOperationForRepair(ctx context.Context, operationID string) (operations.OperationRecord, error)
	CommitOperatorRepairFailed(ctx context.Context, request operatorrepair.CommitRequest) (operations.OperationRecord, error)
}

type RestoreReconciliationStore interface {
	RestoreReconciliationWriteBlocked(ctx context.Context, namespaceID, repoID string) (bool, error)
	ActiveRun(ctx context.Context) (restorereconcile.Run, error)
	ListTargets(ctx context.Context, runID string) ([]restorereconcile.Target, error)
	ObserveTarget(ctx context.Context, target restorereconcile.Target) (restorereconcile.Observation, error)
	CompleteRestoreReconciliationRun(ctx context.Context, runID string, now time.Time) error
	CommitRestoreReconciliationMismatch(ctx context.Context, request restorereconcile.MismatchCommit) error
}

// VolumeEnsureOperationCommitStore atomically commits volume metadata, a
// lease-fenced operation update, and its audit outbox event. Implementations
// must perform all writes in the same durable boundary; they must not compose
// this by calling UpsertVolume followed by CommitOperationWithLease.
type VolumeEnsureOperationCommitStore interface {
	CommitVolumeEnsureWithLease(ctx context.Context, volume resources.Volume, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Volume, operations.OperationRecord, error)
}

// VolumeEnsureOperationRecoveryStore owns the durable recovery boundary for
// volume_ensure. Implementations must push the volume_ensure +
// validate_volume_ensure scope into durable list and acquire predicates before
// ORDER/LIMIT or lease mutation.
type VolumeEnsureOperationRecoveryStore interface {
	ListVolumeEnsureOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireVolumeEnsureOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	VolumeEnsureOperationCommitStore
}

// NamespaceUpsertOperationCommitStore atomically commits namespace metadata,
// a lease-fenced operation update, and its audit outbox event. Implementations
// must perform all three writes in the same durable boundary; they must not
// compose this by calling UpsertNamespace followed by CommitOperationWithLease.
// The operation update and stored leased operation must both describe a
// succeeded namespace_upsert for the same namespace resource, and the audit
// event must describe the same operation, caller, actor, correlation, namespace
// resource, and succeeded outcome.
type NamespaceUpsertOperationCommitStore interface {
	CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error)
}

// NamespaceUpsertOperationRecoveryStore owns the durable recovery boundary for
// the minimal namespace_upsert runner. Implementations must push the
// namespace_upsert + validate_namespace_upsert scope into durable list and
// acquire predicates before ORDER/LIMIT or lease mutation; callers must not
// compose this by using the generic operation recovery list/acquire and
// filtering after the fact.
type NamespaceUpsertOperationRecoveryStore interface {
	ListNamespaceUpsertOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireNamespaceUpsertOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	NamespaceUpsertOperationCommitStore
}

type NamespaceDisableOperationCommitStore interface {
	CommitNamespaceDisableWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error)
}

type NamespaceDisableOperationRecoveryStore interface {
	ListNamespaceDisableOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireNamespaceDisableOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	NamespaceDisableOperationCommitStore
}

// NamespaceVolumeBindingOperationCommitStore atomically commits namespace
// volume binding metadata, a lease-fenced operation update, and its audit
// outbox event. Implementations must perform all writes in the same durable
// boundary; they must not compose this by calling PutNamespaceVolumeBinding
// followed by CommitOperationWithLease. The durable boundary must verify the
// stored operation, active namespace, active default volume, binding metadata,
// lease fence, terminal operation update, and audit event all describe the same
// namespace_volume_binding_put for the same namespace resource.
type NamespaceVolumeBindingOperationCommitStore interface {
	CommitNamespaceVolumeBindingPutWithLease(ctx context.Context, binding resources.NamespaceVolumeBinding, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.NamespaceVolumeBinding, operations.OperationRecord, error)
	CommitNamespaceVolumeBindingPutFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
}

// NamespaceVolumeBindingOperationRecoveryStore owns the durable recovery
// boundary for namespace_volume_binding_put. Implementations must push the
// namespace_volume_binding_put + validate_namespace_volume_binding_put scope
// into durable list and acquire predicates before ORDER/LIMIT or lease mutation.
type NamespaceVolumeBindingOperationRecoveryStore interface {
	ListNamespaceVolumeBindingPutOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireNamespaceVolumeBindingPutOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	NamespaceVolumeBindingOperationCommitStore
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

// RestoreOperationIntakeStore owns direct restore HTTP intake. It resolves
// idempotency before rejecting active same-repo JVS mutations, then inserts only
// a durable direct restore operation.
type RestoreOperationIntakeStore interface {
	CreateOrReuseRestoreOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

// OperationIdempotencyLookupStore is the read-only side of the operation
// idempotency boundary. It exists so handlers that must validate durable
// metadata before creating a new operation can still reuse an already-created
// operation for the same scope before touching mutable resource state.
type OperationIdempotencyLookupStore interface {
	GetOperationByIdempotencyScope(ctx context.Context, scope operations.IdempotencyScope) (operations.OperationRecord, error)
}

// RepoCreateOperationIntakeStore owns the durable repo_create intake boundary.
// Implementations must first resolve idempotency for the operation scope and
// request hash, then reject only brand-new create requests that target an
// existing repo. They must not compose this as GetRepo followed by
// CreateOrReuseOperation.
type RepoCreateOperationIntakeStore interface {
	CreateOrReuseRepoCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

type TemplateOperationIntakeStore interface {
	CreateOrReuseTemplateCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
	CreateOrReuseTemplateCloneOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
}

// RepoCreateOperationCommitStore atomically commits repo metadata, a
// lease-fenced repo_create operation update, audit outbox append, and the
// target create fence release. Failure/intervention updates must also be
// lease-fenced and append audit in one durable boundary; callers choose whether
// to release a held fence only when no external JVS side effect is possible.
type RepoCreateOperationCommitStore interface {
	CommitRepoCreateSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error)
	CommitRepoCreateFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error)
}

type RepoCreateOperationMetadataReader interface {
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
	CreateRepoFence(ctx context.Context, fence fences.Fence) error
}

// RepoCreateOperationRecoveryStore owns the durable recovery and metadata
// boundary for repo_create. Implementations must push repo_create +
// validate_repo_create scope into list/acquire SQL predicates, and success or
// failure/intervention commits must not compose generic operation commits with
// separate repo/fence writes.
type RepoCreateOperationRecoveryStore interface {
	ListRepoCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireRepoCreateOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	RepoCreateOperationCommitStore
	RepoCreateOperationMetadataReader
}

type TemplateOperationCommitStore interface {
	MarkTemplateCreateWriterFencedWithLease(ctx context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error)
	CommitTemplateCreateSucceededWithLease(ctx context.Context, template resources.Repo, sourceRepoID, sourceSavePointID, cloneHistoryMode string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error)
	CommitTemplateCreateFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
	CommitTemplateCloneSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error)
	CommitTemplateCloneFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
}

type TemplateOperationMetadataReader interface {
	GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error)
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
	ListExportSessionsByRepo(ctx context.Context, repoID string) ([]sessionstate.ExportSession, error)
	ListWorkloadMountBindingsByRepo(ctx context.Context, repoID string) ([]sessionstate.WorkloadMountBinding, error)
}

type TemplateOperationRecoveryStore interface {
	ListTemplateCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireTemplateCreateOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	ListTemplateCloneOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireTemplateCloneOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	TemplateOperationCommitStore
	TemplateOperationMetadataReader
}

type RepoLifecycleOperationCommitStore interface {
	CommitRepoLifecycleSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error)
	CommitRepoLifecycleFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error)
}

type RepoLifecycleOperationMetadataReader interface {
	GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error)
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
	CreateRepoFence(ctx context.Context, fence fences.Fence) error
	ListExportSessionsByRepo(ctx context.Context, repoID string) ([]sessionstate.ExportSession, error)
	ListWorkloadMountBindingsByRepo(ctx context.Context, repoID string) ([]sessionstate.WorkloadMountBinding, error)
}

// RepoLifecycleOperationRecoveryStore owns repo_archive,
// repo_restore_archived, repo_delete, and repo_restore_tombstoned recovery.
// Implementations must scope list/acquire at the durable boundary to those
// operation types and validate_repo_lifecycle, explicitly excluding repo_purge,
// and terminal writes must atomically update repo lifecycle metadata,
// operation state, audit outbox, and lifecycle fence release when applicable.
type RepoLifecycleOperationRecoveryStore interface {
	ListRepoLifecycleOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireRepoLifecycleOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	RepoLifecycleOperationCommitStore
	RepoLifecycleOperationMetadataReader
}

type RepoPurgeOperationCommitStore interface {
	CommitRepoPurgeSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error)
	CommitRepoPurgeFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error)
}

// RepoPurgeOperationRecoveryStore owns destructive repo_purge recovery. It is
// intentionally separate from the metadata lifecycle recovery store. Durable
// list/acquire predicates must scope to repo_purge + validate_repo_lifecycle
// only, must not finalize cancel_requested purge operations automatically, and
// terminal writes must atomically update purged repo lifecycle metadata,
// operation state, audit outbox, and lifecycle fence release when applicable.
type RepoPurgeOperationRecoveryStore interface {
	ListRepoPurgeOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireRepoPurgeOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	ListEarlierNonTerminalRepoLifecycleOperations(ctx context.Context, repoID, operationID string, createdAt time.Time) ([]operations.OperationRecord, error)
	RepoPurgeOperationCommitStore
	RepoLifecycleOperationMetadataReader
}

type SavePointCreateOperationCommitStore interface {
	CommitSavePointCreateSucceededWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
	CommitSavePointCreateFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
	MarkSavePointCreateWriterDrainPendingWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error)
}

type SavePointCreateOperationMetadataReader interface {
	GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error)
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
	RepoSessionStateReader
}

type SavePointCreateRecoveryCapabilityReader interface {
	SavePointCreateRecoveryCapabilityReady(ctx context.Context, now time.Time) (bool, error)
}

type SavePointCreateRecoveryCapabilityRecorder interface {
	RecordSavePointCreateRecoveryCapability(ctx context.Context, owner string, observedAt, expiresAt time.Time) error
}

type RestoreOperationCommitStore interface {
	MarkRestoreWriterFencedWithLease(ctx context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error)
	CommitRestoreSucceededWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
	CommitRestoreFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error)
}

type RestoreOperationMetadataReader interface {
	GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error)
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
	RepoSessionStateReader
}

type RestoreOperationRecoveryStore interface {
	ListRestoreOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireRestoreOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	RestoreOperationCommitStore
	RestoreOperationMetadataReader
}

// RepoJVSMutationGateReader is the read-only durable gate for JVS history
// readers. It observes operation-row non-terminal JVS mutations only. It
// must not claim, fence, lease, or mutate operation state.
type RepoJVSMutationGateReader interface {
	RepoHasNonTerminalJVSMutation(ctx context.Context, repoID string) (bool, error)
}

type RepoJVSMutationGateStatusReader interface {
	GetRepoJVSMutationGateStatus(ctx context.Context, repoID string) (operations.RepoJVSMutationGateStatus, error)
}

// SavePointCreateOperationRecoveryStore owns save_point_create recovery. It
// serializes same-repo JVS mutations at acquire time using earlier
// non-terminal operation records rather than repo_fences. The worker executes a
// single direct save; uncertain retries enter operator intervention instead of
// issuing list/adoption fallback commands.
type SavePointCreateOperationRecoveryStore interface {
	ListSavePointCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireSavePointCreateOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	SavePointCreateOperationCommitStore
	SavePointCreateOperationMetadataReader
	SavePointCreateRecoveryCapabilityRecorder
}

// AuditSink accepts audit events for append-only or outbox-backed delivery.
type AuditSink interface {
	AppendAuditEvent(ctx context.Context, event audit.Event) error
}

// AuditOutboxDeliveryStore owns DB-only, at-least-once audit outbox state transitions.
//
// The current audit_outbox schema has no delivery_owner column. ClaimDueAuditOutboxRecords
// and RecoverStaleAuditOutboxRecords therefore validate owner for caller discipline but do
// not persist it and do not provide owner fencing; callers must treat claimed/recovered
// records as at-least-once work.
type AuditOutboxDeliveryStore interface {
	ListDueAuditOutboxRecords(ctx context.Context, now time.Time, limit int) ([]audit.OutboxRecord, error)
	ClaimDueAuditOutboxRecords(ctx context.Context, owner string, now time.Time, limit int) ([]audit.OutboxRecord, error)
	RecoverStaleAuditOutboxRecords(ctx context.Context, owner string, staleThreshold time.Duration, limit int, failure audit.DeliveryFailure) ([]audit.OutboxRecord, error)
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

// RepoSessionStateReader is the read-only durable session substrate boundary
// for direct restore writer gates and lifecycle drain gates. It returns only safe
// admission fields and must not expose credentials, raw paths, mount plans, or
// gateway/orchestrator secrets.
type RepoSessionStateReader interface {
	ListExportSessionsByRepo(ctx context.Context, repoID string) ([]sessionstate.ExportSession, error)
	ListWorkloadMountBindingsByRepo(ctx context.Context, repoID string) ([]sessionstate.WorkloadMountBinding, error)
}

// ExportStore is the synchronous control-plane boundary for WebDAV export
// create/get/revoke. It stores only redacted sessions plus verifier material;
// create callers receive the one-time secret from the API layer, not from this
// store interface.
type ExportStore interface {
	CreateOrReuseExport(ctx context.Context, request exportaccess.CreateRequest) (exportaccess.CreateResult, error)
	GetExportSession(ctx context.Context, exportID string) (exportaccess.Session, error)
	RevokeExport(ctx context.Context, request exportaccess.RevokeRequest) (exportaccess.RevokeResult, error)
}

// ExportAccessStore extends the control-plane export boundary with gateway and
// reconcile helpers. The API runtime needs only ExportStore; gateway servers may
// use these helpers later without changing the control-plane contract.
type ExportAccessStore interface {
	ExportStore
	GetExportGatewayCredential(ctx context.Context, exportID string) (exportaccess.GatewayCredential, error)
	RecordExportAccess(ctx context.Context, exportID string, accessedAt time.Time) error
	BeginExportRuntimeRequest(ctx context.Context, request exportaccess.RuntimeRequestBegin) (exportaccess.Session, error)
	HeartbeatExportRuntimeRequest(ctx context.Context, request exportaccess.RuntimeRequestHeartbeat) (exportaccess.Session, error)
	EndExportRuntimeRequest(ctx context.Context, request exportaccess.RuntimeRequestEnd) (exportaccess.Session, error)
	RecoverStaleExportRuntimeRequests(ctx context.Context, request exportaccess.StaleRuntimeRequestRecovery) (exportaccess.StaleRuntimeRequestRecoveryResult, error)
	ListExportSessionsForTerminalReconcile(ctx context.Context, now time.Time, limit int) ([]exportaccess.Session, error)
	ReconcileExportSessionTerminal(ctx context.Context, request exportaccess.ReconcileRequest) (exportaccess.ReconcileResult, error)
}

type ExportSessionReconcileStore interface {
	RecoverStaleExportRuntimeRequests(ctx context.Context, request exportaccess.StaleRuntimeRequestRecovery) (exportaccess.StaleRuntimeRequestRecoveryResult, error)
	ListExportSessionsForTerminalReconcile(ctx context.Context, now time.Time, limit int) ([]exportaccess.Session, error)
	ReconcileExportSessionTerminal(ctx context.Context, request exportaccess.ReconcileRequest) (exportaccess.ReconcileResult, error)
}

type WorkloadMountBindingReader interface {
	GetWorkloadMountBinding(ctx context.Context, mountBindingID string) (workloadmount.Binding, error)
}

type WorkloadMountStaleLeaseReader interface {
	ListStaleNonTerminalWorkloadMountBindings(ctx context.Context, now time.Time, limit int) ([]workloadmount.Binding, error)
}

type WorkloadMountPlanReader interface {
	GetOrchestratorMountPlan(ctx context.Context, namespaceID, mountBindingID string) (workloadmount.Plan, error)
}

type WorkloadMountBindingOperationCommitStore interface {
	CommitWorkloadMountBindingCreateWithLease(ctx context.Context, binding workloadmount.Binding, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error)
	CommitWorkloadMountBindingStatusWithLease(ctx context.Context, mountBindingID string, status sessionstate.MountStatus, reason string, observedAt time.Time, leaseExpiresAt *time.Time, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error)
	CommitWorkloadMountBindingHeartbeatWithLease(ctx context.Context, mountBindingID string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error)
	CommitWorkloadMountBindingReleaseWithLease(ctx context.Context, mountBindingID string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error)
	CommitWorkloadMountBindingRevokeWithLease(ctx context.Context, mountBindingID string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error)
}

type WorkloadMountBindingOperationRecoveryStore interface {
	ListWorkloadMountBindingOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error)
	AcquireWorkloadMountBindingOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	WorkloadMountBindingOperationCommitStore
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
	GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error)
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
