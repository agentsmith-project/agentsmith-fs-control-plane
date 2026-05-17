package repoexec

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

type LifecycleConfig struct {
	Store        repoLifecycleStore
	JVSRunner    JVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type repoLifecycleStore interface {
	store.RepoLifecycleOperationCommitStore
	store.RepoLifecycleOperationMetadataReader
}

type LifecycleExecutor struct {
	store        repoLifecycleStore
	jvs          JVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

func NewLifecycleExecutor(config LifecycleConfig) (*LifecycleExecutor, error) {
	if config.Store == nil {
		return nil, errors.New("repo lifecycle recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("repo lifecycle jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("repo lifecycle recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("repo lifecycle recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("repo lifecycle audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("repo lifecycle volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("repo lifecycle volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &LifecycleExecutor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *LifecycleExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || !repoLifecycleSupportedType(record.Type) {
		return recovery.OperationSupport{Reason: "unsupported_repo_lifecycle_operation"}
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseRepoLifecycleValidate {
		return recovery.OperationSupport{Reason: "unsupported_repo_lifecycle_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_repo_lifecycle_recovery_action"}
	}
}

func (executor *LifecycleExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported repo lifecycle operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported repo lifecycle operation recovery: %s", support.Reason)
	}
	if err := validateRepoLifecycleLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	if now.IsZero() {
		return errors.New("repo lifecycle recovery time must be set")
	}

	held, err := executor.store.ListHeldRepoFences(ctx, record.RepoID)
	if err != nil {
		return errors.New("repo lifecycle fence read failed")
	}
	existingHeld, hasSameHeld := sameOperationHeldFence(record, held)
	if hasSameHeld && existingHeld.Status != fences.StatusActive {
		return executor.commitLifecycleIntervention(ctx, record, now, "REPO_LIFECYCLE_FENCE_RECOVERY_REQUIRED", "repo lifecycle fence requires operator intervention", existingHeld.ID, map[string]any{"fence_status": string(existingHeld.Status)})
	}
	releaseOnTerminalFailure := ""
	if hasSameHeld {
		releaseOnTerminalFailure = existingHeld.ID
	}

	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return executor.commitLifecycleFailed(ctx, record, now, "REPO_LIFECYCLE_VALIDATION_FAILED", "repo lifecycle validation failed", releaseOnTerminalFailure)
	}
	if !repoLifecycleSourceMatches(record.Type, repo.Status) {
		return executor.commitLifecycleFailed(ctx, record, now, "REPO_LIFECYCLE_INVALID_STATE", "repo lifecycle source status invalid", releaseOnTerminalFailure)
	}
	if err := executor.validateMetadata(ctx, repo); err != nil {
		return executor.commitLifecycleFailed(ctx, record, now, "REPO_LIFECYCLE_VALIDATION_FAILED", "repo lifecycle validation failed", releaseOnTerminalFailure)
	}

	var fenceID string
	if hasSameHeld {
		fenceID = existingHeld.ID
	} else {
		decision := fences.CanAcquire(fences.AcquisitionRequest{RepoID: record.RepoID, Kind: fences.KindLifecycle, HolderOperationID: record.ID}, held)
		if !decision.Allowed {
			if decision.Error != nil && decision.Error.Family == fences.ErrorFamilyOperationRecoveryRequired {
				return executor.commitLifecycleIntervention(ctx, record, now, "OPERATION_RECOVERY_REQUIRED", "repo lifecycle fence requires recovery", "", nil)
			}
			return executor.commitLifecycleFailed(ctx, record, now, "REPO_LIFECYCLE_FENCE_HELD", "repo lifecycle fence held", "")
		}
		fence := fences.Fence{ID: "fence_" + record.ID, RepoID: record.RepoID, Kind: fences.KindLifecycle, HolderOperationID: record.ID, Status: fences.StatusActive, ExpiresAt: leaseOrDefault(record, now), CreatedAt: now, UpdatedAt: now}
		if err := executor.store.CreateRepoFence(ctx, fence); err != nil {
			return errors.New("repo lifecycle fence acquisition failed")
		}
		fenceID = fence.ID
	}

	targetRepo, err := repoLifecycleTarget(repo, record, now)
	if err != nil {
		return executor.commitLifecycleIntervention(ctx, record, now, "REPO_LIFECYCLE_VALIDATION_FAILED", "repo lifecycle validation failed", releaseOnTerminalFailure, nil)
	}

	if repoLifecycleRequiresSessionDrain(record.Type) {
		decision, err := executor.lifecycleDrainDecision(ctx, record, now)
		if err != nil {
			return executor.commitLifecycleIntervention(ctx, record, now, "REPO_LIFECYCLE_SESSION_READ_FAILED", "repo lifecycle session validation failed", fenceID, nil)
		}
		if !decision.Allowed {
			if repoLifecycleWaitsForActiveSessions(record.Type) && decision.ErrorFamily == sessionstate.ErrorFamilyActiveSessionsBlockLifecycle {
				return nil
			}
			return executor.commitLifecycleIntervention(ctx, record, now, decision.ErrorFamily.String(), "repo lifecycle session drain requires operator intervention", fenceID, map[string]any{"blocking_kind": decision.BlockingKind})
		}
	}

	if repoLifecycleRequiresDoctor(record.Type) {
		target, err := executor.directTarget(repo)
		if err != nil {
			return executor.commitLifecycleIntervention(ctx, record, now, "REPO_LIFECYCLE_VALIDATION_FAILED", "repo lifecycle validation failed", fenceID, nil)
		}
		doctor, err := executor.jvs.DirectDoctor(ctx, target)
		if err != nil || !directDoctorAllowsMutation(doctor) || doctor.RepoID != repo.JVSRepoID {
			return executor.commitLifecycleIntervention(ctx, record, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", fenceID, withJVSErrorDetails(map[string]any{"repo_id": repo.JVSRepoID}, err))
		}
	}

	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseRepoLifecycleCommitted
	operation.VerificationResult = map[string]any{"repo_id": record.RepoID, "lifecycle_status": string(targetRepo.Status)}
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.lifecycleAuditEvent(operation, now, audit.OutcomeSucceeded, string(record.Type)+"_committed", map[string]any{"repo_id": record.RepoID, "lifecycle_status": string(targetRepo.Status)})
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, _, err := executor.store.CommitRepoLifecycleSucceededWithLease(commitCtx, targetRepo, operation.SanitizedForPersistence(), executor.owner, now, event, fenceID); err != nil {
		return repoLifecycleCommitError("repo lifecycle success commit failed", err)
	}
	return nil
}

func (executor *LifecycleExecutor) validateMetadata(ctx context.Context, repo resources.Repo) error {
	namespace, err := executor.store.GetNamespace(ctx, repo.NamespaceID)
	if err != nil || namespace.Status != resources.NamespaceStatusActive {
		return errors.New("invalid namespace")
	}
	binding, err := executor.store.GetNamespaceVolumeBinding(ctx, repo.NamespaceID)
	if err != nil || binding.Status != resources.NamespaceStatusActive {
		return errors.New("invalid namespace binding")
	}
	volume, err := executor.store.GetVolume(ctx, repo.VolumeID)
	if err != nil || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return errors.New("invalid volume")
	}
	return nil
}

func (executor *LifecycleExecutor) lifecycleDrainDecision(ctx context.Context, record operations.OperationRecord, now time.Time) (sessionstate.Decision, error) {
	exports, err := executor.store.ListExportSessionsByRepo(ctx, record.RepoID)
	if err != nil {
		return sessionstate.Decision{}, err
	}
	mounts, err := executor.store.ListWorkloadMountBindingsByRepo(ctx, record.RepoID)
	if err != nil {
		return sessionstate.Decision{}, err
	}
	return sessionstate.LifecycleDrainGate(sessionstate.GateRequest{NamespaceID: record.NamespaceID, RepoID: record.RepoID, Now: now, ExportSessions: exports, Mounts: mounts}), nil
}

func (executor *LifecycleExecutor) controlRoot(repo resources.Repo) (string, error) {
	root, ok := executor.volumeRoots[repo.VolumeID]
	if !ok {
		return "", errors.New("missing volume root")
	}
	cleanSubdir := filepath.Clean(repo.ControlVolumeSubdir)
	if cleanSubdir == "." || filepath.IsAbs(cleanSubdir) || strings.HasPrefix(cleanSubdir, ".."+string(filepath.Separator)) || cleanSubdir == ".." {
		return "", errors.New("invalid control subdir")
	}
	controlRoot := filepath.Join(root, cleanSubdir)
	if !strings.HasPrefix(controlRoot, root+string(filepath.Separator)) {
		return "", errors.New("invalid control root")
	}
	return controlRoot, nil
}

func (executor *LifecycleExecutor) directTarget(repo resources.Repo) (jvsrunner.DirectTarget, error) {
	root, ok := executor.volumeRoots[repo.VolumeID]
	if !ok {
		return jvsrunner.DirectTarget{}, errors.New("missing volume root")
	}
	roots, err := pathresolver.ResolveRepoRootPaths(root, repo.NamespaceID, repo.ID)
	if err != nil || roots.ControlVolumeSubdir != repo.ControlVolumeSubdir || roots.PayloadVolumeSubdir != repo.PayloadVolumeSubdir {
		return jvsrunner.DirectTarget{}, errors.New("invalid repo roots")
	}
	return jvsrunner.DirectTarget{ControlRoot: roots.ControlRootPath, Home: roots.PayloadRootPath}, nil
}

func (executor *LifecycleExecutor) commitLifecycleFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message, releaseFenceID string) error {
	operation := repoLifecycleFailedOperation(record, now, operations.OperationStateFailed, code, message)
	event, err := executor.lifecycleAuditEvent(operation, now, audit.OutcomeFailed, string(record.Type)+"_failed", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, err := executor.store.CommitRepoLifecycleFailedWithLease(commitCtx, operation.SanitizedForPersistence(), executor.owner, now, event, releaseFenceID); err != nil {
		return repoLifecycleCommitError("repo lifecycle failure commit failed", err)
	}
	return nil
}

func (executor *LifecycleExecutor) commitLifecycleIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message, fenceID string, details map[string]any) error {
	operation := repoLifecycleFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = details
	attachJVSErrorDetails(&operation, details)
	event, err := executor.lifecycleAuditEvent(operation, now, audit.OutcomeFailed, string(record.Type)+"_operator_intervention_required", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, err := executor.store.CommitRepoLifecycleFailedWithLease(commitCtx, operation.SanitizedForPersistence(), executor.owner, now, event, ""); err != nil {
		return repoLifecycleCommitError("repo lifecycle intervention commit failed", err)
	}
	return fmt.Errorf("%w: repo lifecycle operator intervention required", recovery.ErrOperationManualIntervention)
}

func repoLifecycleCommitError(message string, cause error) error {
	return commitError{message: message, cause: cause}
}

func repoLifecycleFailedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, code, message string) operations.OperationRecord {
	operation := record
	operation.State = state
	operation.Phase = operations.OperationPhaseRepoLifecycleValidate
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID}}
	operation.FinishedAt = &now
	return operation
}

func (executor *LifecycleExecutor) lifecycleAuditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("repo lifecycle audit event id must be set")
	}
	eventType, ok := audit.EventTypeForOperationType(string(operation.Type))
	if !ok {
		return audit.Event{}, errors.New("repo lifecycle audit type is unsupported")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: eventType, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func validateRepoLifecycleLeasedRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid repo lifecycle recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return errors.New("invalid repo lifecycle recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid repo lifecycle recovery record")
	}
	return nil
}

