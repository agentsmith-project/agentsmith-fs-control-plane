package repoexec

import (
	"context"
	"errors"
	"fmt"
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

type SavePointConfig struct {
	Store        savePointCreateStore
	JVSRunner    SavePointJVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type savePointCreateStore interface {
	store.SavePointCreateOperationCommitStore
	store.SavePointCreateOperationMetadataReader
}

type SavePointJVSRunner interface {
	DirectSave(ctx context.Context, target jvsrunner.DirectTarget, message string) (jvsrunner.DirectSaveSummary, error)
	DirectList(ctx context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectListSummary, error)
}

type SavePointExecutor struct {
	store        savePointCreateStore
	jvs          SavePointJVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

func NewSavePointExecutor(config SavePointConfig) (*SavePointExecutor, error) {
	if config.Store == nil {
		return nil, errors.New("save point recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("save point jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("save point recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("save point recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("save point audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("save point volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("save point volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &SavePointExecutor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *SavePointExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationSavePointCreate {
		return recovery.OperationSupport{Reason: "unsupported_save_point_create_operation"}
	}
	if record.Phase != operations.OperationPhaseSavePointCreateValidate && record.Phase != operations.OperationPhaseSavePointCreatePrepared {
		return recovery.OperationSupport{Reason: "unsupported_save_point_create_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_save_point_create_recovery_action"}
	}
}

func (executor *SavePointExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported save point operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported save point operation recovery: %s", support.Reason)
	}
	if err := validateSavePointLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	message, err := savePointMessage(record)
	if err != nil {
		return err
	}
	if record.Phase != operations.OperationPhaseSavePointCreateValidate || plan.Action != recovery.RecoveryActionClaimable {
		return executor.commitSavePointIntervention(ctx, record, "SAVE_POINT_RECOVERY_UNCERTAIN", "save point recovery requires operator intervention", map[string]any{
			"recovery_action": string(plan.Action),
			"phase":           record.Phase,
		})
	}
	now, err := executor.requireCurrentTime()
	if err != nil {
		return err
	}

	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return executor.commitSavePointFailed(ctx, record, "SAVE_POINT_VALIDATION_FAILED", "save point validation failed")
	}
	if err := executor.validateMetadata(ctx, record, repo); err != nil {
		return executor.commitSavePointFailed(ctx, record, "SAVE_POINT_VALIDATION_FAILED", "save point validation failed")
	}
	writerDrainAllowed, err := executor.checkWriterDrainGate(ctx, record, now)
	if err != nil {
		return err
	}
	if !writerDrainAllowed {
		return nil
	}
	target, err := executor.directTarget(repo)
	if err != nil {
		return executor.commitSavePointIntervention(ctx, record, "SAVE_POINT_VALIDATION_FAILED", "save point validation failed", nil)
	}

	saved, err := executor.jvs.DirectSave(ctx, target, message)
	if err != nil {
		details := withJVSErrorDetails(nil, err)
		if savePointDirectCommandFailureIsTerminal(saved, err) {
			if isJVSRepoBusyError(err) {
				return executor.commitSavePointFailedWithDetails(ctx, record, "JVS_COMMAND_FAILED", "jvs direct save blocked by active repo access", true, details)
			}
			return executor.commitSavePointFailedWithDetails(ctx, record, "JVS_COMMAND_FAILED", "jvs direct save failed", false, details)
		}
		return executor.commitSavePointIntervention(ctx, record, "JVS_COMMAND_FAILED", "jvs direct save failed", details)
	}
	if err := validateSavePointDirectSaveSummary(saved, message); err != nil {
		return executor.commitSavePointIntervention(ctx, record, "SAVE_POINT_MATERIALIZATION_UNCERTAIN", "save point materialization is uncertain", savePointMaterializationDetails(saved))
	}
	visibility, err := executor.verifySavePointVisible(ctx, target, saved)
	if err != nil {
		details := withJVSErrorDetails(savePointVisibilityDetails(visibility), err)
		if errors.Is(err, errSavePointNotVisible) {
			return executor.commitSavePointFailedWithDetails(ctx, record, "SAVE_POINT_NOT_VISIBLE", "save point was not visible after direct save", false, details)
		}
		return executor.commitSavePointIntervention(ctx, record, "SAVE_POINT_VISIBILITY_UNCERTAIN", "save point visibility is uncertain", details)
	}
	return executor.commitSavePointSuccess(ctx, record, savePointFromDirectSaveSummary(saved, message), message, false, false, false, saved.CloneEvidence, visibility)
}

func (executor *SavePointExecutor) checkWriterDrainGate(ctx context.Context, record operations.OperationRecord, now time.Time) (bool, error) {
	exports, err := executor.store.ListExportSessionsByRepo(ctx, record.RepoID)
	if err != nil {
		return false, executor.commitSavePointFailedWithDetails(ctx, record, "SAVE_POINT_WRITER_DRAIN_UNAVAILABLE", "save point writer drain validation failed", true, savePointWriterDrainUnavailableDetails())
	}
	mounts, err := executor.store.ListWorkloadMountBindingsByRepo(ctx, record.RepoID)
	if err != nil {
		return false, executor.commitSavePointFailedWithDetails(ctx, record, "SAVE_POINT_WRITER_DRAIN_UNAVAILABLE", "save point writer drain validation failed", true, savePointWriterDrainUnavailableDetails())
	}
	decision := sessionstate.RestoreWriterGate(sessionstate.GateRequest{
		NamespaceID:    record.NamespaceID,
		RepoID:         record.RepoID,
		Now:            now,
		ExportSessions: exports,
		Mounts:         mounts,
	})
	if decision.Allowed {
		return true, nil
	}
	if decision.ErrorFamily == sessionstate.ErrorFamilyInternalError {
		return false, executor.commitSavePointFailedWithDetails(ctx, record, "SAVE_POINT_WRITER_DRAIN_UNAVAILABLE", "save point writer drain validation failed", true, savePointWriterDrainUnavailableDetails())
	}
	return false, executor.commitSavePointFailedWithDetails(ctx, record, "SAVE_POINT_WRITER_DRAIN_PENDING", "save point writer drain is pending", true, savePointWriterDrainPendingDetails(decision))
}

func (executor *SavePointExecutor) validateMetadata(ctx context.Context, record operations.OperationRecord, repo resources.Repo) error {
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

func (executor *SavePointExecutor) directTarget(repo resources.Repo) (jvsrunner.DirectTarget, error) {
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

func (executor *SavePointExecutor) currentTime() time.Time {
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	return now
}

func (executor *SavePointExecutor) requireCurrentTime() (time.Time, error) {
	now := executor.currentTime()
	if now.IsZero() {
		return time.Time{}, errors.New("save point recovery time must be set")
	}
	return now, nil
}

func (executor *SavePointExecutor) commitSavePointSuccess(ctx context.Context, record operations.OperationRecord, savePoint jvsrunner.SavePointSummary, message string, unsavedChanges, unsavedChangesKnown, adopted bool, cloneEvidence []jvsrunner.CloneEvidence, visibility savePointVisibilityEvidence) error {
	now, err := executor.requireCurrentTime()
	if err != nil {
		return err
	}
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseSavePointCreateCommitted
	operation.ExternalResourceIDs = map[string]string{"save_point_id": savePoint.SavePointID}
	jvsOutput := map[string]any{"save_point_id": savePoint.SavePointID, "message": message, "created_at": savePoint.CreatedAt, "repo_id": record.RepoID, "unsaved_changes_known": unsavedChangesKnown, "history_visible": true, "history_head_id": visibility.HistoryHeadID}
	verification := map[string]any{"save_point_id": savePoint.SavePointID, "created_at": savePoint.CreatedAt, "unsaved_changes_known": unsavedChangesKnown, "adopted": adopted, "history_visible": true, "history_head_id": visibility.HistoryHeadID}
	auditDetails := map[string]any{"repo_id": record.RepoID, "save_point_id": savePoint.SavePointID, "unsaved_changes_known": unsavedChangesKnown, "adopted": adopted, "history_visible": true, "history_head_id": visibility.HistoryHeadID}
	if unsavedChangesKnown {
		jvsOutput["unsaved_changes"] = unsavedChanges
		verification["unsaved_changes"] = unsavedChanges
		auditDetails["unsaved_changes"] = unsavedChanges
	}
	operation.JVSJSONOutput = withCloneEvidenceProjection(jvsOutput, cloneEvidence)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), withCloneEvidenceProjection(verification, cloneEvidence))
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now, audit.OutcomeSucceeded, "save_point_create_committed", withCloneEvidenceProjection(auditDetails, cloneEvidence))
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitSavePointCreateSucceededWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("save point success commit failed")
	}
	return nil
}

func (executor *SavePointExecutor) commitSavePointFailed(ctx context.Context, record operations.OperationRecord, code, message string) error {
	return executor.commitSavePointFailedWithDetails(ctx, record, code, message, false, nil)
}

func (executor *SavePointExecutor) commitSavePointFailedWithDetails(ctx context.Context, record operations.OperationRecord, code, message string, retryable bool, details map[string]any) error {
	now, err := executor.requireCurrentTime()
	if err != nil {
		return err
	}
	operation := savePointFailedOperation(record, now, operations.OperationStateFailed, code, message, retryable)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	if operation.Error != nil {
		operation.Error.Details = mergeStringAnyMap(asStringAnyMap(operation.Error.Details), details)
	}
	attachJVSErrorDetails(&operation, details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "save_point_create_failed", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitSavePointCreateFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("save point failure commit failed")
	}
	return nil
}

func (executor *SavePointExecutor) commitSavePointIntervention(ctx context.Context, record operations.OperationRecord, code, message string, details map[string]any) error {
	now, err := executor.requireCurrentTime()
	if err != nil {
		return err
	}
	operation := savePointFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message, false)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	if operation.Error != nil {
		operation.Error.Details = mergeStringAnyMap(asStringAnyMap(operation.Error.Details), details)
	}
	attachJVSErrorDetails(&operation, details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "save_point_create_operator_intervention_required", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	if _, err := executor.store.CommitSavePointCreateFailedWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("save point intervention commit failed")
	}
	return fmt.Errorf("%w: save point operator intervention required", recovery.ErrOperationManualIntervention)
}

func savePointFailedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, code, message string, retryable bool) operations.OperationRecord {
	operation := record
	operation.State = state
	if operation.Phase != operations.OperationPhaseSavePointCreatePrepared {
		operation.Phase = operations.OperationPhaseSavePointCreateValidate
	}
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: retryable, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID}}
	operation.FinishedAt = &now
	return operation
}

