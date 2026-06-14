package repoexec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
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

const durableCommitTimeout = 10 * time.Second

const repoCreateMetadataReadPendingCode = "REPO_CREATE_METADATA_READ_PENDING"

type AuditEventIDGenerator func() string

type JVSRunner interface {
	DirectSave(ctx context.Context, target jvsrunner.DirectTarget, message string) (jvsrunner.DirectSaveSummary, error)
	DirectList(ctx context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectListSummary, error)
	DirectRestore(ctx context.Context, target jvsrunner.DirectTarget, savePointID string) (jvsrunner.DirectRestoreSummary, error)
	DirectStatus(ctx context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectStatusSummary, error)
	DirectDoctor(ctx context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectDoctorSummary, error)
}

type RepoCreateJVSRunner interface {
	Init(ctx context.Context, payloadRoot, controlRoot string) (jvsrunner.InitSummary, error)
	DirectDoctor(ctx context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectDoctorSummary, error)
}

type Config struct {
	Store        repoCreateStore
	JVSRunner    RepoCreateJVSRunner
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
	VolumeRoots  map[string]string
}

type repoCreateStore interface {
	store.RepoCreateOperationCommitStore
	store.RepoCreateOperationProgressStore
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
	CreateRepoFence(ctx context.Context, fence fences.Fence) error
}