func repoLifecycleSupportedType(typ operations.OperationType) bool {
	switch typ {
	case operations.OperationRepoArchive, operations.OperationRepoRestoreArchived, operations.OperationRepoDelete, operations.OperationRepoRestoreTombstoned:
		return true
	default:
		return false
	}
}

func repoLifecycleSourceMatches(typ operations.OperationType, status resources.RepoStatus) bool {
	switch typ {
	case operations.OperationRepoArchive:
		return status == resources.RepoStatusActive
	case operations.OperationRepoRestoreArchived:
		return status == resources.RepoStatusArchived
	case operations.OperationRepoDelete:
		return status == resources.RepoStatusActive || status == resources.RepoStatusArchived
	case operations.OperationRepoRestoreTombstoned:
		return status == resources.RepoStatusTombstoned
	default:
		return false
	}
}

func repoLifecycleRequiresSessionDrain(typ operations.OperationType) bool {
	switch typ {
	case operations.OperationRepoArchive, operations.OperationRepoRestoreArchived, operations.OperationRepoDelete, operations.OperationRepoRestoreTombstoned:
		return true
	default:
		return false
	}
}

func repoLifecycleWaitsForActiveSessions(typ operations.OperationType) bool {
	return typ == operations.OperationRepoArchive || typ == operations.OperationRepoDelete
}

