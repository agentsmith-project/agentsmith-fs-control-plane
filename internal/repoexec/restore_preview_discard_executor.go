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

type RestorePreviewDiscardJVSRunner interface {
	RecoveryStatus(ctx context.Context, controlRoot string) (jvsrunner.RecoveryStatusSummary, error)
	RestoreDiscard(ctx context.Context, controlRoot, planID string) (jvsrunner.RestoreDiscardSummary, error)
}

type RestorePreviewDiscardConfig struct {
	Store        restorePreviewDiscardStore
	JVSRunner    RestorePreviewDiscardJVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type restorePreviewDiscardStore interface {
	store.RestorePreviewDiscardOperationCommitStore
	store.RestorePreviewDiscardOperationMetadataReader
}

type RestorePreviewDiscardExecutor struct {
	store        restorePreviewDiscardStore
	jvs          RestorePreviewDiscardJVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

func NewRestorePreviewDiscardExecutor(config RestorePreviewDiscardConfig) (*RestorePreviewDiscardExecutor, error) {
	if config.Store == nil {
		return nil, errors.New("restore preview discard recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("restore preview discard jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("restore preview discard recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("restore preview discard recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("restore preview discard audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("restore preview discard volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("restore preview discard volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &RestorePreviewDiscardExecutor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *RestorePreviewDiscardExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationRestorePreviewDiscard {
		return recovery.OperationSupport{Reason: "unsupported_restore_preview_discard_operation"}
	}
	if record.Phase != operations.OperationPhaseRestorePreviewDiscardValidate && record.Phase != operations.OperationPhaseRestorePreviewDiscarding {
		return recovery.OperationSupport{Reason: "unsupported_restore_preview_discard_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_restore_preview_discard_recovery_action"}
	}
}

func (executor *RestorePreviewDiscardExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported restore preview discard operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported restore preview discard operation recovery: %s", support.Reason)
	}
	if err := validateRestorePreviewDiscardLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	now := executor.currentTime()
	if now.IsZero() {
		return errors.New("restore preview discard recovery time must be set")
	}
	previewOperationID, err := restorePreviewDiscardPreviewOperationID(record)
	if err != nil {
		return executor.commitRestorePreviewDiscardFailed(ctx, record, now, "RESTORE_PREVIEW_DISCARD_VALIDATION_FAILED", "restore preview discard validation failed")
	}
	previewOperation, err := executor.store.GetOperation(ctx, previewOperationID)
	if err != nil {
		return executor.commitRestorePreviewDiscardIntervention(ctx, record, now, "RESTORE_PREVIEW_DISCARD_PREVIEW_OPERATION_INVALID", "restore preview discard preview operation invalid", nil)
	}
	durablePlan, err := executor.store.GetRestorePlanByPreviewOperation(ctx, previewOperationID)
	if err != nil {
		return executor.commitRestorePreviewDiscardIntervention(ctx, record, now, "RESTORE_PREVIEW_DISCARD_PLAN_INVALID", "restore preview discard plan invalid", nil)
	}
	if err := validateRestorePreviewDiscardPreviewAndPlan(record, previewOperation, durablePlan); err != nil {
		return executor.commitRestorePreviewDiscardIntervention(ctx, record, now, "RESTORE_PREVIEW_DISCARD_PLAN_MISMATCH", "restore preview discard plan mismatch", nil)
	}
	if record.Phase == operations.OperationPhaseRestorePreviewDiscardValidate && durablePlan.Status != restoreplan.StatusPending {
		return executor.commitRestorePreviewDiscardIntervention(ctx, record, now, "RESTORE_PREVIEW_DISCARD_PLAN_NOT_PENDING", "restore preview discard plan is not pending", map[string]any{"restore_plan_id": durablePlan.ID, "restore_plan_status": durablePlan.Status.String()})
	}
	if record.Phase == operations.OperationPhaseRestorePreviewDiscarding && durablePlan.Status != restoreplan.StatusDiscarding {
		return executor.commitRestorePreviewDiscardIntervention(ctx, record, now, "RESTORE_PREVIEW_DISCARD_PLAN_NOT_DISCARDING", "restore preview discard plan is not discarding", map[string]any{"restore_plan_id": durablePlan.ID, "restore_plan_status": durablePlan.Status.String()})
	}

	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return executor.commitRestorePreviewDiscardFailed(ctx, record, now, "RESTORE_PREVIEW_DISCARD_VALIDATION_FAILED", "restore preview discard validation failed")
	}
	if err := executor.validateMetadata(ctx, record, repo); err != nil {
		return executor.commitRestorePreviewDiscardFailed(ctx, record, now, "RESTORE_PREVIEW_DISCARD_VALIDATION_FAILED", "restore preview discard validation failed")
	}
	controlRoot, err := executor.controlRoot(repo)
	if err != nil {
		return executor.commitRestorePreviewDiscardFailed(ctx, record, now, "RESTORE_PREVIEW_DISCARD_VALIDATION_FAILED", "restore preview discard validation failed")
	}

	status, err := executor.jvs.RecoveryStatus(ctx, controlRoot)
	if err != nil {
		return executor.commitRestorePreviewDiscardIntervention(ctx, record, now, "JVS_RECOVERY_STATUS_FAILED", "jvs recovery status failed", nil)
	}
	if !restorePreviewDiscardRecoveryStatusMatchesPlan(status, durablePlan.ID) {
		return executor.commitRestorePreviewDiscardIntervention(ctx, record, now, "RESTORE_PREVIEW_DISCARD_RECOVERY_STATE_MISMATCH", "restore preview discard recovery state mismatch", restorePreviewDiscardRecoveryStatusDetails(status, durablePlan.ID))
	}

	working := record
	if record.Phase == operations.OperationPhaseRestorePreviewDiscardValidate {
		working = withRestorePreviewDiscardingMarker(record, durablePlan, status)
		updatedPlan, updatedOperation, err := executor.store.MarkRestorePreviewDiscardingWithLease(ctx, durablePlan, working.SanitizedForPersistence(), executor.owner, now)
		if err != nil {
			return errors.New("restore preview discard mark discarding failed")
		}
		durablePlan = updatedPlan
		working = updatedOperation
	}

	discard, err := executor.jvs.RestoreDiscard(ctx, controlRoot, durablePlan.ID)
	if err != nil {
		return executor.commitRestorePreviewDiscardIntervention(ctx, working, now, "JVS_RESTORE_DISCARD_FAILED", "jvs restore discard failed", map[string]any{"restore_plan_id": durablePlan.ID})
	}
	if err := validateRestoreDiscardSummary(discard, durablePlan.ID); err != nil {
		return executor.commitRestorePreviewDiscardIntervention(ctx, working, now, "RESTORE_PREVIEW_DISCARD_RESULT_MISMATCH", "restore preview discard result mismatch", map[string]any{"restore_plan_id": durablePlan.ID})
	}
	return executor.commitRestorePreviewDiscardSuccess(ctx, working, now, discard)
}

func (executor *RestorePreviewDiscardExecutor) currentTime() time.Time {
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	return now
}

func (executor *RestorePreviewDiscardExecutor) validateMetadata(ctx context.Context, record operations.OperationRecord, repo resources.Repo) error {
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
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: savePointRepoAccessFencesFromStore(held), Intent: repoaccess.IntentRestorePreviewDiscard, Mode: repoaccess.ModeReadOnly})
	if !decision.Allowed {
		return errors.New("repo access denied")
	}
	volume, err := executor.store.GetVolume(ctx, repo.VolumeID)
	if err != nil || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return errors.New("invalid volume")
	}
	return nil
}

func (executor *RestorePreviewDiscardExecutor) controlRoot(repo resources.Repo) (string, error) {
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

func (executor *RestorePreviewDiscardExecutor) commitRestorePreviewDiscardSuccess(ctx context.Context, record operations.OperationRecord, now time.Time, summary jvsrunner.RestoreDiscardSummary) error {
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseRestorePreviewDiscardCommitted
	operation.ExternalResourceIDs = map[string]string{"restore_plan_id": summary.PlanID}
	operation.JVSJSONOutput = map[string]any{"restore_plan_id": summary.PlanID, "plan_discarded": summary.PlanDiscarded, "workspace": summary.Workspace}
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), map[string]any{"preview_operation_id": restorePreviewDiscardPreviewOperationIDOrEmpty(operation), "restore_plan_id": summary.PlanID, "plan_discarded": summary.PlanDiscarded, "workspace": summary.Workspace, "restore_plan_status": restoreplan.StatusDiscarded.String()})
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now, audit.OutcomeSucceeded, "restore_preview_discard_committed", map[string]any{"repo_id": record.RepoID, "restore_plan_id": summary.PlanID})
	if err != nil {
		return err
	}
	if _, _, err := executor.store.CommitRestorePreviewDiscardSucceededWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore preview discard success commit failed")
	}
	return nil
}

