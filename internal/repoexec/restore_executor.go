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

type RestoreJVSRunner interface {
	DirectRestore(ctx context.Context, target jvsrunner.DirectTarget, savePointID string) (jvsrunner.DirectRestoreSummary, error)
}

type RestoreConfig struct {
	Store        restoreStore
	JVSRunner    RestoreJVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type restoreStore interface {
	store.RestoreOperationCommitStore
	store.RestoreOperationMetadataReader
}

type RestoreExecutor struct {
	store        restoreStore
	jvs          RestoreJVSRunner
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
	volumeRoots  map[string]string
}

func NewRestoreExecutor(config RestoreConfig) (*RestoreExecutor, error) {
	if config.Store == nil {
		return nil, errors.New("restore recovery store is required")
	}
	if config.JVSRunner == nil {
		return nil, errors.New("restore jvs runner is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("restore recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("restore recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("restore audit event id generator is required")
	}
	roots := map[string]string{}
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("restore volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("restore volume root config is invalid")
		}
		roots[volumeID] = root
	}
	return &RestoreExecutor{store: config.Store, jvs: config.JVSRunner, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID, volumeRoots: roots}, nil
}

func (executor *RestoreExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationRestore {
		return recovery.OperationSupport{Reason: "unsupported_restore_operation"}
	}
	switch record.Phase {
	case operations.OperationPhaseRestoreValidate, operations.OperationPhaseRestoreWriterFenced:
	default:
		return recovery.OperationSupport{Reason: "unsupported_restore_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_restore_recovery_action"}
	}
}

func (executor *RestoreExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported restore operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported restore operation recovery: %s", support.Reason)
	}
	if err := validateRestoreLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	now := executor.currentTime()
	if now.IsZero() {
		return errors.New("restore recovery time must be set")
	}
	savePointID, err := restoreInputSavePointID(record)
	if err != nil {
		return executor.commitRestoreFailed(ctx, record, now, "RESTORE_VALIDATION_FAILED", "restore validation failed", nil)
	}
	repo, err := executor.store.GetRepoInNamespace(ctx, record.NamespaceID, record.RepoID)
	if err != nil {
		return executor.commitRestoreFailed(ctx, record, now, "RESTORE_VALIDATION_FAILED", "restore validation failed", nil)
	}
	if err := executor.validateMetadata(ctx, record, repo); err != nil {
		return executor.commitRestoreFailed(ctx, record, now, "RESTORE_VALIDATION_FAILED", "restore validation failed", nil)
	}
	target, err := executor.directTarget(repo)
	if err != nil {
		return executor.commitRestoreFailed(ctx, record, now, "RESTORE_VALIDATION_FAILED", "restore validation failed", nil)
	}

	working := record
	if record.Phase == operations.OperationPhaseRestoreValidate {
		fence := restoreWriterFenceForOperation(record, now)
		working = withRestoreWriterFencedMarker(record, fence.ID)
		updatedFence, updatedOperation, err := executor.store.MarkRestoreWriterFencedWithLease(ctx, fence, working.SanitizedForPersistence(), executor.owner, now)
		if err != nil {
			return errors.New("restore writer fence mark failed")
		}
		working = updatedOperation
		working.SessionFenceID = updatedFence.ID
	}

	if err := executor.checkWriterSessions(ctx, working, now); err != nil {
		return executor.commitRestoreFailed(ctx, working, now, "RESTORE_WRITER_SESSIONS_DENIED", "restore writer sessions denied", map[string]any{"writer_gate_error_family": restoreWriterGateFamily(err)})
	}

	summary, err := executor.jvs.DirectRestore(ctx, target, savePointID)
	if err != nil {
		return executor.commitRestoreFailed(ctx, working, now, "JVS_RESTORE_FAILED", "jvs restore failed", withJVSErrorDetails(nil, err))
	}
	if err := validateDirectRestoreSummary(summary, savePointID); err != nil {
		return executor.commitRestoreIntervention(ctx, working, now, "RESTORE_RESULT_MISMATCH", "restore result mismatch", nil)
	}
	return executor.commitRestoreSuccess(ctx, working, executor.currentTime(), summary)
}

func (executor *RestoreExecutor) currentTime() time.Time {
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	return now
}

func (executor *RestoreExecutor) validateMetadata(ctx context.Context, record operations.OperationRecord, repo resources.Repo) error {
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
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: restoreRepoAccessFences(record, held), Intent: repoaccess.IntentRestore, Mode: repoaccess.ModeReadWrite})
	if !decision.Allowed {
		return errors.New("repo access denied")
	}
	volume, err := executor.store.GetVolume(ctx, repo.VolumeID)
	if err != nil || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return errors.New("invalid volume")
	}
	return nil
}

