package repoexec

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

type RestorePreviewJVSRunner interface {
	RecoveryStatus(ctx context.Context, controlRoot string) (jvsrunner.RecoveryStatusSummary, error)
	RestorePreview(ctx context.Context, controlRoot, savePointID string) (jvsrunner.RestorePreviewSummary, error)
}

type RestorePreviewConfig struct {
	Store        restorePreviewStore
	JVSRunner    RestorePreviewJVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type restorePreviewStore interface {
	store.RestorePreviewOperationCommitStore
	store.RestorePreviewOperationMetadataReader
}

type RestorePreviewExecutor struct {
	store        restorePreviewStore
	jvs          RestorePreviewJVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

func NewRestorePreviewExecutor(config RestorePreviewConfig) (*RestorePreviewExecutor, error) {
	if config.Store == nil {
		return nil, errors.New("restore preview recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("restore preview jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("restore preview recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("restore preview recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("restore preview audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("restore preview volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("restore preview volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &RestorePreviewExecutor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *RestorePreviewExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationRestorePreview {
		return recovery.OperationSupport{Reason: "unsupported_restore_preview_operation"}
	}
	if record.Phase != operations.OperationPhaseRestorePreviewValidate && record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle {
		return recovery.OperationSupport{Reason: "unsupported_restore_preview_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_restore_preview_recovery_action"}
	}
}

func (executor *RestorePreviewExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported restore preview operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported restore preview operation recovery: %s", support.Reason)
	}
	if err := validateRestorePreviewLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	savePointID, err := restorePreviewSavePointID(record)
	if err != nil {
		now := executor.currentTime()
		if now.IsZero() {
			return errors.New("restore preview recovery time must be set")
		}
		return executor.commitRestorePreviewFailed(ctx, record, now, "RESTORE_PREVIEW_VALIDATION_FAILED", "restore preview validation failed")
	}
	now := executor.currentTime()
	if now.IsZero() {
		return errors.New("restore preview recovery time must be set")
	}

	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return executor.commitRestorePreviewFailed(ctx, record, now, "RESTORE_PREVIEW_VALIDATION_FAILED", "restore preview validation failed")
	}
	if err := executor.validateMetadata(ctx, record, repo); err != nil {
		return executor.commitRestorePreviewFailed(ctx, record, now, "RESTORE_PREVIEW_VALIDATION_FAILED", "restore preview validation failed")
	}
	controlRoot, err := executor.controlRoot(repo)
	if err != nil {
		return executor.commitRestorePreviewFailed(ctx, record, now, "RESTORE_PREVIEW_VALIDATION_FAILED", "restore preview validation failed")
	}

	if record.Phase == operations.OperationPhaseRestorePreviewPreflightIdle {
		if !restorePreviewPreflightMarker(record) {
			return executor.commitRestorePreviewIntervention(ctx, record, now, "RESTORE_PREVIEW_PREFLIGHT_MARKER_MISSING", "restore preview preflight marker missing", nil)
		}
		status, err := executor.jvs.RecoveryStatus(ctx, controlRoot)
		if err != nil {
			return executor.commitRestorePreviewIntervention(ctx, record, now, "JVS_RECOVERY_STATUS_FAILED", "jvs recovery status failed", withJVSErrorDetails(nil, err))
		}
		return executor.commitRestorePreviewIntervention(ctx, record, now, "RESTORE_PREVIEW_PREFLIGHT_REQUIRES_OPERATOR", "restore preview preflight recovery requires operator intervention", restorePreviewRecoveryStatusDetails(status))
	}

	status, err := executor.jvs.RecoveryStatus(ctx, controlRoot)
	if err != nil {
		return executor.commitRestorePreviewIntervention(ctx, record, now, "JVS_RECOVERY_STATUS_FAILED", "jvs recovery status failed", withJVSErrorDetails(nil, err))
	}
	if !restorePreviewRecoveryStatusIdle(status) {
		return executor.commitRestorePreviewIntervention(ctx, record, now, "RESTORE_PREVIEW_RECOVERY_STATE_NOT_IDLE", "restore preview recovery state is not idle", restorePreviewRecoveryStatusDetails(status))
	}

	working := withRestorePreviewPreflightMarker(record, status)
	working.State = operations.OperationStateRunning
	working.Phase = operations.OperationPhaseRestorePreviewPreflightIdle
	updated, err := executor.store.UpdateRestorePreviewPreflightWithLease(ctx, working.SanitizedForPersistence(), executor.owner, now)
	if err != nil {
		return errors.New("restore preview preflight update failed")
	}
	working = updated

	preview, err := executor.jvs.RestorePreview(ctx, controlRoot, savePointID)
	if err != nil {
		return executor.commitRestorePreviewIntervention(ctx, working, now, "JVS_RESTORE_PREVIEW_FAILED", "jvs restore preview failed", withJVSErrorDetails(map[string]any{"source_save_point_id": savePointID}, err))
	}
	if err := validateRestorePreviewSummary(preview, savePointID); err != nil {
		return executor.commitRestorePreviewIntervention(ctx, working, now, "RESTORE_PREVIEW_RESULT_MISMATCH", "restore preview result mismatch", map[string]any{"source_save_point_id": savePointID})
	}
	return executor.commitRestorePreviewSuccess(ctx, working, now, preview)
}

func (executor *RestorePreviewExecutor) currentTime() time.Time {
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	return now
}

func (executor *RestorePreviewExecutor) validateMetadata(ctx context.Context, record operations.OperationRecord, repo resources.Repo) error {
	namespace, err := executor.store.GetNamespace(ctx, record.NamespaceID)
	if err != nil {
		return err
	}
	binding, err := executor.store.GetNamespaceVolumeBinding(ctx, record.NamespaceID)
	if err != nil {
		return err
	}
	held, err := executor.store.ListHeldRepoFences(ctx, record.RepoID)
	if err != nil {
		return err
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: savePointRepoAccessFencesFromStore(held), Intent: repoaccess.IntentSavePointCreate, Mode: repoaccess.ModeReadOnly})
	if !decision.Allowed {
		return errors.New("repo access denied")
	}
	volume, err := executor.store.GetVolume(ctx, repo.VolumeID)
	if err != nil || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return errors.New("invalid volume")
	}
	return nil
}

func (executor *RestorePreviewExecutor) controlRoot(repo resources.Repo) (string, error) {
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

func (executor *RestorePreviewExecutor) commitRestorePreviewSuccess(ctx context.Context, record operations.OperationRecord, now time.Time, preview jvsrunner.RestorePreviewSummary) error {
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseRestorePreviewCommitted
	operation.ExternalResourceIDs = map[string]string{"restore_plan_id": preview.PlanID, "source_save_point_id": preview.SourceSavePointID}
	operation.JVSJSONOutput = map[string]any{"restore_plan_id": preview.PlanID, "source_save_point_id": preview.SourceSavePointID, "workspace": preview.Workspace, "run_command_present": preview.RunCommandPresent}
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), map[string]any{"restore_plan_id": preview.PlanID, "source_save_point_id": preview.SourceSavePointID, "workspace": preview.Workspace, "run_command_present": preview.RunCommandPresent, "restore_plan_status": restoreplan.StatusPending.String()})
	operation.Error = nil
	operation.FinishedAt = &now
	plan := restoreplan.Plan{ID: preview.PlanID, NamespaceID: record.NamespaceID, RepoID: record.RepoID, PreviewOperationID: record.ID, SourceSavePointID: preview.SourceSavePointID, Status: restoreplan.StatusPending, CreatedAt: now, UpdatedAt: now}
	if err := plan.Validate(); err != nil {
		return executor.commitRestorePreviewIntervention(ctx, record, now, "RESTORE_PREVIEW_RESULT_INVALID", "restore preview result invalid", nil)
	}
	event, err := executor.auditEvent(operation, now, audit.OutcomeSucceeded, "restore_preview_committed", map[string]any{"repo_id": record.RepoID, "restore_plan_id": preview.PlanID, "source_save_point_id": preview.SourceSavePointID})
	if err != nil {
		return err
	}
	if _, _, err := executor.store.CommitRestorePreviewSucceededWithLease(ctx, plan, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore preview success commit failed")
	}
	return nil
}

func (executor *RestorePreviewExecutor) commitRestorePreviewFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string) error {
	operation := restorePreviewFailedOperation(record, now, operations.OperationStateFailed, code, message)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_preview_failed", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRestorePreviewFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore preview failure commit failed")
	}
	return nil
}