func savePointWriterDrainPendingDetails(decision sessionstate.Decision) map[string]any {
	details := map[string]any{
		"writer_drain_status": "pending",
		"writer_drain_reason": decision.ErrorFamily.String(),
	}
	if strings.TrimSpace(decision.BlockingKind) != "" {
		details["blocking_session_kind"] = decision.BlockingKind
	}
	return details
}

func savePointWriterDrainUnavailableDetails() map[string]any {
	return map[string]any{"writer_drain_status": "unknown"}
}

func isJVSRepoBusyError(err error) bool {
	var commandErr *jvsrunner.CommandError
	return errors.As(err, &commandErr) && commandErr.Code == "E_REPO_BUSY"
}

func savePointDirectCommandFailureIsTerminal(summary jvsrunner.DirectSaveSummary, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var commandErr *jvsrunner.CommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	return !savePointDirectSaveHasSideEffectEvidence(summary)
}

func savePointDirectSaveHasSideEffectEvidence(summary jvsrunner.DirectSaveSummary) bool {
	return strings.TrimSpace(summary.SavePointID) != "" ||
		strings.TrimSpace(summary.HistoryHeadID) != "" ||
		strings.TrimSpace(summary.Message) != "" ||
		strings.TrimSpace(summary.Purpose) != "" ||
		strings.TrimSpace(summary.CreatedAt) != "" ||
		len(summary.CloneEvidence) > 0
}

