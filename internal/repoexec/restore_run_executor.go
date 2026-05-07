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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

type RestoreRunJVSRunner interface {
	RecoveryStatus(ctx context.Context, controlRoot string) (jvsrunner.RecoveryStatusSummary, error)
	RestoreRun(ctx context.Context, controlRoot, planID string) (jvsrunner.RestoreRunSummary, error)
	DoctorStrict(ctx context.Context, controlRoot string) (jvsrunner.DoctorSummary, error)
}

type RestoreRunConfig struct {
	Store        restoreRunStore
	JVSRunner    RestoreRunJVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type restoreRunStore interface {
	store.RestoreRunOperationCommitStore
	store.RestoreRunOperationMetadataReader
}

type RestoreRunExecutor struct {
	store        restoreRunStore
	jvs          RestoreRunJVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

func NewRestoreRunExecutor(config RestoreRunConfig) (*RestoreRunExecutor, error) {
	if config.Store == nil {
		return nil, errors.New("restore run recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("restore run jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("restore run recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("restore run recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("restore run audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("restore run volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("restore run volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &RestoreRunExecutor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *RestoreRunExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationRestoreRun {
		return recovery.OperationSupport{Reason: "unsupported_restore_run_operation"}
	}
	switch record.Phase {
	case operations.OperationPhaseRestoreRunValidate, operations.OperationPhaseRestoreRunWriterFenced, operations.OperationPhaseRestoreRunConsuming:
	default:
		return recovery.OperationSupport{Reason: "unsupported_restore_run_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_restore_run_recovery_action"}
	}
}

func (executor *RestoreRunExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported restore run operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported restore run operation recovery: %s", support.Reason)
	}
	if err := validateRestoreRunLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	now := executor.currentTime()
	if now.IsZero() {
		return errors.New("restore run recovery time must be set")
	}
	previewOperationID, err := restoreRunPreviewOperationID(record)
	if err != nil {
		return executor.commitRestoreRunFailed(ctx, record, now, "RESTORE_RUN_VALIDATION_FAILED", "restore run validation failed", nil)
	}
	previewOperation, err := executor.store.GetOperation(ctx, previewOperationID)
	if err != nil {
		return executor.commitRestoreRunIntervention(ctx, record, now, "RESTORE_RUN_PREVIEW_OPERATION_INVALID", "restore run preview operation invalid", nil)
	}
	durablePlan, err := executor.store.GetRestorePlanByPreviewOperation(ctx, previewOperationID)
	if err != nil {
		return executor.commitRestoreRunIntervention(ctx, record, now, "RESTORE_RUN_PLAN_INVALID", "restore run plan invalid", nil)
	}
	if err := validateRestoreRunPreviewAndPlan(record, previewOperation, durablePlan); err != nil {
		return executor.commitRestoreRunIntervention(ctx, record, now, "RESTORE_RUN_PLAN_MISMATCH", "restore run plan mismatch", nil)
	}
	if (record.Phase == operations.OperationPhaseRestoreRunValidate || record.Phase == operations.OperationPhaseRestoreRunWriterFenced) && durablePlan.Status != restoreplan.StatusPending {
		return executor.commitRestoreRunIntervention(ctx, record, now, "RESTORE_RUN_PLAN_NOT_PENDING", "restore run plan is not pending", map[string]any{"restore_plan_id": durablePlan.ID, "restore_plan_status": durablePlan.Status.String()})
	}
	if record.Phase == operations.OperationPhaseRestoreRunConsuming && durablePlan.Status != restoreplan.StatusConsuming {
		return executor.commitRestoreRunIntervention(ctx, record, now, "RESTORE_RUN_PLAN_NOT_CONSUMING", "restore run plan is not consuming", map[string]any{"restore_plan_id": durablePlan.ID, "restore_plan_status": durablePlan.Status.String()})
	}
	if record.Phase == operations.OperationPhaseRestoreRunConsuming {
		return executor.commitRestoreRunIntervention(ctx, record, now, "RESTORE_RUN_CONSUMING_RECOVERY_REQUIRES_OPERATOR", "restore run consuming recovery requires durable applied evidence", map[string]any{"restore_plan_id": durablePlan.ID, "restore_plan_status": durablePlan.Status.String(), "missing_evidence": "restore_run_applied"})
	}

	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return executor.commitRestoreRunFailed(ctx, record, now, "RESTORE_RUN_VALIDATION_FAILED", "restore run validation failed", nil)
	}
	if err := executor.validateMetadata(ctx, record, repo); err != nil {
		return executor.commitRestoreRunFailed(ctx, record, now, "RESTORE_RUN_VALIDATION_FAILED", "restore run validation failed", nil)
	}
	controlRoot, err := executor.controlRoot(repo)
	if err != nil {
		return executor.commitRestoreRunFailed(ctx, record, now, "RESTORE_RUN_VALIDATION_FAILED", "restore run validation failed", nil)
	}

	status, err := executor.jvs.RecoveryStatus(ctx, controlRoot)
	if err != nil {
		return executor.commitRestoreRunIntervention(ctx, record, now, "JVS_RECOVERY_STATUS_FAILED", "jvs recovery status failed", withJVSErrorDetails(nil, err))
	}
	if !restoreRunRecoveryStatusMatchesPendingPlan(status, durablePlan.ID) {
		return executor.commitRestoreRunIntervention(ctx, record, now, "RESTORE_RUN_RECOVERY_STATE_MISMATCH", "restore run recovery state mismatch", restoreRunRecoveryStatusDetails(status, durablePlan.ID))
	}

	working := record
	if record.Phase == operations.OperationPhaseRestoreRunValidate {
		fence := restoreRunWriterFenceForOperation(record, now)
		working = withRestoreRunWriterFencedMarker(record, durablePlan, status, fence.ID)
		updatedFence, updatedOperation, err := executor.store.MarkRestoreRunWriterFencedWithLease(ctx, fence, working.SanitizedForPersistence(), executor.owner, now)
		if err != nil {
			return errors.New("restore run writer fence mark failed")
		}
		working = updatedOperation
		working.SessionFenceID = updatedFence.ID
	}

	if err := executor.checkWriterSessions(ctx, working, now); err != nil {
		return executor.commitRestoreRunFailed(ctx, working, now, "RESTORE_RUN_WRITER_SESSIONS_DENIED", "restore run writer sessions denied", map[string]any{"writer_gate_error_family": restoreRunWriterGateFamily(err)})
	}

	working = withRestoreRunConsumingMarker(working, durablePlan)
	updatedPlan, updatedOperation, err := executor.store.MarkRestoreRunConsumingWithLease(ctx, working.SanitizedForPersistence(), executor.owner, now)
	if err != nil {
		return errors.New("restore run consuming mark failed")
	}
	durablePlan = updatedPlan
	working = updatedOperation

	run, err := executor.jvs.RestoreRun(ctx, controlRoot, durablePlan.ID)
	if err != nil {
		return executor.commitRestoreRunIntervention(ctx, working, now, "JVS_RESTORE_RUN_FAILED", "jvs restore run failed", withJVSErrorDetails(map[string]any{"restore_plan_id": durablePlan.ID}, err))
	}
	if err := validateRestoreRunSummary(run, durablePlan); err != nil {
		return executor.commitRestoreRunIntervention(ctx, working, now, "RESTORE_RUN_RESULT_MISMATCH", "restore run result mismatch", map[string]any{"restore_plan_id": durablePlan.ID})
	}
	doctor, err := executor.jvs.DoctorStrict(ctx, controlRoot)
	if err != nil || doctor.Workspace != "main" || !doctor.Healthy || doctor.RepoID != repo.JVSRepoID {
		return executor.commitRestoreRunIntervention(ctx, working, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", withJVSErrorDetails(map[string]any{"restore_plan_id": durablePlan.ID}, err))
	}
	postStatus, err := executor.jvs.RecoveryStatus(ctx, controlRoot)
	if err != nil {
		return executor.commitRestoreRunIntervention(ctx, working, now, "JVS_RECOVERY_STATUS_FAILED", "jvs recovery status failed", withJVSErrorDetails(map[string]any{"restore_plan_id": durablePlan.ID}, err))
	}
	if !restoreRunRecoveryStatusIdle(postStatus) {
		return executor.commitRestoreRunIntervention(ctx, working, now, "RESTORE_RUN_RECOVERY_STATE_NOT_IDLE", "restore run recovery state is not idle", restoreRunRecoveryStatusDetails(postStatus, durablePlan.ID))
	}
	return executor.commitRestoreRunSuccess(ctx, working, now, run, postStatus)
}

func (executor *RestoreRunExecutor) currentTime() time.Time {
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	return now
}

func (executor *RestoreRunExecutor) validateMetadata(ctx context.Context, record operations.OperationRecord, repo resources.Repo) error {
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
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: restoreRunRepoAccessFences(record, held), Intent: repoaccess.IntentRestoreRun, Mode: repoaccess.ModeReadWrite})
	if !decision.Allowed {
		return errors.New("repo access denied")
	}
	volume, err := executor.store.GetVolume(ctx, repo.VolumeID)
	if err != nil || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return errors.New("invalid volume")
	}
	return nil
}

func restoreRunRepoAccessFences(record operations.OperationRecord, held []fences.Fence) []repoaccess.Fence {
	filtered := make([]fences.Fence, 0, len(held))
	for _, fence := range held {
		if fence.Kind == fences.KindWriterSession && fence.HolderOperationID == record.ID {
			continue
		}
		filtered = append(filtered, fence)
	}
	return savePointRepoAccessFencesFromStore(filtered)
}

func (executor *RestoreRunExecutor) controlRoot(repo resources.Repo) (string, error) {
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

var (
	errRestoreRunActiveWriterSession  = errors.New("restore run active writer session")
	errRestoreRunStaleWriterSession   = errors.New("restore run stale writer session")
	errRestoreRunInvalidWriterSession = errors.New("restore run invalid writer session")
)

type restoreRunWriterGateError struct {
	family sessionstate.ErrorFamily
	err    error
}

func (err restoreRunWriterGateError) Error() string {
	return err.err.Error()
}

func (err restoreRunWriterGateError) Unwrap() error {
	return err.err
}

func (executor *RestoreRunExecutor) checkWriterSessions(ctx context.Context, record operations.OperationRecord, now time.Time) error {
	exports, err := executor.store.ListExportSessionsByRepo(ctx, record.RepoID)
	if err != nil {
		return restoreRunWriterGateError{family: sessionstate.ErrorFamilyInternalError, err: errRestoreRunInvalidWriterSession}
	}
	mounts, err := executor.store.ListWorkloadMountBindingsByRepo(ctx, record.RepoID)
	if err != nil {
		return restoreRunWriterGateError{family: sessionstate.ErrorFamilyInternalError, err: errRestoreRunInvalidWriterSession}
	}
	decision := sessionstate.RestoreRunWriterGate(sessionstate.GateRequest{NamespaceID: record.NamespaceID, RepoID: record.RepoID, Now: now, ExportSessions: exports, Mounts: mounts})
	if decision.Allowed {
		return nil
	}
	switch decision.ErrorFamily {
	case sessionstate.ErrorFamilyActiveWriterSessions:
		return restoreRunWriterGateError{family: decision.ErrorFamily, err: errRestoreRunActiveWriterSession}
	case sessionstate.ErrorFamilyStaleWriterSessionUncertain:
		return restoreRunWriterGateError{family: decision.ErrorFamily, err: errRestoreRunStaleWriterSession}
	default:
		return restoreRunWriterGateError{family: decision.ErrorFamily, err: errRestoreRunInvalidWriterSession}
	}
}

func restoreRunWriterGateFamily(err error) string {
	var gateErr restoreRunWriterGateError
	if errors.As(err, &gateErr) {
		return gateErr.family.String()
	}
	return ""
}

func (executor *RestoreRunExecutor) commitRestoreRunSuccess(ctx context.Context, record operations.OperationRecord, now time.Time, summary jvsrunner.RestoreRunSummary, status jvsrunner.RecoveryStatusSummary) error {
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseRestoreRunCommitted
	operation.ExternalResourceIDs = map[string]string{"restore_plan_id": summary.PlanID}
	if strings.TrimSpace(summary.RestoredSavePointID) != "" {
		operation.ExternalResourceIDs["restored_save_point_id"] = summary.RestoredSavePointID
	}
	operation.JVSJSONOutput = map[string]any{"restore_plan_id": summary.PlanID, "source_save_point_id": summary.SourceSavePointID, "restored_save_point_id": summary.RestoredSavePointID, "workspace": summary.Workspace}
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), map[string]any{"preview_operation_id": restoreRunPreviewOperationIDOrEmpty(operation), "restore_plan_id": summary.PlanID, "source_save_point_id": summary.SourceSavePointID, "restored_save_point_id": summary.RestoredSavePointID, "workspace": summary.Workspace, "post_restore_state": status.RestoreState, "restore_plan_status": restoreplan.StatusConsumed.String()})
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now, audit.OutcomeSucceeded, "restore_run_committed", map[string]any{"repo_id": record.RepoID, "restore_plan_id": summary.PlanID, "source_save_point_id": summary.SourceSavePointID, "restored_save_point_id": summary.RestoredSavePointID})
	if err != nil {
		return err
	}
	if _, _, err := executor.store.CommitRestoreRunSucceededWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore run success commit failed")
	}
	return nil
}