func repoLifecycleRequiresDoctor(typ operations.OperationType) bool {
	return typ == operations.OperationRepoRestoreArchived || typ == operations.OperationRepoRestoreTombstoned
}

func repoLifecycleTarget(repo resources.Repo, record operations.OperationRecord, now time.Time) (resources.Repo, error) {
	target := repo
	target.Lifecycle.LastLifecycleOperationID = record.ID
	target.UpdatedAt = now
	switch record.Type {
	case operations.OperationRepoArchive:
		target.Status = resources.RepoStatusArchived
		target.Lifecycle.Status = resources.RepoStatusArchived
		target.Lifecycle.RetentionExpiresAt = nil
		target.Lifecycle.PreDeleteStatus = ""
	case operations.OperationRepoRestoreArchived:
		target.Status = resources.RepoStatusActive
		target.Lifecycle.Status = resources.RepoStatusActive
		target.Lifecycle.RetentionExpiresAt = nil
		target.Lifecycle.PreDeleteStatus = ""
	case operations.OperationRepoDelete:
		retentionExpiresAt, err := repoLifecycleDeleteRetentionExpiresAt(record)
		if err != nil {
			return resources.Repo{}, err
		}
		target.Status = resources.RepoStatusTombstoned
		target.Lifecycle.Status = resources.RepoStatusTombstoned
		target.Lifecycle.RetentionExpiresAt = &retentionExpiresAt
		target.Lifecycle.PreDeleteStatus = repo.Status
	case operations.OperationRepoRestoreTombstoned:
		if repo.Lifecycle.PreDeleteStatus != resources.RepoStatusActive && repo.Lifecycle.PreDeleteStatus != resources.RepoStatusArchived {
			return resources.Repo{}, errors.New("invalid tombstone pre-delete status")
		}
		if repo.Lifecycle.RetentionExpiresAt == nil || !record.CreatedAt.Before(*repo.Lifecycle.RetentionExpiresAt) {
			return resources.Repo{}, errors.New("restore operation is not eligible for tombstone retention")
		}
		if !record.CreatedAt.After(repo.UpdatedAt) {
			return resources.Repo{}, errors.New("restore operation is not in current tombstone cycle")
		}
		target.Status = repo.Lifecycle.PreDeleteStatus
		target.Lifecycle.Status = repo.Lifecycle.PreDeleteStatus
		target.Lifecycle.RetentionExpiresAt = nil
		target.Lifecycle.PreDeleteStatus = ""
	default:
		return resources.Repo{}, errors.New("unsupported repo lifecycle target")
	}
	return target, nil
}