func (executor *RestorePreviewDiscardExecutor) commitRestorePreviewDiscardFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string) error {
	operation := restorePreviewDiscardFailedOperation(record, now, operations.OperationStateFailed, code, message)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_preview_discard_failed", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRestorePreviewDiscardFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore preview discard failure commit failed")
	}
	return nil
}

func (executor *RestorePreviewDiscardExecutor) commitRestorePreviewDiscardIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := restorePreviewDiscardFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_preview_discard_operator_intervention_required", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRestorePreviewDiscardFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore preview discard intervention commit failed")
	}
	return fmt.Errorf("%w: restore preview discard operator intervention required", recovery.ErrOperationManualIntervention)
}

func restorePreviewDiscardFailedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, code, message string) operations.OperationRecord {
	operation := record
	operation.State = state
	if operation.Phase != operations.OperationPhaseRestorePreviewDiscarding {
		operation.Phase = operations.OperationPhaseRestorePreviewDiscardValidate
	}
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID}}
	operation.FinishedAt = &now
	return operation
}

func (executor *RestorePreviewDiscardExecutor) auditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("restore preview discard audit event id must be set")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: audit.EventTypeRestorePreviewDiscard, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func validateRestorePreviewDiscardLeasedRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid restore preview discard recovery record")
	}
	if record.Type != operations.OperationRestorePreviewDiscard || (record.Phase != operations.OperationPhaseRestorePreviewDiscardValidate && record.Phase != operations.OperationPhaseRestorePreviewDiscarding) {
		return errors.New("invalid restore preview discard recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return errors.New("invalid restore preview discard recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid restore preview discard recovery record")
	}
	return nil
}