func (executor *RestoreRunExecutor) commitRestoreRunFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := restoreRunFailedOperation(record, now, operations.OperationStateFailed, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_run_failed", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRestoreRunFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore run failure commit failed")
	}
	return nil
}

func (executor *RestoreRunExecutor) commitRestoreRunIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := restoreRunFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	attachJVSErrorDetails(&operation, details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_run_operator_intervention_required", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitRestoreRunFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore run intervention commit failed")
	}
	return fmt.Errorf("%w: restore run operator intervention required", recovery.ErrOperationManualIntervention)
}

func restoreRunFailedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, code, message string) operations.OperationRecord {
	operation := record
	operation.State = state
	switch operation.Phase {
	case operations.OperationPhaseRestoreRunWriterFenced, operations.OperationPhaseRestoreRunConsuming:
	default:
		operation.Phase = operations.OperationPhaseRestoreRunValidate
		operation.SessionFenceID = ""
	}
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID}}
	operation.FinishedAt = &now
	return operation
}

func (executor *RestoreRunExecutor) auditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("restore run audit event id must be set")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: audit.EventTypeRestoreRun, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func validateRestoreRunLeasedRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid restore run recovery record")
	}
	if record.Type != operations.OperationRestoreRun {
		return errors.New("invalid restore run recovery record")
	}
	switch record.Phase {
	case operations.OperationPhaseRestoreRunValidate:
	case operations.OperationPhaseRestoreRunWriterFenced, operations.OperationPhaseRestoreRunConsuming:
		if strings.TrimSpace(record.SessionFenceID) == "" {
			return errors.New("invalid restore run recovery record")
		}
	default:
		return errors.New("invalid restore run recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return errors.New("invalid restore run recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid restore run recovery record")
	}
	return nil
}