type Executor struct {
	store        repoCreateStore
	jvs          RepoCreateJVSRunner
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
		canonicalVolumeID, ok := canonicalVolumeRootID(volumeID)
		if !ok {
			return nil, errors.New("repo create volume root config is invalid")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, errors.New("repo create volume root config is invalid")
		}
		if _, exists := roots[canonicalVolumeID]; exists {
			return nil, errors.New("repo create volume root config is invalid")
		}
		roots[canonicalVolumeID] = root
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
		return executor.commitMetadataFailure(ctx, record, now, err, existingHeldFence, hasSameOpHeldFence)
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
	directTarget := jvsrunner.DirectTarget{ControlRoot: roots.ControlRootPath, Home: roots.PayloadRootPath}
	if adoptionAllowed {
		doctor, err := executor.jvs.DirectDoctor(ctx, directTarget)
		if err != nil || !directDoctorAllowsMutation(doctor) {
			return executor.commitIntervention(ctx, record, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", fenceID, withJVSErrorDetails(nil, err))
		}
		jvsRepoID = doctor.RepoID
		adopted = true
	} else {
		initSummary, err := executor.jvs.Init(ctx, roots.PayloadRootPath, roots.ControlRootPath)
		if err != nil {
			return executor.commitIntervention(ctx, record, now, "JVS_COMMAND_FAILED", "jvs init failed", fenceID, withJVSErrorDetails(nil, err))
		}
		doctor, err := executor.jvs.DirectDoctor(ctx, directTarget)
		if err != nil || !directDoctorAllowsMutation(doctor) {
			return executor.commitIntervention(ctx, record, now, "JVS_DOCTOR_FAILED", "jvs doctor failed", fenceID, withJVSErrorDetails(map[string]any{"repo_id": initSummary.RepoID, "workspace": initSummary.Workspace}, err))
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
	operation.VerificationResult = map[string]any{"repo_id": jvsRepoID, "workspace": "main", "healthy": true, "adopted": adopted, "volume_id": repo.VolumeID}
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now, audit.OutcomeSucceeded, "repo_create_committed", map[string]any{"repo_id": record.RepoID, "volume_id": repo.VolumeID, "jvs_repo_id": jvsRepoID, "adopted": adopted})
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, _, err := executor.store.CommitRepoCreateSucceededWithLease(commitCtx, repo, operation.SanitizedForPersistence(), executor.owner, now, event, fenceID); err != nil {
		return repoCreateCommitError("repo create success commit failed", err)
	}
	return nil
}

type repoMetadata struct {
	namespace resources.Namespace
	binding   resources.NamespaceVolumeBinding
	volume    resources.Volume
}

type repoCreateMetadataError struct {
	reason    string
	stage     string
	retryable bool
	details   map[string]any
}

func terminalMetadataError(reason, stage string, details map[string]any) repoCreateMetadataError {
	return repoCreateMetadataError{reason: reason, stage: stage, details: details}
}

func retryableMetadataError(reason, stage string, details map[string]any) repoCreateMetadataError {
	return repoCreateMetadataError{reason: reason, stage: stage, retryable: true, details: details}
}

func (err repoCreateMetadataError) Error() string {
	if err.retryable {
		return "repo create metadata read unavailable"
	}
	return "repo create metadata validation failed"
}

func (err repoCreateMetadataError) operationDetails(record operations.OperationRecord) map[string]any {
	details := map[string]any{"repo_id": record.RepoID}
	if err.retryable {
		details["retry_reason"] = err.reason
	} else {
		details["validation_reason"] = err.reason
	}
	details["metadata_stage"] = err.stage
	for key, value := range err.details {
		switch key {
		case "volume_id":
			if strings.TrimSpace(fmt.Sprint(value)) == "" {
				continue
			}
			details[key] = value
		case "configured_volume_root_ids":
			if ids, ok := safeConfiguredVolumeRootIDsFromValue(value); ok {
				details[key] = safeConfiguredVolumeRootIDsFromSlice(ids)
			}
		}
	}
	return details
}

func (executor *Executor) commitMetadataFailure(ctx context.Context, record operations.OperationRecord, now time.Time, cause error, existingHeldFence fences.Fence, hasSameOpHeldFence bool) error {
	metadataErr := normalizedMetadataFailure(cause)
	details := metadataErr.operationDetails(record)
	if metadataErr.retryable {
		return executor.markMetadataReadPending(ctx, record, now, details)
	}
	if hasSameOpHeldFence {
		return executor.commitIntervention(ctx, record, now, "REPO_CREATE_VALIDATION_FAILED_WITH_FENCE", "repo create validation failed with held fence", existingHeldFence.ID, details)
	}
	return executor.commitFailedWithDetails(ctx, record, now, "REPO_CREATE_VALIDATION_FAILED", "repo create validation failed", "", details)
}

func normalizedMetadataFailure(cause error) repoCreateMetadataError {
	var metadataErr repoCreateMetadataError
	if !errors.As(cause, &metadataErr) {
		return retryableMetadataError("metadata_read_unavailable", "metadata", nil)
	}
	metadataErr.reason = strings.TrimSpace(metadataErr.reason)
	metadataErr.stage = strings.TrimSpace(metadataErr.stage)
	metadataErr.details = safeMetadataDetails(metadataErr.details)
	if metadataErr.retryable {
		if !safeMetadataToken(metadataErr.reason) {
			metadataErr.reason = "metadata_read_unavailable"
		}
		if !safeMetadataToken(metadataErr.stage) {
			metadataErr.stage = "metadata"
		}
		return metadataErr
	}
	if knownTerminalMetadataFailure(metadataErr.reason, metadataErr.stage) {
		return metadataErr
	}
	if safeMetadataToken(metadataErr.stage) {
		return retryableMetadataError("metadata_read_unavailable", metadataErr.stage, metadataErr.details)
	}
	return retryableMetadataError("metadata_read_unavailable", "metadata", metadataErr.details)
}

func knownTerminalMetadataFailure(reason, stage string) bool {
	switch reason {
	case "namespace_missing", "namespace_inactive":
		return stage == "namespace"
	case "binding_missing", "binding_inactive":
		return stage == "namespace_volume_binding"
	case "volume_missing", "volume_inactive", "volume_jvs_capability_disabled":
		return stage == "volume"
	case "volume_root_config_missing", "volume_root_config_invalid":
		return stage == "volume_root"
	default:
		return false
	}
}

func safeMetadataDetails(details map[string]any) map[string]any {
	out := map[string]any{}
	if details == nil {
		return out
	}
	if volumeID, ok := details["volume_id"].(string); ok && strings.TrimSpace(volumeID) != "" {
		out["volume_id"] = strings.TrimSpace(volumeID)
	}
	if ids, ok := safeConfiguredVolumeRootIDsFromValue(details["configured_volume_root_ids"]); ok {
		safeIDs := safeConfiguredVolumeRootIDsFromSlice(ids)
		out["configured_volume_root_ids"] = safeIDs
	}
	return out
}

func safeConfiguredVolumeRootIDs(roots map[string]string) []string {
	ids := make([]string, 0, len(roots))
	for volumeID := range roots {
		ids = append(ids, volumeID)
	}
	return safeConfiguredVolumeRootIDsFromSlice(ids)
}

func configuredVolumeRoot(roots map[string]string, volumeID string) (string, bool) {
	canonicalVolumeID, ok := canonicalVolumeRootID(volumeID)
	if !ok {
		return "", false
	}
	if root, ok := roots[canonicalVolumeID]; ok {
		return root, true
	}
	for configuredVolumeID, root := range roots {
		if canonicalConfiguredVolumeID, ok := canonicalVolumeRootID(configuredVolumeID); ok && canonicalConfiguredVolumeID == canonicalVolumeID {
			return root, true
		}
	}
	return "", false
}

func canonicalVolumeRootID(volumeID string) (string, bool) {
	canonicalVolumeID := strings.TrimSpace(volumeID)
	if err := pathresolver.ValidateID(pathresolver.VolumeID, canonicalVolumeID); err != nil {
		return "", false
	}
	return canonicalVolumeID, true
}

func safeConfiguredVolumeRootIDsFromValue(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		ids := make([]string, 0, len(typed))
		for _, item := range typed {
			id, ok := item.(string)
			if !ok {
				return nil, false
			}
			ids = append(ids, id)
		}
		return ids, true
	default:
		return nil, false
	}
}

func safeConfiguredVolumeRootIDsFromSlice(ids []string) []string {
	safeIDs := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, volumeID := range ids {
		canonicalVolumeID, ok := canonicalVolumeRootID(volumeID)
		if !ok {
			continue
		}
		if _, exists := seen[canonicalVolumeID]; exists {
			continue
		}
		seen[canonicalVolumeID] = struct{}{}
		safeIDs = append(safeIDs, canonicalVolumeID)
	}
	sort.Strings(safeIDs)
	return safeIDs
}

func safeMetadataToken(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char == '_' || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func (executor *Executor) loadMetadata(ctx context.Context, record operations.OperationRecord, now time.Time) (repoMetadata, pathresolver.RepoRootPaths, error) {
	namespace, err := executor.store.GetNamespace(ctx, record.NamespaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("namespace_missing", "namespace", nil)
		}
		return repoMetadata{}, pathresolver.RepoRootPaths{}, retryableMetadataError("namespace_read_unavailable", "namespace", nil)
	}
	if namespace.Status != resources.NamespaceStatusActive {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("namespace_inactive", "namespace", nil)
	}
	binding, err := executor.store.GetNamespaceVolumeBinding(ctx, record.NamespaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("binding_missing", "namespace_volume_binding", nil)
		}
		return repoMetadata{}, pathresolver.RepoRootPaths{}, retryableMetadataError("binding_read_unavailable", "namespace_volume_binding", nil)
	}
	if binding.Status != resources.NamespaceStatusActive {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("binding_inactive", "namespace_volume_binding", map[string]any{"volume_id": binding.DefaultVolumeID})
	}
	volume, err := executor.store.GetVolume(ctx, binding.DefaultVolumeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("volume_missing", "volume", map[string]any{"volume_id": binding.DefaultVolumeID})
		}
		return repoMetadata{}, pathresolver.RepoRootPaths{}, retryableMetadataError("volume_read_unavailable", "volume", map[string]any{"volume_id": binding.DefaultVolumeID})
	}
	if volume.Status != resources.VolumeStatusActive {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("volume_inactive", "volume", map[string]any{"volume_id": volume.ID})
	}
	if volume.Capabilities["jvs_external_control_root"] != true {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("volume_jvs_capability_disabled", "volume", map[string]any{"volume_id": volume.ID})
	}
	root, ok := configuredVolumeRoot(executor.volumeRoots, volume.ID)
	if !ok {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("volume_root_config_missing", "volume_root", map[string]any{"volume_id": volume.ID, "configured_volume_root_ids": safeConfiguredVolumeRootIDs(executor.volumeRoots)})
	}
	roots, err := pathresolver.ResolveRepoRootPaths(root, record.NamespaceID, record.RepoID)
	if err != nil {
		return repoMetadata{}, pathresolver.RepoRootPaths{}, terminalMetadataError("volume_root_config_invalid", "volume_root", map[string]any{"volume_id": volume.ID})
	}
	return repoMetadata{namespace: namespace, binding: binding, volume: volume}, roots, nil
}