func restoreRepoAccessFences(record operations.OperationRecord, held []fences.Fence) []repoaccess.Fence {
	filtered := make([]fences.Fence, 0, len(held))
	for _, fence := range held {
		if fence.Kind == fences.KindWriterSession && fence.HolderOperationID == record.ID {
			continue
		}
		filtered = append(filtered, fence)
	}
	return savePointRepoAccessFencesFromStore(filtered)
}

func (executor *RestoreExecutor) directTarget(repo resources.Repo) (jvsrunner.DirectTarget, error) {
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

var (
	errRestoreActiveWriterSession  = errors.New("restore active writer session")
	errRestoreStaleWriterSession   = errors.New("restore stale writer session")
	errRestoreInvalidWriterSession = errors.New("restore invalid writer session")
)

type restoreWriterGateError struct {
	family sessionstate.ErrorFamily
	err    error
}

func (err restoreWriterGateError) Error() string {
	return err.err.Error()
}

func (err restoreWriterGateError) Unwrap() error {
	return err.err
}

func (executor *RestoreExecutor) checkWriterSessions(ctx context.Context, record operations.OperationRecord, now time.Time) error {
	exports, err := executor.store.ListExportSessionsByRepo(ctx, record.RepoID)
	if err != nil {
		return restoreWriterGateError{family: sessionstate.ErrorFamilyInternalError, err: errRestoreInvalidWriterSession}
	}
	mounts, err := executor.store.ListWorkloadMountBindingsByRepo(ctx, record.RepoID)
	if err != nil {
		return restoreWriterGateError{family: sessionstate.ErrorFamilyInternalError, err: errRestoreInvalidWriterSession}
	}
	decision := sessionstate.RestoreWriterGate(sessionstate.GateRequest{NamespaceID: record.NamespaceID, RepoID: record.RepoID, Now: now, ExportSessions: exports, Mounts: mounts})
	if decision.Allowed {
		return nil
	}
	switch decision.ErrorFamily {
	case sessionstate.ErrorFamilyActiveWriterSessions:
		return restoreWriterGateError{family: decision.ErrorFamily, err: errRestoreActiveWriterSession}
	case sessionstate.ErrorFamilyStaleWriterSessionUncertain:
		return restoreWriterGateError{family: decision.ErrorFamily, err: errRestoreStaleWriterSession}
	default:
		return restoreWriterGateError{family: decision.ErrorFamily, err: errRestoreInvalidWriterSession}
	}
}

func restoreWriterGateFamily(err error) string {
	var gateErr restoreWriterGateError
	if errors.As(err, &gateErr) {
		return gateErr.family.String()
	}
	return ""
}

func (executor *RestoreExecutor) commitRestoreSuccess(ctx context.Context, record operations.OperationRecord, now time.Time, summary jvsrunner.DirectRestoreSummary) error {
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseRestoreCommitted
	operation.ExternalResourceIDs = map[string]string{"restored_save_point_id": summary.RestoredSavePointID}
	jvsOutput := map[string]any{"mode": "afscp_direct_restore", "restored_save_point_id": summary.RestoredSavePointID, "new_head": summary.NewHeadID}
	verification := map[string]any{"direct_restore": true, "restored_save_point_id": summary.RestoredSavePointID, "new_head": summary.NewHeadID, "writer_gate_allowed": true}
	if strings.TrimSpace(summary.PreviousHeadID) != "" {
		jvsOutput["previous_head"] = summary.PreviousHeadID
		verification["previous_head"] = summary.PreviousHeadID
	}
	operation.JVSJSONOutput = jvsOutput
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), verification)
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now, audit.OutcomeSucceeded, "restore_committed", map[string]any{"repo_id": record.RepoID, "restored_save_point_id": summary.RestoredSavePointID})
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, err := executor.store.CommitRestoreSucceededWithLease(commitCtx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore success commit failed")
	}
	return nil
}