func repoLifecycleDeleteRetentionExpiresAt(record operations.OperationRecord) (time.Time, error) {
	snapshot, ok := record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)
	if !ok {
		return time.Time{}, errors.New("missing lifecycle policy snapshot")
	}
	value, ok := snapshot["tombstone_retention_seconds"]
	if !ok {
		return time.Time{}, errors.New("missing tombstone retention")
	}
	seconds, err := repoLifecycleRetentionSeconds(value)
	if err != nil {
		return time.Time{}, err
	}
	return record.CreatedAt.Add(time.Duration(seconds) * time.Second), nil
}

func repoLifecycleRetentionSeconds(value any) (int64, error) {
	const maxDurationSeconds = int64(math.MaxInt64 / int64(time.Second))
	var seconds int64
	switch typed := value.(type) {
	case int:
		seconds = int64(typed)
	case int64:
		seconds = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 || typed > float64(maxDurationSeconds) || typed != math.Trunc(typed) {
			return 0, errors.New("invalid tombstone retention")
		}
		seconds = int64(typed)
	default:
		return 0, errors.New("invalid tombstone retention")
	}
	if seconds < 0 || seconds > maxDurationSeconds {
		return 0, errors.New("invalid tombstone retention")
	}
	return seconds, nil
}

var _ recovery.OperationExecutor = (*LifecycleExecutor)(nil)
