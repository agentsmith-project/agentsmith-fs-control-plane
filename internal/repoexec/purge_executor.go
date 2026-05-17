package repoexec

import (
	"context"
	"errors"
	"fmt"
	"os"
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

type PurgeConfig struct {
	Store         repoPurgeStore
	JVSRunner     JVSRunner
	StoragePurger StoragePurger
	Owner         string
	Now           time.Time
	Clock         func() time.Time
	AuditEventID  AuditEventIDGenerator
	VolumeRoots   map[string]string
}

type repoPurgeStore interface {
	store.RepoPurgeOperationCommitStore
	store.RepoLifecycleOperationMetadataReader
	ListEarlierNonTerminalRepoLifecycleOperations(ctx context.Context, repoID, operationID string, createdAt time.Time) ([]operations.OperationRecord, error)
}

type PurgeExecutor struct {
	store        repoPurgeStore
	jvs          JVSRunner
	purger       StoragePurger
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

type RepoStorageState string

const (
	RepoStoragePresent       RepoStorageState = "present"
	RepoStorageAbsent        RepoStorageState = "absent"
	RepoStoragePartialAbsent RepoStorageState = "partial_absent"
)

type RepoStoragePaths struct {
	VolumeRootPath    string
	ContainerRootPath string
	ControlRootPath   string
	PayloadRootPath   string
}

type StoragePurger interface {
	InspectRepoStorage(ctx context.Context, paths RepoStoragePaths) (RepoStorageState, error)
	PurgeRepoStorage(ctx context.Context, paths RepoStoragePaths) error
}

func NewPurgeExecutor(config PurgeConfig) (*PurgeExecutor, error) {
	if config.Store == nil {
		return nil, errors.New("repo purge recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("repo purge jvs runner is required")
	}
	if config.StoragePurger == nil {
		return nil, errors.New("repo purge storage purger is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("repo purge recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("repo purge recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("repo purge audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("repo purge volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("repo purge volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &PurgeExecutor{store: config.Store, jvs: config.JVSRunner, purger: config.StoragePurger, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *PurgeExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationRepoPurge {
		return recovery.OperationSupport{Reason: "unsupported_repo_purge_operation"}
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseRepoLifecycleValidate {
		return recovery.OperationSupport{Reason: "unsupported_repo_purge_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_repo_purge_recovery_action"}
	}
}

func (executor *PurgeExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported repo purge operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported repo purge operation recovery: %s", support.Reason)
	}
	if err := validateRepoLifecycleLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	if now.IsZero() {
		return errors.New("repo purge recovery time must be set")
	}

	held, err := executor.store.ListHeldRepoFences(ctx, record.RepoID)
	if err != nil {
		return errors.New("repo purge fence read failed")
	}
	existingHeld, hasSameHeld := sameOperationHeldFence(record, held)
	if hasSameHeld && existingHeld.Status != fences.StatusActive {
		return executor.commitPurgeIntervention(ctx, record, now, "REPO_LIFECYCLE_FENCE_RECOVERY_REQUIRED", "repo purge fence requires operator intervention", map[string]any{"fence_status": string(existingHeld.Status)})
	}

	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil || repo.Status != resources.RepoStatusTombstoned {
		return executor.commitPurgeIntervention(ctx, record, now, "REPO_LIFECYCLE_INVALID_STATE", "repo purge source status invalid", nil)
	}
	if err := executor.validatePurgePolicy(record, repo); err != nil {
		return executor.commitPurgeIntervention(ctx, record, now, "PURGE_RETENTION_NOT_MET", "repo purge policy validation failed", nil)
	}
	if !record.CreatedAt.After(repo.UpdatedAt) {
		return executor.commitPurgeIntervention(ctx, record, now, "OPERATION_RECOVERY_REQUIRED", "repo purge operation is not in current tombstone cycle", nil)
	}
	blocking, err := executor.store.ListEarlierNonTerminalRepoLifecycleOperations(ctx, record.RepoID, record.ID, record.CreatedAt)
	if err != nil || len(blocking) > 0 {
		return executor.commitPurgeIntervention(ctx, record, now, "OPERATION_RECOVERY_REQUIRED", "earlier repo lifecycle operation blocks purge", nil)
	}

	var fenceID string
	if hasSameHeld {
		fenceID = existingHeld.ID
	} else {
		decision := fences.CanAcquire(fences.AcquisitionRequest{RepoID: record.RepoID, Kind: fences.KindLifecycle, HolderOperationID: record.ID}, held)
		if !decision.Allowed {
			if decision.Error != nil && decision.Error.Family == fences.ErrorFamilyOperationRecoveryRequired {
				return executor.commitPurgeIntervention(ctx, record, now, "OPERATION_RECOVERY_REQUIRED", "repo purge fence requires recovery", nil)
			}
			return executor.commitPurgeIntervention(ctx, record, now, "REPO_LIFECYCLE_FENCE_HELD", "repo purge fence held", nil)
		}
		fence := fences.Fence{ID: "fence_" + record.ID, RepoID: record.RepoID, Kind: fences.KindLifecycle, HolderOperationID: record.ID, Status: fences.StatusActive, ExpiresAt: leaseOrDefault(record, now), CreatedAt: now, UpdatedAt: now}
		if err := executor.store.CreateRepoFence(ctx, fence); err != nil {
			return errors.New("repo purge fence acquisition failed")
		}
		fenceID = fence.ID
	}

	decision, err := executor.lifecycleDrainDecision(ctx, record, now)
	if err != nil {
		return executor.commitPurgeIntervention(ctx, record, now, "REPO_LIFECYCLE_SESSION_READ_FAILED", "repo purge session validation failed", nil)
	}
	if !decision.Allowed {
		if decision.ErrorFamily == sessionstate.ErrorFamilyActiveSessionsBlockLifecycle {
			return nil
		}
		return executor.commitPurgeIntervention(ctx, record, now, decision.ErrorFamily.String(), "repo purge session drain requires operator intervention", map[string]any{"blocking_kind": decision.BlockingKind})
	}

	paths, err := executor.storagePaths(repo)
	if err != nil {
		return executor.commitPurgeIntervention(ctx, record, now, "REPO_LIFECYCLE_VALIDATION_FAILED", "repo purge storage validation failed", nil)
	}
	state, err := executor.purger.InspectRepoStorage(ctx, paths)
	if err != nil {
		return executor.commitPurgeIntervention(ctx, record, now, "REPO_PURGE_STORAGE_UNCERTAIN", "repo purge storage inspection failed", nil)
	}
	switch state {
	case RepoStorageAbsent:
		// Idempotent retry after a previous physical purge.
	case RepoStoragePartialAbsent:
		if err := executor.purger.PurgeRepoStorage(ctx, paths); err != nil {
			return executor.commitPurgeIntervention(ctx, record, now, "REPO_PURGE_STORAGE_FAILED", "repo purge storage delete failed", nil)
		}
		after, err := executor.purger.InspectRepoStorage(ctx, paths)
		if err != nil || after != RepoStorageAbsent {
			return executor.commitPurgeIntervention(ctx, record, now, "REPO_PURGE_STORAGE_UNCERTAIN", "repo purge storage delete not confirmed", nil)
		}
	case RepoStoragePresent:
		if err := executor.validateActiveMetadata(ctx, repo); err != nil {
			return executor.commitPurgeIntervention(ctx, record, now, "REPO_LIFECYCLE_VALIDATION_FAILED", "repo purge validation failed", nil)
		}
		controlExists := strings.TrimSpace(repo.ControlVolumeSubdir) != ""
		if controlExists {
			doctor, err := executor.jvs.DirectDoctor(ctx, jvsrunner.DirectTarget{ControlRoot: paths.ControlRootPath, Home: paths.PayloadRootPath})
			if err != nil || !directDoctorAllowsMutation(doctor) || doctor.RepoID != repo.JVSRepoID {
				return executor.commitPurgeIntervention(ctx, record, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", withJVSErrorDetails(map[string]any{"repo_id": repo.JVSRepoID}, err))
			}
		}
		if err := executor.purger.PurgeRepoStorage(ctx, paths); err != nil {
			return executor.commitPurgeIntervention(ctx, record, now, "REPO_PURGE_STORAGE_FAILED", "repo purge storage delete failed", nil)
		}
		after, err := executor.purger.InspectRepoStorage(ctx, paths)
		if err != nil || after != RepoStorageAbsent {
			return executor.commitPurgeIntervention(ctx, record, now, "REPO_PURGE_STORAGE_UNCERTAIN", "repo purge storage delete not confirmed", nil)
		}
	default:
		return executor.commitPurgeIntervention(ctx, record, now, "REPO_PURGE_STORAGE_UNCERTAIN", "repo purge storage state is uncertain", nil)
	}

	target := repo
	target.Status = resources.RepoStatusPurged
	target.Lifecycle.Status = resources.RepoStatusPurged
	target.Lifecycle.RetentionExpiresAt = nil
	target.Lifecycle.LastLifecycleOperationID = record.ID
	target.UpdatedAt = now
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseRepoLifecycleCommitted
	operation.VerificationResult = map[string]any{"repo_id": record.RepoID, "lifecycle_status": string(resources.RepoStatusPurged)}
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.lifecycleAuditEvent(operation, now, audit.OutcomeSucceeded, "repo_purge_committed", purgeAuditDetails(record, map[string]any{"repo_id": record.RepoID, "lifecycle_status": string(resources.RepoStatusPurged)}))
	if err != nil {
		return err
	}
	if _, _, err := executor.store.CommitRepoPurgeSucceededWithLease(ctx, target, operation.SanitizedForPersistence(), executor.owner, now, event, fenceID); err != nil {
		return errors.New("repo purge success commit failed")
	}
	return nil
}

func (executor *PurgeExecutor) validateActiveMetadata(ctx context.Context, repo resources.Repo) error {
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

func (executor *PurgeExecutor) lifecycleDrainDecision(ctx context.Context, record operations.OperationRecord, now time.Time) (sessionstate.Decision, error) {
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

func (executor *PurgeExecutor) validatePurgePolicy(record operations.OperationRecord, repo resources.Repo) error {
	if repo.Lifecycle.RetentionExpiresAt == nil || repo.Lifecycle.PreDeleteStatus == "" {
		return errors.New("invalid tombstone metadata")
	}
	snapshot, ok := record.InputSummary["lifecycle_policy_snapshot"].(map[string]any)
	if !ok || record.InputSummary["product_confirmation_present"] != true {
		return errors.New("invalid purge snapshot")
	}
	if !record.CreatedAt.Before(*repo.Lifecycle.RetentionExpiresAt) {
		return nil
	}
	if snapshot["retention_override_requested"] == true && snapshot["operator_approval_present"] == true && snapshot["break_glass_enabled"] == true && snapshot["break_glass_authorized"] == true {
		return nil
	}
	return errors.New("purge retention not met")
}

func (executor *PurgeExecutor) storagePaths(repo resources.Repo) (RepoStoragePaths, error) {
	root, ok := executor.volumeRoots[repo.VolumeID]
	if !ok {
		return RepoStoragePaths{}, errors.New("missing volume root")
	}
	resolved, err := pathresolver.ResolveRepoRootPaths(root, repo.NamespaceID, repo.ID)
	if err != nil {
		return RepoStoragePaths{}, err
	}
	if repo.ControlVolumeSubdir != resolved.ControlVolumeSubdir || repo.PayloadVolumeSubdir != resolved.PayloadVolumeSubdir {
		return RepoStoragePaths{}, errors.New("repo storage identity mismatch")
	}
	container := filepath.Join(root, filepath.FromSlash(resolved.ContainerVolumeSubdir))
	if err := validatePurgeRoot(root, container, resolved.ControlRootPath, resolved.PayloadRootPath); err != nil {
		return RepoStoragePaths{}, err
	}
	return RepoStoragePaths{VolumeRootPath: root, ContainerRootPath: container, ControlRootPath: resolved.ControlRootPath, PayloadRootPath: resolved.PayloadRootPath}, nil
}

func validatePurgeRoot(volumeRoot, container, control, payload string) error {
	for _, path := range []string{volumeRoot, container, control, payload} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
			return errors.New("invalid purge path")
		}
	}
	if !strings.HasPrefix(container, volumeRoot+string(filepath.Separator)) || !strings.HasPrefix(control, container+string(filepath.Separator)) || !strings.HasPrefix(payload, container+string(filepath.Separator)) {
		return errors.New("invalid purge path")
	}
	if control == payload || strings.HasPrefix(control, payload+string(filepath.Separator)) || strings.HasPrefix(payload, control+string(filepath.Separator)) {
		return errors.New("invalid purge path")
	}
	return nil
}

func (executor *PurgeExecutor) commitPurgeIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := repoLifecycleFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = details
	attachJVSErrorDetails(&operation, details)
	event, err := executor.lifecycleAuditEvent(operation, now, audit.OutcomeFailed, string(record.Type)+"_operator_intervention_required", purgeAuditDetails(record, map[string]any{"repo_id": record.RepoID}))
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRepoPurgeFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event, ""); err != nil {
		return errors.New("repo purge intervention commit failed")
	}
	return fmt.Errorf("%w: repo purge operator intervention required", recovery.ErrOperationManualIntervention)
}

func (executor *PurgeExecutor) lifecycleAuditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("repo purge audit event id must be set")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: audit.EventTypeRepoPurge, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func purgeAuditDetails(record operations.OperationRecord, base map[string]any) map[string]any {
	details := asStringAnyMap(base)
	for _, key := range []string{"product_confirmation_ref_fingerprint", "operator_approval_ref_fingerprint"} {
		value, ok := record.InputSummary[key].(string)
		if ok && validPurgeRefFingerprint(value) {
			details[key] = value
		}
	}
	return details
}

func validPurgeRefFingerprint(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

type FilesystemStoragePurger struct{}

func (FilesystemStoragePurger) InspectRepoStorage(_ context.Context, paths RepoStoragePaths) (RepoStorageState, error) {
	if err := validateNoSymlinkAncestors(paths.VolumeRootPath, paths.ContainerRootPath); err != nil {
		return "", errors.New("repo storage inspection failed")
	}
	exists := 0
	missing := 0
	for _, path := range []string{paths.ContainerRootPath, paths.ControlRootPath, paths.PayloadRootPath} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			missing++
			continue
		}
		if err != nil {
			return "", errors.New("repo storage inspection failed")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("repo storage inspection failed")
		}
		exists++
	}
	if exists == 0 {
		return RepoStorageAbsent, nil
	}
	if missing > 0 {
		return RepoStoragePartialAbsent, nil
	}
	return RepoStoragePresent, nil
}

func (FilesystemStoragePurger) PurgeRepoStorage(_ context.Context, paths RepoStoragePaths) error {
	if err := validatePurgeRoot(paths.VolumeRootPath, paths.ContainerRootPath, paths.ControlRootPath, paths.PayloadRootPath); err != nil {
		return errors.New("repo storage purge failed")
	}
	if err := validateNoSymlinkAncestors(paths.VolumeRootPath, paths.ContainerRootPath); err != nil {
		return errors.New("repo storage purge failed")
	}
	info, err := os.Lstat(paths.ContainerRootPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("repo storage purge failed")
	}
	if err := os.RemoveAll(paths.ContainerRootPath); err != nil {
		return errors.New("repo storage purge failed")
	}
	return nil
}

func validateNoSymlinkAncestors(volumeRoot, target string) error {
	info, err := os.Lstat(volumeRoot)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid purge path")
	}
	rel, err := filepath.Rel(volumeRoot, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return errors.New("invalid purge path")
	}
	current := volumeRoot
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid purge path")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid purge path")
		}
	}
	return nil
}

var _ recovery.OperationExecutor = (*PurgeExecutor)(nil)