var (
	errSavePointNotVisible             = errors.New("save point was not visible in history")
	errInvalidSavePointMaterialization = errors.New("invalid save point materialization")
)

type savePointVisibilityEvidence struct {
	SavePointID    string
	HistoryHeadID  string
	HistoryHead    bool
	MessageMatch   bool
	CreatedAtMatch bool
	PurposeMatch   bool
	Visible        bool
}

func validateSavePointDirectSaveSummary(summary jvsrunner.DirectSaveSummary, message string) error {
	savePointID := strings.TrimSpace(summary.SavePointID)
	if err := operations.ValidateSavePointID(savePointID); err != nil {
		return errInvalidSavePointMaterialization
	}
	if strings.TrimSpace(summary.HistoryHeadID) != savePointID {
		return errInvalidSavePointMaterialization
	}
	if summary.Message != message || strings.TrimSpace(summary.CreatedAt) == "" || savePointCreateInternalPurpose(summary.Purpose) {
		return errInvalidSavePointMaterialization
	}
	return nil
}

func (executor *SavePointExecutor) verifySavePointVisible(ctx context.Context, target jvsrunner.DirectTarget, saved jvsrunner.DirectSaveSummary) (savePointVisibilityEvidence, error) {
	evidence := savePointVisibilityEvidence{SavePointID: strings.TrimSpace(saved.SavePointID)}
	history, err := executor.jvs.DirectList(ctx, target)
	if err != nil {
		return evidence, err
	}
	evidence.HistoryHeadID = strings.TrimSpace(history.HistoryHeadID)
	for _, savePoint := range history.SavePoints {
		if strings.TrimSpace(savePoint.SavePointID) != evidence.SavePointID {
			continue
		}
		evidence.HistoryHead = savePoint.HistoryHead
		evidence.MessageMatch = savePoint.Message == saved.Message
		evidence.CreatedAtMatch = strings.TrimSpace(savePoint.CreatedAt) == strings.TrimSpace(saved.CreatedAt)
		evidence.PurposeMatch = strings.TrimSpace(savePoint.Purpose) == strings.TrimSpace(saved.Purpose)
		evidence.Visible = evidence.HistoryHeadID == evidence.SavePointID &&
			savePoint.HistoryHead &&
			evidence.MessageMatch &&
			evidence.CreatedAtMatch &&
			evidence.PurposeMatch &&
			!savePointCreateInternalPurpose(savePoint.Purpose)
		if evidence.Visible {
			return evidence, nil
		}
		return evidence, errSavePointNotVisible
	}
	return evidence, errSavePointNotVisible
}