func restoreRunPreviewOperationID(record operations.OperationRecord) (string, error) {
	raw, _ := record.InputSummary["preview_operation_id"].(string)
	operationID := strings.TrimSpace(raw)
	if err := pathresolver.ValidateID(pathresolver.OperationID, operationID); err != nil {
		return "", err
	}
	return operationID, nil
}

func restoreRunPreviewOperationIDOrEmpty(record operations.OperationRecord) string {
	id, _ := restoreRunPreviewOperationID(record)
	return id
}

func validateRestoreRunPreviewAndPlan(record, preview operations.OperationRecord, plan restoreplan.Plan) error {
	if preview.Type != operations.OperationRestorePreview || preview.State != operations.OperationStateSucceeded || preview.Phase != operations.OperationPhaseRestorePreviewCommitted {
		return errors.New("preview operation is not a succeeded restore preview")
	}
	if preview.NamespaceID != record.NamespaceID || preview.RepoID != record.RepoID || preview.Resource.Type != "repo" || preview.Resource.ID != record.RepoID {
		return errors.New("preview operation does not match restore run target")
	}
	previewOperationID, err := restoreRunPreviewOperationID(record)
	if err != nil {
		return err
	}
	if plan.PreviewOperationID != previewOperationID || plan.NamespaceID != record.NamespaceID || plan.RepoID != record.RepoID {
		return errors.New("restore plan does not match restore run target")
	}
	return nil
}

