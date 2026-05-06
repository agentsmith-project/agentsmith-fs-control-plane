package repoexec

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

type AuditEventIDGenerator func() string

type JVSRunner interface {
	Init(ctx context.Context, payloadRoot, controlRoot string) (jvsrunner.InitSummary, error)
	DoctorStrict(ctx context.Context, controlRoot string) (jvsrunner.DoctorSummary, error)
	Save(ctx context.Context, controlRoot, message string) (jvsrunner.SaveSummary, error)
	History(ctx context.Context, controlRoot string) (jvsrunner.HistorySummary, error)
}

type Config struct {
	Store        repoCreateStore
	JVSRunner    JVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type repoCreateStore interface {
	store.RepoCreateOperationCommitStore
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
	CreateRepoFence(ctx context.Context, fence fences.Fence) error
}

type Executor struct {
	store        repoCreateStore
	jvs          JVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

func NewExecutor(config Config) (*Executor, error) {
	if config.Store == nil {
		return nil, errors.New("repo create recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("repo create jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("repo create recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("repo create recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("repo create audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("repo create volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("repo create volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &Executor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *Executor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationRepoCreate {
		return recovery.OperationSupport{Reason: "unsupported_repo_create_operation"}
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseRepoCreateValidate {
		return recovery.OperationSupport{Reason: "unsupported_repo_create_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_repo_create_recovery_action"}
	}
}

func (executor *Executor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported repo create operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported repo create operation recovery: %s", support.Reason)
	}
	if err := validateLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	if err := validateInputSummary(record); err != nil {
		return err
	}
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	if now.IsZero() {
		return errors.New("repo create recovery time must be set")
	}

	held, err := executor.store.ListHeldRepoFences(ctx, record.RepoID)
	if err != nil {
		return errors.New("repo create fence read failed")
	}
	existingHeldFence, hasSameOpHeldFence := sameOperationHeldFence(record, held)
	if hasSameOpHeldFence && existingHeldFence.Status != fences.StatusActive {
		return executor.commitIntervention(ctx, record, now, "REPO_CREATE_FENCE_RECOVERY_REQUIRED", "repo create fence requires operator intervention", existingHeldFence.ID, map[string]any{"fence_status": string(existingHeldFence.Status)})
	}

	metadata, roots, err := executor.loadMetadata(ctx, record, now)
	if err != nil {
		if hasSameOpHeldFence {
			return executor.commitIntervention(ctx, record, now, "REPO_CREATE_VALIDATION_FAILED_WITH_FENCE", "repo create validation failed with held fence", existingHeldFence.ID, nil)
		}
		return executor.commitFailed(ctx, record, now, "REPO_CREATE_VALIDATION_FAILED", "repo create validation failed", "")
	}

	existingFence, hasSameOpFence := sameOperationActiveFence(record, held)
	var fenceID string
	if hasSameOpFence {
		fenceID = existingFence.ID
	} else {
		decision := fences.CanAcquire(fences.AcquisitionRequest{RepoID: record.RepoID, Kind: fences.KindLifecycle, HolderOperationID: record.ID}, held)
		if !decision.Allowed {
			return executor.commitFailed(ctx, record, now, "REPO_CREATE_FENCE_HELD", "repo create fence held", "")
		}
		fence := fences.Fence{ID: "fence_" + record.ID, RepoID: record.RepoID, Kind: fences.KindLifecycle, HolderOperationID: record.ID, Status: fences.StatusActive, ExpiresAt: leaseOrDefault(record, now), CreatedAt: now, UpdatedAt: now}
		if err := executor.store.CreateRepoFence(ctx, fence); err != nil {
			return errors.New("repo create fence acquisition failed")
		}
		fenceID = fence.ID
	}

	adoptionAllowed := hasSameOpFence && (plan.Action == recovery.RecoveryActionRetry || plan.Action == recovery.RecoveryActionReclaim)
	var jvsRepoID string
	adopted := false
	if adoptionAllowed {
		doctor, err := executor.jvs.DoctorStrict(ctx, roots.ControlRootPath)
		if err != nil {
			return executor.commitIntervention(ctx, record, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", fenceID, nil)
		}
		jvsRepoID = doctor.RepoID
		adopted = true
	} else {
		initSummary, err := executor.jvs.Init(ctx, roots.PayloadRootPath, roots.ControlRootPath)
		if err != nil {
			_, _ = executor.jvs.DoctorStrict(ctx, roots.ControlRootPath)
			return executor.commitIntervention(ctx, record, now, "JVS_COMMAND_FAILED", "jvs init failed", fenceID, nil)
		}
		doctor, err := executor.jvs.DoctorStrict(ctx, roots.ControlRootPath)
		if err != nil {
			return executor.commitIntervention(ctx, record, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", fenceID, map[string]any{"repo_id": initSummary.RepoID, "workspace": initSummary.Workspace})
		}
		if initSummary.RepoID != doctor.RepoID {
			return executor.commitIntervention(ctx, record, now, "JVS_REPO_ID_MISMATCH", "jvs repo identity mismatch", fenceID, map[string]any{"init_repo_id": initSummary.RepoID, "doctor_repo_id": doctor.RepoID})
		}
		jvsRepoID = initSummary.RepoID
	}

	repo := resources.Repo{
		ID:                  record.RepoID,
		NamespaceID:         record.NamespaceID,
		VolumeID:            metadata.binding.DefaultVolumeID,
		JVSRepoID:           jvsRepoID,
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: roots.ControlVolumeSubdir,
		PayloadVolumeSubdir: roots.PayloadVolumeSubdir,
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive, LastLifecycleOperationID: record.ID},
		CreatedAt:           createdAt(record, now),
		UpdatedAt:           now,
	}
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseRepoCreateCommitted
	operation.ExternalResourceIDs = map[string]string{"jvs_repo_id": jvsRepoID}
	operation.JVSJSONOutput = map[string]any{"repo_id": jvsRepoID, "workspace": "main"}
	operation.VerificationResult = map[string]any{"repo_id": jvsRepoID, "workspace": "main", "healthy": true, "adopted": adopted, "volume_id": repo.VolumeID, "control_volume_subdir": repo.ControlVolumeSubdir, "payload_volume_subdir": repo.PayloadVolumeSubdir}
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now, audit.OutcomeSucceeded, "repo_create_committed", map[string]any{"repo_id": record.RepoID, "volume_id": repo.VolumeID, "jvs_repo_id": jvsRepoID, "adopted": adopted})
	if err != nil {
		return err
	}
	if _, _, err := executor.store.CommitRepoCreateSucceededWithLease(ctx, repo, operation.SanitizedForPersistence(), executor.owner, now, event, fenceID); err != nil {
		return errors.New("repo create success commit failed")
	}
	return nil
}

type repoMetadata struct {
	namespace resources.Namespace
	binding   resources.NamespaceVolumeBinding
	volume    resources.Volume
}

func (executor *Executor) loadMetadata(ctx context.Context, record operations.OperationRecord, now time.Time) (repoMetadata, pathresolver.RepoRootPaths, error) {
	namespace, err := executor.store.GetNamespace(ctx, record.NamespaceID)
	if err != nil || namespace.Status != resources.NamespaceStatusActive {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, errors.New("invalid namespace")
	}
	binding, err := executor.store.GetNamespaceVolumeBinding(ctx, record.NamespaceID)
	if err != nil || binding.Status != resources.NamespaceStatusActive {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, errors.New("invalid namespace volume binding")
	}
	volume, err := executor.store.GetVolume(ctx, binding.DefaultVolumeID)
	if err != nil || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, errors.New("invalid volume")
	}
	root, ok := executor.volumeRoots[volume.ID]
	if !ok {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, errors.New("missing volume root")
	}
	roots, err := pathresolver.ResolveRepoRootPaths(root, record.NamespaceID, record.RepoID)
	if err != nil {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, err
	}
	return repoMetadata{namespace: namespace, binding: binding, volume: volume}, roots, nil
}

func (executor *Executor) commitFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message, releaseFenceID string) error {
	operation := failedOperation(record, now, operations.OperationStateFailed, code, message)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "repo_create_failed", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRepoCreateFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event, releaseFenceID); err != nil {
		return errors.New("repo create failure commit failed")
	}
	return nil
}

func (executor *Executor) commitIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message, fenceID string, details map[string]any) error {
	operation := failedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = details
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "repo_create_operator_intervention_required", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRepoCreateFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event, ""); err != nil {
		return errors.New("repo create intervention commit failed")
	}
	return nil
}

func failedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, code, message string) operations.OperationRecord {
	operation := record
	operation.State = state
	operation.Phase = operations.OperationPhaseRepoCreateValidate
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID}}
	operation.FinishedAt = &now
	return operation
}

