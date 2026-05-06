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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

type TemplateJVSRunner interface {
	Save(ctx context.Context, controlRoot, message string) (jvsrunner.SaveSummary, error)
	RepoClone(ctx context.Context, sourceControlRoot, targetPayloadRoot, targetControlRoot string) (jvsrunner.RepoCloneSummary, error)
	DoctorStrict(ctx context.Context, controlRoot string) (jvsrunner.DoctorSummary, error)
}

type TemplateConfig struct {
	Store        templateStore
	JVSRunner    TemplateJVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type templateStore interface {
	store.TemplateOperationCommitStore
	store.TemplateOperationMetadataReader
}

type TemplateCreateExecutor struct {
	store        templateStore
	jvs          TemplateJVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

type TemplateCloneExecutor TemplateCreateExecutor

func NewTemplateCreateExecutor(config TemplateConfig) (*TemplateCreateExecutor, error) {
	executor, err := newTemplateBaseExecutor(config, "template create")
	if err != nil {
		return nil, err
	}
	return (*TemplateCreateExecutor)(executor), nil
}

func NewTemplateCloneExecutor(config TemplateConfig) (*TemplateCloneExecutor, error) {
	executor, err := newTemplateBaseExecutor(config, "template clone")
	if err != nil {
		return nil, err
	}
	return (*TemplateCloneExecutor)(executor), nil
}

func newTemplateBaseExecutor(config TemplateConfig, label string) (*TemplateCreateExecutor, error) {
	if config.Store == nil {
		return nil, errors.New(label + " recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New(label + " jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New(label + " recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New(label + " recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New(label + " audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New(label + " volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New(label + " volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &TemplateCreateExecutor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *TemplateCreateExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationTemplateCreate {
		return recovery.OperationSupport{Reason: "unsupported_template_create_operation"}
	}
	if record.Phase != operations.OperationPhaseTemplateCreateValidate && record.Phase != operations.OperationPhaseTemplateCreateWriterFenced {
		return recovery.OperationSupport{Reason: "unsupported_template_create_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_template_create_recovery_action"}
	}
}

func (executor *TemplateCreateExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported template create operation recovery: %s", support.Reason)
	}
	if err := validateTemplateLeasedRecord(record, executor.owner, operations.OperationTemplateCreate, operations.OperationPhaseTemplateCreateValidate); err != nil {
		return err
	}
	now := executor.currentTime()
	if now.IsZero() {
		return errors.New("template create recovery time must be set")
	}
	source, binding, err := executor.validateSourceRepo(ctx, record)
	if err != nil {
		return executor.commitTemplateCreateFailed(ctx, record, now, "TEMPLATE_CREATE_VALIDATION_FAILED", "template create validation failed")
	}
	sourceControlRoot, err := executor.controlRoot(source)
	if err != nil {
		return executor.commitTemplateCreateFailed(ctx, record, now, "TEMPLATE_CREATE_VALIDATION_FAILED", "template create validation failed")
	}
	paths, err := templateRootPaths(executor.volumeRoots[binding.DefaultVolumeID], record.NamespaceID, record.TemplateID)
	if err != nil {
		return executor.commitTemplateCreateFailed(ctx, record, now, "TEMPLATE_CREATE_VALIDATION_FAILED", "template create validation failed")
	}
	working := record
	if record.Phase == operations.OperationPhaseTemplateCreateValidate {
		fence := templateCreateWriterFenceForOperation(record, now)
		working = withTemplateCreateWriterFencedMarker(record, fence.ID)
		updatedFence, updatedOperation, err := executor.store.MarkTemplateCreateWriterFencedWithLease(ctx, fence, working.SanitizedForPersistence(), executor.owner, now)
		if err != nil {
			return errors.New("template create writer fence mark failed")
		}
		working = updatedOperation
		working.SessionFenceID = updatedFence.ID
	}
	if err := executor.checkTemplateCreateWriterSessions(ctx, working, now); err != nil {
		return executor.commitTemplateCreateFailed(ctx, working, now, "SOURCE_DIRTY_AFTER_TEMPLATE_SAVE", "source repo has active or stale writer sessions after template writer fence")
	}
	save, err := executor.jvs.Save(ctx, sourceControlRoot, "template "+record.TemplateID)
	if err != nil {
		return executor.commitTemplateCreateIntervention(ctx, working, now, "JVS_COMMAND_FAILED", "jvs save failed", nil)
	}
	clone, err := executor.jvs.RepoClone(ctx, sourceControlRoot, paths.PayloadRootPath, paths.ControlRootPath)
	if err != nil {
		return executor.commitTemplateCreateIntervention(ctx, working, now, "JVS_COMMAND_FAILED", "jvs repo clone failed", map[string]any{"source_save_point_id": save.SavePointID})
	}
	doctor, err := executor.jvs.DoctorStrict(ctx, paths.ControlRootPath)
	if err != nil {
		return executor.commitTemplateCreateIntervention(ctx, working, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", map[string]any{"source_save_point_id": save.SavePointID})
	}
	if clone.SavePointsMode != "main" || clone.RuntimeStateCopied || clone.SourceRepoID != source.JVSRepoID || clone.TargetRepoID != doctor.RepoID {
		return executor.commitTemplateCreateIntervention(ctx, working, now, "JVS_REPO_ID_MISMATCH", "jvs repo identity mismatch", map[string]any{"source_save_point_id": save.SavePointID})
	}
	template := resources.Repo{
		ID:                  record.TemplateID,
		NamespaceID:         record.NamespaceID,
		VolumeID:            binding.DefaultVolumeID,
		JVSRepoID:           clone.TargetRepoID,
		Kind:                resources.RepoKindTemplate,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: paths.ControlVolumeSubdir,
		PayloadVolumeSubdir: paths.PayloadVolumeSubdir,
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
		CreatedAt:           createdAt(record, now),
		UpdatedAt:           now,
	}
	operation := working
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseTemplateCreateCommitted
	operation.ExternalResourceIDs = map[string]string{"source_save_point_id": save.SavePointID, "jvs_repo_id": clone.TargetRepoID}
	operation.JVSJSONOutput = map[string]any{"source_repo_id": clone.SourceRepoID, "target_repo_id": clone.TargetRepoID, "save_points_mode": clone.SavePointsMode, "save_points_copied_count": clone.SavePointsCopiedCount, "runtime_state_copied": clone.RuntimeStateCopied, "workspace": clone.Workspace}
	operation.VerificationResult = map[string]any{"source_repo_id": record.RepoID, "template_id": record.TemplateID, "source_save_point_id": save.SavePointID, "clone_history_mode": "main", "healthy": true}
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now, audit.EventTypeTemplateCreate, audit.OutcomeSucceeded, "template_create_committed", map[string]any{"source_repo_id": record.RepoID, "template_id": record.TemplateID, "source_save_point_id": save.SavePointID})
	if err != nil {
		return err
	}
	if _, _, err := executor.store.CommitTemplateCreateSucceededWithLease(ctx, template, record.RepoID, save.SavePointID, "main", operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("template create success commit failed")
	}
	return nil
}

func (executor *TemplateCloneExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationTemplateClone {
		return recovery.OperationSupport{Reason: "unsupported_template_clone_operation"}
	}
	if record.Phase != operations.OperationPhaseTemplateCloneValidate {
		return recovery.OperationSupport{Reason: "unsupported_template_clone_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_template_clone_recovery_action"}
	}
}

func (executor *TemplateCloneExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	base := (*TemplateCreateExecutor)(executor)
	if ctx == nil {
		ctx = context.Background()
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported template clone operation recovery: %s", support.Reason)
	}
	if err := validateTemplateLeasedRecord(record, base.owner, operations.OperationTemplateClone, operations.OperationPhaseTemplateCloneValidate); err != nil {
		return err
	}
	now := base.currentTime()
	if now.IsZero() {
		return errors.New("template clone recovery time must be set")
	}
	template, binding, err := base.validateTemplateSource(ctx, record)
	if err != nil {
		return base.commitTemplateCloneFailed(ctx, record, now, "TEMPLATE_CLONE_VALIDATION_FAILED", "template clone validation failed")
	}
	sourceControlRoot, err := base.controlRoot(template)
	if err != nil {
		return base.commitTemplateCloneFailed(ctx, record, now, "TEMPLATE_CLONE_VALIDATION_FAILED", "template clone validation failed")
	}
	paths, err := pathresolver.ResolveRepoRootPaths(base.volumeRoots[binding.DefaultVolumeID], record.NamespaceID, record.RepoID)
	if err != nil {
		return base.commitTemplateCloneFailed(ctx, record, now, "TEMPLATE_CLONE_VALIDATION_FAILED", "template clone validation failed")
	}
	clone, err := base.jvs.RepoClone(ctx, sourceControlRoot, paths.PayloadRootPath, paths.ControlRootPath)
	if err != nil {
		return base.commitTemplateCloneIntervention(ctx, record, now, "JVS_COMMAND_FAILED", "jvs repo clone failed", nil)
	}
	doctor, err := base.jvs.DoctorStrict(ctx, paths.ControlRootPath)
	if err != nil {
		return base.commitTemplateCloneIntervention(ctx, record, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", nil)
	}
	if clone.SavePointsMode != "main" || clone.RuntimeStateCopied || clone.SourceRepoID != template.JVSRepoID || clone.TargetRepoID != doctor.RepoID {
		return base.commitTemplateCloneIntervention(ctx, record, now, "JVS_REPO_ID_MISMATCH", "jvs repo identity mismatch", nil)
	}
	repo := resources.Repo{
		ID:                  record.RepoID,
		NamespaceID:         record.NamespaceID,
		VolumeID:            binding.DefaultVolumeID,
		JVSRepoID:           clone.TargetRepoID,
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: paths.ControlVolumeSubdir,
		PayloadVolumeSubdir: paths.PayloadVolumeSubdir,
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive, LastLifecycleOperationID: record.ID},
		CreatedAt:           createdAt(record, now),
		UpdatedAt:           now,
	}
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseTemplateCloneCommitted
	operation.ExternalResourceIDs = map[string]string{"jvs_repo_id": clone.TargetRepoID}
	operation.JVSJSONOutput = map[string]any{"source_repo_id": clone.SourceRepoID, "target_repo_id": clone.TargetRepoID, "save_points_mode": clone.SavePointsMode, "save_points_copied_count": clone.SavePointsCopiedCount, "runtime_state_copied": clone.RuntimeStateCopied, "workspace": clone.Workspace}
	operation.VerificationResult = map[string]any{"template_id": record.TemplateID, "repo_id": record.RepoID, "clone_history_mode": "main", "healthy": true}
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := base.auditEvent(operation, now, audit.EventTypeTemplateClone, audit.OutcomeSucceeded, "template_clone_committed", map[string]any{"template_id": record.TemplateID, "repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, _, err := base.store.CommitTemplateCloneSucceededWithLease(ctx, repo, operation.SanitizedForPersistence(), base.owner, now, event); err != nil {
		return errors.New("template clone success commit failed")
	}
	return nil
}

func (executor *TemplateCreateExecutor) currentTime() time.Time {
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	return now
}

func (executor *TemplateCreateExecutor) validateSourceRepo(ctx context.Context, record operations.OperationRecord) (resources.Repo, resources.NamespaceVolumeBinding, error) {
	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return resources.Repo{}, resources.NamespaceVolumeBinding{}, err
	}
	namespace, binding, held, err := executor.commonNamespaceMetadata(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return resources.Repo{}, resources.NamespaceVolumeBinding{}, err
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: templateCreateRepoAccessFences(record, held), Intent: repoaccess.IntentTemplateCreateFromRepo, Mode: repoaccess.ModeReadOnly})
	if !decision.Allowed || repo.VolumeID != binding.DefaultVolumeID {
		return resources.Repo{}, resources.NamespaceVolumeBinding{}, errors.New("template create metadata invalid")
	}
	return repo, binding, nil
}

func templateCreateRepoAccessFences(record operations.OperationRecord, held []repoaccess.Fence) []repoaccess.Fence {
	filtered := make([]repoaccess.Fence, 0, len(held))
	for _, fence := range held {
		if fence.Kind == repoaccess.FenceKindWriterSession && fence.HolderOperationID == record.ID {
			continue
		}
		filtered = append(filtered, fence)
	}
	return filtered
}

func (executor *TemplateCreateExecutor) validateTemplateSource(ctx context.Context, record operations.OperationRecord) (resources.Repo, resources.NamespaceVolumeBinding, error) {
	template, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.TemplateID)
	if err != nil {
		return resources.Repo{}, resources.NamespaceVolumeBinding{}, err
	}
	_, binding, _, err := executor.commonNamespaceMetadata(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return resources.Repo{}, resources.NamespaceVolumeBinding{}, err
	}
	if template.Kind != resources.RepoKindTemplate || template.Status != resources.RepoStatusActive || template.VolumeID != binding.DefaultVolumeID {
		return resources.Repo{}, resources.NamespaceVolumeBinding{}, errors.New("template clone metadata invalid")
	}
	return template, binding, nil
}

func (executor *TemplateCreateExecutor) commonNamespaceMetadata(ctx context.Context, namespaceID, fenceRepoID string) (resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, error) {
	namespace, err := executor.store.GetNamespace(ctx, namespaceID)
	if err != nil {
		return resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, err
	}
	binding, err := executor.store.GetNamespaceVolumeBinding(ctx, namespaceID)
	if err != nil || binding.Status != resources.NamespaceStatusActive || binding.TemplatePolicy["namespace_templates_enabled"] != true {
		return resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, errors.New("invalid namespace binding")
	}
	volume, err := executor.store.GetVolume(ctx, binding.DefaultVolumeID)
	if err != nil || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, errors.New("invalid volume")
	}
	held, err := executor.store.ListHeldRepoFences(ctx, fenceRepoID)
	if err != nil {
		return resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, err
	}
	return namespace, binding, savePointRepoAccessFencesFromStore(held), nil
}

func (executor *TemplateCreateExecutor) controlRoot(repo resources.Repo) (string, error) {
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

func templateRootPaths(volumeRoot, namespaceID, templateID string) (pathresolver.RepoRootPaths, error) {
	if err := validateVolumeRoot(volumeRoot); err != nil {
		return pathresolver.RepoRootPaths{}, err
	}
	paths, err := pathresolver.ResolveTemplatePaths(namespaceID, templateID)
	if err != nil {
		return pathresolver.RepoRootPaths{}, err
	}
	controlRoot := filepath.Join(volumeRoot, filepath.FromSlash(paths.ControlVolumeSubdir))
	payloadRoot := filepath.Join(volumeRoot, filepath.FromSlash(paths.PayloadVolumeSubdir))
	return pathresolver.RepoRootPaths{RepoPaths: pathresolver.RepoPaths{ContainerVolumeSubdir: paths.ContainerVolumeSubdir, ControlVolumeSubdir: paths.ControlVolumeSubdir, PayloadVolumeSubdir: paths.PayloadVolumeSubdir}, ControlRootPath: controlRoot, PayloadRootPath: payloadRoot}, nil
}

func validateTemplateLeasedRecord(record operations.OperationRecord, owner string, typ operations.OperationType, phase string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid template recovery record")
	}
	if record.Type != typ || strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || strings.TrimSpace(record.TemplateID) == "" {
		return errors.New("invalid template recovery record")
	}
	if record.Phase != phase {
		if typ != operations.OperationTemplateCreate || record.Phase != operations.OperationPhaseTemplateCreateWriterFenced || strings.TrimSpace(record.SessionFenceID) == "" {
			return errors.New("invalid template recovery record")
		}
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid template recovery record")
	}
	return nil
}

func (executor *TemplateCreateExecutor) commitTemplateCreateFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string) error {
	phase := operations.OperationPhaseTemplateCreateValidate
	if record.Phase == operations.OperationPhaseTemplateCreateWriterFenced {
		phase = operations.OperationPhaseTemplateCreateWriterFenced
	}
	operation := templateFailedOperation(record, now, operations.OperationStateFailed, phase, code, message)
	event, err := executor.auditEvent(operation, now, audit.EventTypeTemplateCreate, audit.OutcomeFailed, "template_create_failed", map[string]any{"source_repo_id": record.RepoID, "template_id": record.TemplateID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitTemplateCreateFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("template create failure commit failed")
	}
	return nil
}

func (executor *TemplateCreateExecutor) commitTemplateCreateIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	phase := operations.OperationPhaseTemplateCreateValidate
	if record.Phase == operations.OperationPhaseTemplateCreateWriterFenced {
		phase = operations.OperationPhaseTemplateCreateWriterFenced
	}
	operation := templateFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, phase, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	event, err := executor.auditEvent(operation, now, audit.EventTypeTemplateCreate, audit.OutcomeFailed, "template_create_operator_intervention_required", map[string]any{"source_repo_id": record.RepoID, "template_id": record.TemplateID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitTemplateCreateFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("template create intervention commit failed")
	}
	return fmt.Errorf("%w: template create operator intervention required", recovery.ErrOperationManualIntervention)
}

func templateCreateWriterFenceForOperation(record operations.OperationRecord, now time.Time) fences.Fence {
	return fences.Fence{ID: "fence_" + record.ID, RepoID: record.RepoID, Kind: fences.KindWriterSession, HolderOperationID: record.ID, Status: fences.StatusActive, ExpiresAt: leaseOrDefault(record, now), CreatedAt: now, UpdatedAt: now}
}

func withTemplateCreateWriterFencedMarker(record operations.OperationRecord, fenceID string) operations.OperationRecord {
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseTemplateCreateWriterFenced
	record.SessionFenceID = fenceID
	record.VerificationResult = mergeStringAnyMap(asStringAnyMap(record.VerificationResult), map[string]any{
		"writer_fence_acquired": true,
		"clone_history_mode":    "main",
	})
	return record
}

func (executor *TemplateCreateExecutor) checkTemplateCreateWriterSessions(ctx context.Context, record operations.OperationRecord, now time.Time) error {
	exports, err := executor.store.ListExportSessionsByRepo(ctx, record.RepoID)
	if err != nil {
		return err
	}
	mounts, err := executor.store.ListWorkloadMountBindingsByRepo(ctx, record.RepoID)
	if err != nil {
		return err
	}
	decision := sessionstate.RestoreRunWriterGate(sessionstate.GateRequest{NamespaceID: record.NamespaceID, RepoID: record.RepoID, Now: now, ExportSessions: exports, Mounts: mounts})
	if decision.Allowed {
		return nil
	}
	return errors.New(decision.ErrorFamily.String())
}

func (executor *TemplateCreateExecutor) commitTemplateCloneFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string) error {
	operation := templateFailedOperation(record, now, operations.OperationStateFailed, operations.OperationPhaseTemplateCloneValidate, code, message)
	event, err := executor.auditEvent(operation, now, audit.EventTypeTemplateClone, audit.OutcomeFailed, "template_clone_failed", map[string]any{"template_id": record.TemplateID, "repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitTemplateCloneFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("template clone failure commit failed")
	}
	return nil
}

func (executor *TemplateCreateExecutor) commitTemplateCloneIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := templateFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, operations.OperationPhaseTemplateCloneValidate, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	event, err := executor.auditEvent(operation, now, audit.EventTypeTemplateClone, audit.OutcomeFailed, "template_clone_operator_intervention_required", map[string]any{"template_id": record.TemplateID, "repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitTemplateCloneFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("template clone intervention commit failed")
	}
	return fmt.Errorf("%w: template clone operator intervention required", recovery.ErrOperationManualIntervention)
}

func templateFailedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, phase, code, message string) operations.OperationRecord {
	operation := record
	operation.State = state
	operation.Phase = phase
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID, "template_id": record.TemplateID}}
	operation.FinishedAt = &now
	return operation
}

func (executor *TemplateCreateExecutor) auditEvent(operation operations.OperationRecord, now time.Time, eventType audit.EventType, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("template audit event id must be set")
	}
	resourceType, resourceID := operation.Resource.Type, operation.Resource.ID
	return audit.NewEvent(audit.Event{EventID: eventID, Type: eventType, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: resourceType, ID: resourceID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

var (
	_ recovery.OperationExecutor = (*TemplateCreateExecutor)(nil)
	_ recovery.OperationExecutor = (*TemplateCloneExecutor)(nil)
)