func (executor *Executor) commitFailed(ctx context.Context, record operations.OperationRecord, now time.Time, code, message, releaseFenceID string) error {
	return executor.commitFailedWithDetails(ctx, record, now, code, message, releaseFenceID, nil)
}

func (executor *Executor) commitFailedWithDetails(ctx context.Context, record operations.OperationRecord, now time.Time, code, message, releaseFenceID string, details map[string]any) error {
	operation := failedOperation(record, now, operations.OperationStateFailed, code, message)
	operation.VerificationResult = mergeStringAnyMap(asStringAnyMap(operation.VerificationResult), details)
	if operation.Error != nil {
		operation.Error.Details = mergeStringAnyMap(asStringAnyMap(operation.Error.Details), details)
	}
	eventDetails := mergeStringAnyMap(map[string]any{"repo_id": record.RepoID}, details)
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "repo_create_failed", eventDetails)
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, err := executor.store.CommitRepoCreateFailedWithLease(commitCtx, operation.SanitizedForPersistence(), executor.owner, now, event, releaseFenceID); err != nil {
		return repoCreateCommitError("repo create failure commit failed", err)
	}
	return nil
}

func (executor *Executor) markMetadataReadPending(ctx context.Context, record operations.OperationRecord, now time.Time, details map[string]any) error {
	operation := record
	operation.State = operations.OperationStateRunning
	operation.Phase = operations.OperationPhaseRepoCreateValidate
	operation.ExternalResourceIDs = map[string]string{}
	operation.JVSJSONOutput = nil
	operation.VerificationResult = details
	operation.FinishedAt = nil
	operation.Error = &operations.OperationError{
		Code:          repoCreateMetadataReadPendingCode,
		Message:       "repo create metadata read is pending",
		Retryable:     true,
		CorrelationID: record.CorrelationID,
		OperationID:   record.ID,
		Details:       details,
	}
	if _, err := executor.store.MarkRepoCreateMetadataReadPendingWithLease(ctx, operation.SanitizedForPersistence(), executor.owner, now); err != nil {
		return repoCreateCommitError("repo create metadata read pending update failed", err)
	}
	return nil
}