func (executor *Executor) auditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("repo create audit event id must be set")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: audit.EventTypeRepoCreate, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func validateLeasedRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid repo create recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return errors.New("invalid repo create recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid repo create recovery record")
	}
	return nil
}

func validateInputSummary(record operations.OperationRecord) error {
	namespaceID, _ := record.InputSummary["namespace_id"].(string)
	repoID, _ := record.InputSummary["target_repo_id"].(string)
	if namespaceID != record.NamespaceID || repoID != record.RepoID {
		return errors.New("invalid repo create input summary")
	}
	return nil
}

func sameOperationHeldFence(record operations.OperationRecord, held []fences.Fence) (fences.Fence, bool) {
	for _, fence := range held {
		if fence.RepoID == record.RepoID && fence.Kind == fences.KindLifecycle && fence.HolderOperationID == record.ID && fence.Held() {
			return fence, true
		}
	}
	return fences.Fence{}, false
}

func sameOperationActiveFence(record operations.OperationRecord, held []fences.Fence) (fences.Fence, bool) {
	fence, ok := sameOperationHeldFence(record, held)
	if !ok || fence.Status != fences.StatusActive {
		return fences.Fence{}, false
	}
	return fence, true
}

func leaseOrDefault(record operations.OperationRecord, now time.Time) time.Time {
	if record.LeaseExpiresAt != nil && record.LeaseExpiresAt.After(now) {
		return *record.LeaseExpiresAt
	}
	return now.Add(time.Hour)
}

func createdAt(record operations.OperationRecord, now time.Time) time.Time {
	if record.CreatedAt.IsZero() {
		return now
	}
	return record.CreatedAt
}

func validateVolumeRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return errors.New("invalid volume root")
	}
	return nil
}

var _ recovery.OperationExecutor = (*Executor)(nil)