func (executor *RestorePreviewExecutor) commitRestorePreviewIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := restorePreviewFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	attachJVSErrorDetails(&operation, details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_preview_operator_intervention_required", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRestorePreviewFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore preview intervention commit failed")
	}
	return fmt.Errorf("%w: restore preview operator intervention required", recovery.ErrOperationManualIntervention)
}

func restorePreviewFailedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, code, message string) operations.OperationRecord {
	operation := record
	operation.State = state
	if operation.Phase != operations.OperationPhaseRestorePreviewPreflightIdle {
		operation.Phase = operations.OperationPhaseRestorePreviewValidate
	}
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID}}
	operation.FinishedAt = &now
	return operation
}

func (executor *RestorePreviewExecutor) auditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("restore preview audit event id must be set")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: audit.EventTypeRestorePreview, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func validateRestorePreviewLeasedRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid restore preview recovery record")
	}
	if record.Type != operations.OperationRestorePreview || (record.Phase != operations.OperationPhaseRestorePreviewValidate && record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle) {
		return errors.New("invalid restore preview recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return errors.New("invalid restore preview recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid restore preview recovery record")
	}
	return nil
}

func restorePreviewSavePointID(record operations.OperationRecord) (string, error) {
	raw, _ := record.InputSummary["save_point_id"].(string)
	savePointID := strings.TrimSpace(raw)
	if !restorePreviewSafeOpaqueID(savePointID) {
		return "", errors.New("invalid restore preview save point id")
	}
	return savePointID, nil
}