func savePointMaterializationDetails(summary jvsrunner.DirectSaveSummary) map[string]any {
	details := map[string]any{
		"save_point_materialized": false,
	}
	if savePointID := strings.TrimSpace(summary.SavePointID); savePointID != "" {
		details["save_point_id"] = savePointID
	}
	if headID := strings.TrimSpace(summary.HistoryHeadID); headID != "" {
		details["history_head_id"] = headID
	}
	return details
}

func savePointVisibilityDetails(evidence savePointVisibilityEvidence) map[string]any {
	details := map[string]any{
		"history_visible": false,
	}
	if strings.TrimSpace(evidence.SavePointID) != "" {
		details["save_point_id"] = evidence.SavePointID
	}
	if strings.TrimSpace(evidence.HistoryHeadID) != "" {
		details["history_head_id"] = evidence.HistoryHeadID
	}
	details["history_head"] = evidence.HistoryHead
	details["message_match"] = evidence.MessageMatch
	details["created_at_match"] = evidence.CreatedAtMatch
	details["purpose_match"] = evidence.PurposeMatch
	return details
}

func savePointCreateInternalPurpose(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case "template_source", "task_template_source":
		return true
	default:
		return false
	}
}

func (executor *SavePointExecutor) auditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("save point audit event id must be set")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: audit.EventTypeSavePointCreate, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func validateSavePointLeasedRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid save point recovery record")
	}
	if record.Type != operations.OperationSavePointCreate || (record.Phase != operations.OperationPhaseSavePointCreateValidate && record.Phase != operations.OperationPhaseSavePointCreatePrepared) {
		return errors.New("invalid save point recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return errors.New("invalid save point recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid save point recovery record")
	}
	return nil
}

func savePointMessage(record operations.OperationRecord) (string, error) {
	raw, _ := record.InputSummary["message"].(string)
	message, err := operations.NormalizeSavePointMessage(raw)
	if err != nil {
		return "", errors.New("invalid save point message")
	}
	return message, nil
}

func savePointFromDirectSaveSummary(summary jvsrunner.DirectSaveSummary, fallbackMessage string) jvsrunner.SavePointSummary {
	message := summary.Message
	if strings.TrimSpace(message) == "" {
		message = fallbackMessage
	}
	return jvsrunner.SavePointSummary{SavePointID: summary.SavePointID, Message: message, CreatedAt: summary.CreatedAt}
}

func savePointRepoAccessFencesFromStore(existing []fences.Fence) []repoaccess.Fence {
	out := make([]repoaccess.Fence, len(existing))
	for idx, fence := range existing {
		out[idx] = repoaccess.Fence{
			ID:                fence.ID,
			RepoID:            fence.RepoID,
			Kind:              repoaccess.FenceKind(fence.Kind.String()),
			HolderOperationID: fence.HolderOperationID,
			Status:            repoaccess.FenceStatus(fence.Status.String()),
			ExpiresAt:         fence.ExpiresAt,
			ReleasedAt:        fence.ReleasedAt,
			RecoveredAt:       fence.RecoveredAt,
			CreatedAt:         fence.CreatedAt,
			UpdatedAt:         fence.UpdatedAt,
		}
	}
	return out
}

func asStringAnyMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok && typed != nil {
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	}
	return map[string]any{}
}

func mergeStringAnyMap(base map[string]any, extra map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}

var _ recovery.OperationExecutor = (*SavePointExecutor)(nil)