func restoreRunWriterFenceForOperation(record operations.OperationRecord, now time.Time) fences.Fence {
	return fences.Fence{ID: "fence_" + record.ID, RepoID: record.RepoID, Kind: fences.KindWriterSession, HolderOperationID: record.ID, Status: fences.StatusActive, ExpiresAt: leaseOrDefault(record, now), CreatedAt: now, UpdatedAt: now}
}

func withRestoreRunWriterFencedMarker(record operations.OperationRecord, plan restoreplan.Plan, status jvsrunner.RecoveryStatusSummary, fenceID string) operations.OperationRecord {
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseRestoreRunWriterFenced
	record.SessionFenceID = fenceID
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": plan.ID}
	record.VerificationResult = mergeStringAnyMap(asStringAnyMap(record.VerificationResult), map[string]any{
		"preview_operation_id":  plan.PreviewOperationID,
		"restore_plan_id":       plan.ID,
		"restore_state":         status.RestoreState,
		"active_plan_matches":   strings.TrimSpace(status.ActivePlanID) == plan.ID,
		"blocking":              status.Blocking,
		"workspace":             status.Workspace,
		"restore_plan_status":   restoreplan.StatusPending.String(),
		"writer_fence_acquired": true,
	})
	return record
}

func withRestoreRunConsumingMarker(record operations.OperationRecord, plan restoreplan.Plan) operations.OperationRecord {
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseRestoreRunConsuming
	record.ExternalResourceIDs = map[string]string{"restore_plan_id": plan.ID}
	record.VerificationResult = mergeStringAnyMap(asStringAnyMap(record.VerificationResult), map[string]any{
		"preview_operation_id": plan.PreviewOperationID,
		"restore_plan_id":      plan.ID,
		"restore_plan_status":  restoreplan.StatusConsuming.String(),
		"writer_gate_allowed":  true,
	})
	return record
}