func restorePreviewDiscardPreviewOperationID(record operations.OperationRecord) (string, error) {
	raw, _ := record.InputSummary["preview_operation_id"].(string)
	operationID := strings.TrimSpace(raw)
	if err := pathresolver.ValidateID(pathresolver.OperationID, operationID); err != nil {
		return "", err
	}
	return operationID, nil
}

func restorePreviewDiscardPreviewOperationIDOrEmpty(record operations.OperationRecord) string {
	id, _ := restorePreviewDiscardPreviewOperationID(record)
	return id
}

func validateRestorePreviewDiscardPreviewAndPlan(record, preview operations.OperationRecord, plan restoreplan.Plan) error {
	if preview.Type != operations.OperationRestorePreview || preview.State != operations.OperationStateSucceeded || preview.Phase != operations.OperationPhaseRestorePreviewCommitted {
		return errors.New("preview operation is not a succeeded restore preview")
	}
	if preview.NamespaceID != record.NamespaceID || preview.RepoID != record.RepoID || preview.Resource.Type != "repo" || preview.Resource.ID != record.RepoID {
		return errors.New("preview operation does not match discard target")
	}
	previewOperationID, err := restorePreviewDiscardPreviewOperationID(record)
	if err != nil {
		return err
	}
	if plan.PreviewOperationID != previewOperationID || plan.NamespaceID != record.NamespaceID || plan.RepoID != record.RepoID {
		return errors.New("restore plan does not match discard target")
	}
	return nil
}

func withRestorePreviewDiscardingMarker(record operations.OperationRecord, plan restoreplan.Plan, status jvsrunner.RecoveryStatusSummary) operations.OperationRecord {
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseRestorePreviewDiscarding
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": plan.ID}
	record.VerificationResult = mergeStringAnyMap(asStringAnyMap(record.VerificationResult), map[string]any{
		"preview_operation_id": plan.PreviewOperationID,
		"restore_plan_id":      plan.ID,
		"restore_state":        status.RestoreState,
		"active_plan_matches":  strings.TrimSpace(status.ActivePlanID) == plan.ID,
		"blocking":             status.Blocking,
		"workspace":            status.Workspace,
		"restore_plan_status":  restoreplan.StatusDiscarding.String(),
	})
	return record
}

func restorePreviewDiscardRecoveryStatusMatchesPlan(status jvsrunner.RecoveryStatusSummary, planID string) bool {
	state := status.RestoreState
	return (state == "pending_restore_preview" || state == "stale_restore_preview") &&
		status.Workspace == "main" &&
		strings.TrimSpace(status.ActivePlanID) == planID &&
		status.Blocking
}

func restorePreviewDiscardRecoveryStatusDetails(status jvsrunner.RecoveryStatusSummary, planID string) map[string]any {
	return map[string]any{
		"restore_state":           status.RestoreState,
		"active_plan_present":     strings.TrimSpace(status.ActivePlanID) != "",
		"active_plan_matches":     strings.TrimSpace(status.ActivePlanID) == planID,
		"active_recovery_present": strings.TrimSpace(status.ActiveRecoveryPlanID) != "",
		"blocking":                status.Blocking,
		"workspace":               status.Workspace,
	}
}

func validateRestoreDiscardSummary(summary jvsrunner.RestoreDiscardSummary, planID string) error {
	if !restorePreviewSafeOpaqueID(summary.PlanID) || summary.PlanID != planID || summary.Workspace != "main" || !summary.PlanDiscarded {
		return errors.New("invalid restore discard summary")
	}
	return nil
}

var _ recovery.OperationExecutor = (*RestorePreviewDiscardExecutor)(nil)