func (executor *Executor) commitIntervention(ctx context.Context, record operations.OperationRecord, now time.Time, code, message, fenceID string, details map[string]any) error {
	operation := failedOperation(record, now, operations.OperationStateOperatorInterventionRequired, code, message)
	operation.VerificationResult = details
	if operation.Error != nil {
		operation.Error.Details = mergeStringAnyMap(asStringAnyMap(operation.Error.Details), details)
	}
	attachJVSErrorDetails(&operation, details)
	eventDetails := map[string]any{"repo_id": record.RepoID}
	if code == "REPO_CREATE_VALIDATION_FAILED_WITH_FENCE" {
		eventDetails = mergeStringAnyMap(eventDetails, details)
		eventDetails["repo_id"] = record.RepoID
	}
	event, err := executor.auditEvent(operation, now, audit.OutcomeFailed, "repo_create_operator_intervention_required", eventDetails)
	if err != nil {
		return err
	}
	commitCtx, cancel := durableCommitContext(ctx)
	defer cancel()
	if _, err := executor.store.CommitRepoCreateFailedWithLease(commitCtx, operation.SanitizedForPersistence(), executor.owner, now, event, ""); err != nil {
		return repoCreateCommitError("repo create intervention commit failed", err)
	}
	return nil
}

type commitError struct {
	message string
	cause   error
}

func repoCreateCommitError(message string, cause error) error {
	return commitError{message: message, cause: cause}
}

func (err commitError) Error() string {
	return err.message
}

func (err commitError) Unwrap() error {
	return err.cause
}

func durableCommitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), durableCommitTimeout)
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