func restoreRunRecoveryStatusMatchesPendingPlan(status jvsrunner.RecoveryStatusSummary, planID string) bool {
	return status.RestoreState == "pending_restore_preview" &&
		status.Workspace == "main" &&
		strings.TrimSpace(status.ActivePlanID) == planID &&
		status.Blocking
}

func restoreRunRecoveryStatusIdle(status jvsrunner.RecoveryStatusSummary) bool {
	return status.RestoreState == "idle" &&
		status.Workspace == "main" &&
		strings.TrimSpace(status.ActivePlanID) == "" &&
		strings.TrimSpace(status.ActiveRecoveryPlanID) == "" &&
		!status.Blocking
}

func restoreRunRecoveryStatusDetails(status jvsrunner.RecoveryStatusSummary, planID string) map[string]any {
	return map[string]any{
		"restore_state":           status.RestoreState,
		"active_plan_present":     strings.TrimSpace(status.ActivePlanID) != "",
		"active_plan_matches":     strings.TrimSpace(status.ActivePlanID) == planID,
		"active_recovery_present": strings.TrimSpace(status.ActiveRecoveryPlanID) != "",
		"blocking":                status.Blocking,
		"workspace":               status.Workspace,
	}
}

func validateRestoreRunSummary(summary jvsrunner.RestoreRunSummary, plan restoreplan.Plan) error {
	if !restorePreviewSafeOpaqueID(summary.PlanID) || summary.PlanID != plan.ID || summary.Workspace != "main" {
		return errors.New("invalid restore run summary")
	}
	if strings.TrimSpace(summary.SourceSavePointID) != "" && summary.SourceSavePointID != plan.SourceSavePointID {
		return errors.New("invalid restore run source save point")
	}
	if strings.TrimSpace(summary.RestoredSavePointID) != "" && !restorePreviewSafeOpaqueID(summary.RestoredSavePointID) {
		return errors.New("invalid restore run restored save point")
	}
	return nil
}

var _ recovery.OperationExecutor = (*RestoreRunExecutor)(nil)