func withRestorePreviewPreflightMarker(record operations.OperationRecord, status jvsrunner.RecoveryStatusSummary) operations.OperationRecord {
	verification := asStringAnyMap(record.VerificationResult)
	verification["preflight_recovery_status_captured"] = true
	verification["preflight_restore_state"] = status.RestoreState
	verification["preflight_blocking"] = status.Blocking
	verification["preflight_workspace"] = status.Workspace
	record.VerificationResult = verification
	return record
}

func restorePreviewPreflightMarker(record operations.OperationRecord) bool {
	verification := asStringAnyMap(record.VerificationResult)
	captured, _ := verification["preflight_recovery_status_captured"].(bool)
	state, _ := verification["preflight_restore_state"].(string)
	blocking, _ := verification["preflight_blocking"].(bool)
	return captured && state == "idle" && !blocking
}

func restorePreviewRecoveryStatusIdle(status jvsrunner.RecoveryStatusSummary) bool {
	return status.RestoreState == "idle" &&
		status.Workspace == "main" &&
		strings.TrimSpace(status.ActivePlanID) == "" &&
		strings.TrimSpace(status.ActiveRecoveryPlanID) == "" &&
		!status.Blocking
}

func restorePreviewRecoveryStatusDetails(status jvsrunner.RecoveryStatusSummary) map[string]any {
	return map[string]any{
		"restore_state":           status.RestoreState,
		"active_plan_present":     strings.TrimSpace(status.ActivePlanID) != "",
		"active_recovery_present": strings.TrimSpace(status.ActiveRecoveryPlanID) != "",
		"blocking":                status.Blocking,
		"workspace":               status.Workspace,
	}
}

func validateRestorePreviewSummary(summary jvsrunner.RestorePreviewSummary, savePointID string) error {
	if !restorePreviewSafeOpaqueID(summary.PlanID) || !restorePreviewSafeOpaqueID(summary.SourceSavePointID) {
		return errors.New("invalid restore preview summary ids")
	}
	if summary.SourceSavePointID != savePointID || summary.Workspace != "main" || !summary.RunCommandPresent {
		return errors.New("invalid restore preview summary")
	}
	return nil
}

func restorePreviewSafeOpaqueID(id string) bool {
	if len(id) == 0 || len(id) > 128 || strings.TrimSpace(id) != id {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if i == 0 {
			if !restorePreviewASCIIAlphaNum(b) {
				return false
			}
			continue
		}
		if !restorePreviewASCIIAlphaNum(b) && b != '_' && b != '-' && b != '.' && b != ':' {
			return false
		}
	}
	return true
}

func restorePreviewASCIIAlphaNum(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

var _ recovery.OperationExecutor = (*RestorePreviewExecutor)(nil)