func (executor *RestoreExecutor) commitRestoreFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := restoreFailedOperation(record, now, operations.OperationStateFailed, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	attachJVSErrorDetails(&operation, details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_failed", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, err := executor.store.CommitRestoreFailedWithLease(commitCtx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore failure commit failed")
	}
	return nil
}

func (executor *RestoreExecutor) commitRestoreIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message string, details map[string]any) error {
	operation := restoreFailedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	attachJVSErrorDetails(&operation, details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "restore_operator_intervention_required", map[string]any{"repo_id": record.RepoID})
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, err := executor.store.CommitRestoreFailedWithLease(commitCtx, operation.SanitizedForPersistence(), executor.owner, now, event); err != nil {
		return errors.New("restore intervention commit failed")
	}
	return fmt.Errorf("%w: restore operator intervention required", recovery.ErrOperationManualIntervention)
}

func restoreFailedOperation(record operations.OperationRecord, now time.Time, state operations.OperationState, code, message string) operations.OperationRecord {
	operation := record
	operation.State = state
	if operation.Phase != operations.OperationPhaseRestoreWriterFenced {
		operation.Phase = operations.OperationPhaseRestoreValidate
		operation.SessionFenceID = ""
	}
	operation.Error = &operations.OperationError{Code: code, Message: message, Retryable: false, CorrelationID: record.CorrelationID, OperationID: record.ID, Details: map[string]any{"repo_id": record.RepoID}}
	operation.FinishedAt = &now
	return operation
}

func (executor *RestoreExecutor) auditEvent(operation operations.OperationRecord, now time.Time, outcome audit.Outcome, reason string, details map[string]any) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("restore audit event id must be set")
	}
	return audit.NewEvent(audit.Event{EventID: eventID, Type: audit.EventTypeRestore, Time: now, CallerService: operation.CallerService, AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID}, CorrelationID: operation.CorrelationID, OperationID: operation.ID, Resource: audit.Resource{Type: "repo", ID: operation.RepoID, NamespaceID: operation.NamespaceID}, Outcome: outcome, Reason: reason, Details: details}), nil
}

func validateRestoreLeasedRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" || record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("invalid restore recovery record")
	}
	if record.Type != operations.OperationRestore {
		return errors.New("invalid restore recovery record")
	}
	switch record.Phase {
	case operations.OperationPhaseRestoreValidate:
	case operations.OperationPhaseRestoreWriterFenced:
		if strings.TrimSpace(record.SessionFenceID) == "" {
			return errors.New("invalid restore recovery record")
		}
	default:
		return errors.New("invalid restore recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return errors.New("invalid restore recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid restore recovery record")
	}
	return nil
}

func restoreInputSavePointID(record operations.OperationRecord) (string, error) {
	raw, _ := record.InputSummary["save_point_id"].(string)
	savePointID := strings.TrimSpace(raw)
	if err := operations.ValidateSavePointID(savePointID); err != nil {
		return "", err
	}
	return savePointID, nil
}

func restoreWriterFenceForOperation(record operations.OperationRecord, now time.Time) fences.Fence {
	return fences.Fence{ID: "fence_" + record.ID, RepoID: record.RepoID, Kind: fences.KindWriterSession, HolderOperationID: record.ID, Status: fences.StatusActive, ExpiresAt: leaseOrDefault(record, now), CreatedAt: now, UpdatedAt: now}
}

func withRestoreWriterFencedMarker(record operations.OperationRecord, fenceID string) operations.OperationRecord {
	record.State = operations.OperationStateRunning
	record.Phase = operations.OperationPhaseRestoreWriterFenced
	record.SessionFenceID = fenceID
	record.VerificationResult = mergeStringAnyMap(asStringAnyMap(record.VerificationResult), map[string]any{
		"direct_restore":        true,
		"writer_fence_acquired": true,
	})
	return record
}

func validateDirectRestoreSummary(summary jvsrunner.DirectRestoreSummary, savePointID string) error {
	if err := operations.ValidateSavePointID(summary.RestoredSavePointID); err != nil {
		return err
	}
	if summary.RestoredSavePointID != savePointID || summary.NewHeadID != savePointID {
		return errors.New("invalid restore summary")
	}
	if strings.TrimSpace(summary.PreviousHeadID) != "" {
		if err := operations.ValidateSavePointID(summary.PreviousHeadID); err != nil {
			return err
		}
	}
	return nil
}

var _ recovery.OperationExecutor = (*RestoreExecutor)(nil)
