package volumebootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/volumeexec"
)

const (
	ResultSchemaVersion = "afscp.volume_bootstrap.v1"

	ActionEnsure Action = "ensure"
	ActionCheck  Action = "check"

	ResultStatusReady         ResultStatus = "ready"
	ResultStatusNotReady      ResultStatus = "not_ready"
	ResultStatusInvalidConfig ResultStatus = "invalid_config"

	FindingVolumeMissing        FindingCode = "volume_missing"
	FindingVolumeNotActive      FindingCode = "volume_not_active"
	FindingVolumeInvalid        FindingCode = "volume_invalid"
	FindingVolumeSpecMismatch   FindingCode = "volume_spec_mismatch"
	FindingStorageUnavailable   FindingCode = "storage_unavailable"
	FindingBootstrapUnavailable FindingCode = "bootstrap_unavailable"
)

var ErrNotReady = errors.New("volume bootstrap not ready")

type Action string

type ResultStatus string

type FindingCode string

type VolumeSpec struct {
	VolumeID       string
	Backend        resources.VolumeBackend
	IsolationClass resources.VolumeIsolationClass
	Status         resources.VolumeStatus
	Capabilities   map[string]any
}

type Result struct {
	SchemaVersion   string       `json:"schema_version"`
	Action          Action       `json:"action"`
	Status          ResultStatus `json:"status"`
	VolumeID        string       `json:"volume_id,omitempty"`
	VolumeStatus    string       `json:"volume_status,omitempty"`
	OperationID     string       `json:"operation_id,omitempty"`
	OperationState  string       `json:"operation_state,omitempty"`
	OperationReused bool         `json:"operation_reused,omitempty"`
	CheckedAt       string       `json:"checked_at,omitempty"`
	Findings        []Finding    `json:"findings,omitempty"`
}

type Finding struct {
	Code     FindingCode `json:"code"`
	Message  string      `json:"message"`
	Severity string      `json:"severity"`
}

type Config struct {
	Store           Store
	Owner           string
	CallerService   string
	AuthorizedActor operations.Actor
	IdempotencyKey  string
	LeaseDuration   time.Duration
	Clock           func() time.Time
	OperationID     func() string
	AuditEventID    func() string
}

type Store interface {
	CreateOrReuseOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error)
	AcquireVolumeEnsureOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error)
	store.VolumeEnsureOperationCommitStore
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
}

type Runner struct {
	store           Store
	owner           string
	callerService   string
	authorizedActor operations.Actor
	idempotencyKey  string
	leaseDuration   time.Duration
	clock           func() time.Time
	operationID     func() string
	auditEventID    func() string
}

func NewRunner(config Config) (*Runner, error) {
	if config.Store == nil {
		return nil, errors.New("volume bootstrap store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("volume bootstrap owner is required")
	}
	config.CallerService = strings.TrimSpace(config.CallerService)
	if config.CallerService == "" {
		return nil, errors.New("volume bootstrap caller service is required")
	}
	config.AuthorizedActor.Type = strings.TrimSpace(config.AuthorizedActor.Type)
	config.AuthorizedActor.ID = strings.TrimSpace(config.AuthorizedActor.ID)
	if config.AuthorizedActor.Type == "" || config.AuthorizedActor.ID == "" {
		return nil, errors.New("volume bootstrap authorized actor is required")
	}
	if config.LeaseDuration <= 0 {
		return nil, errors.New("volume bootstrap lease duration must be positive")
	}
	if config.Clock == nil {
		return nil, errors.New("volume bootstrap clock is required")
	}
	if config.OperationID == nil {
		return nil, errors.New("volume bootstrap operation id generator is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("volume bootstrap audit event id generator is required")
	}
	return &Runner{
		store:           config.Store,
		owner:           config.Owner,
		callerService:   config.CallerService,
		authorizedActor: config.AuthorizedActor,
		idempotencyKey:  strings.TrimSpace(config.IdempotencyKey),
		leaseDuration:   config.LeaseDuration,
		clock:           config.Clock,
		operationID:     config.OperationID,
		auditEventID:    config.AuditEventID,
	}, nil
}

func (runner *Runner) Ensure(ctx context.Context, spec VolumeSpec) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := runner.now()
	if err := ValidateSpec(spec); err != nil {
		return invalidResult(ActionEnsure, spec.VolumeID, now, FindingVolumeInvalid, "volume bootstrap spec is invalid"), err
	}
	canonical := canonicalRequest(spec)
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		return invalidResult(ActionEnsure, spec.VolumeID, now, FindingVolumeInvalid, "volume bootstrap spec is invalid"), err
	}
	scope := operations.NewIdempotencyScope(runner.callerService, "", operations.OperationVolumeEnsure, runner.idempotencyKeyFor(spec.VolumeID))
	operationID := strings.TrimSpace(runner.operationID())
	queued, err := runner.store.CreateOrReuseOperation(ctx, operations.QueuedOperationSpec{
		OperationID:         operationID,
		Scope:               scope,
		RequestHash:         requestHash,
		Phase:               operations.OperationPhaseVolumeEnsureValidate,
		CorrelationID:       "corr_volume_bootstrap_" + spec.VolumeID,
		CallerService:       runner.callerService,
		AuthorizedActor:     runner.authorizedActor,
		Resource:            operations.ResourceRef{Type: "volume", ID: spec.VolumeID},
		InputSummary:        volumeInputSummary(spec),
		CreatedAt:           now,
		ExternalResourceIDs: map[string]string{},
	})
	if err != nil {
		return notReadyResult(ActionEnsure, spec.VolumeID, now, FindingBootstrapUnavailable, "volume ensure operation could not be created"), err
	}
	resultOperation := queued.Operation
	if queued.Reused && queued.Operation.State == operations.OperationStateSucceeded {
		result, checkErr := runner.Check(ctx, spec)
		result.Action = ActionEnsure
		result.OperationID = resultOperation.ID
		result.OperationState = resultOperation.State.String()
		result.OperationReused = true
		return result, checkErr
	}

	leased, err := runner.store.AcquireVolumeEnsureOperationLease(ctx, queued.Operation.ID, operations.LeaseRequest{
		Owner:    runner.owner,
		Duration: runner.leaseDuration,
		Now:      now,
	})
	if err != nil {
		result, checkErr := runner.Check(ctx, spec)
		result.Action = ActionEnsure
		result.OperationID = resultOperation.ID
		result.OperationState = resultOperation.State.String()
		result.OperationReused = queued.Reused
		if checkErr == nil && result.Status == ResultStatusReady {
			return result, nil
		}
		return result, err
	}
	resultOperation = leased
	executor, err := volumeexec.NewExecutor(volumeexec.Config{
		CommitStore:  runner.store,
		Owner:        runner.owner,
		Now:          now,
		AuditEventID: runner.auditEventID,
	})
	if err != nil {
		return notReadyResult(ActionEnsure, spec.VolumeID, now, FindingBootstrapUnavailable, "volume ensure executor could not be configured"), err
	}
	if err := executor.ExecuteOperationRecovery(ctx, leased, recovery.RecoveryPlan{Action: recovery.RecoveryActionClaimable}); err != nil {
		return notReadyResult(ActionEnsure, spec.VolumeID, now, FindingBootstrapUnavailable, "volume ensure operation could not be committed"), err
	}
	result, checkErr := runner.Check(ctx, spec)
	result.Action = ActionEnsure
	result.OperationID = resultOperation.ID
	result.OperationState = operations.OperationStateSucceeded.String()
	result.OperationReused = queued.Reused
	return result, checkErr
}

func (runner *Runner) Check(ctx context.Context, spec VolumeSpec) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := runner.now()
	if err := ValidateSpec(spec); err != nil {
		return invalidResult(ActionCheck, spec.VolumeID, now, FindingVolumeInvalid, "volume bootstrap spec is invalid"), err
	}
	volume, err := runner.store.GetVolume(ctx, spec.VolumeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return notReadyResult(ActionCheck, spec.VolumeID, now, FindingVolumeMissing, "volume metadata is missing"), ErrNotReady
		}
		return notReadyResult(ActionCheck, spec.VolumeID, now, FindingStorageUnavailable, "durable metadata store is unavailable"), err
	}
	if err := volume.Validate(); err != nil {
		return notReadyResult(ActionCheck, spec.VolumeID, now, FindingVolumeInvalid, "volume metadata is invalid"), ErrNotReady
	}
	if volume.ID != spec.VolumeID || volume.Backend != spec.Backend || volume.IsolationClass != spec.IsolationClass || !reflect.DeepEqual(volume.Capabilities, spec.Capabilities) {
		result := notReadyResult(ActionCheck, spec.VolumeID, now, FindingVolumeSpecMismatch, "volume metadata does not match bootstrap spec")
		result.VolumeStatus = string(volume.Status)
		return result, ErrNotReady
	}
	if volume.Status != resources.VolumeStatusActive {
		result := notReadyResult(ActionCheck, spec.VolumeID, now, FindingVolumeNotActive, "volume metadata is not active")
		result.VolumeStatus = string(volume.Status)
		return result, ErrNotReady
	}
	return Result{
		SchemaVersion: ResultSchemaVersion,
		Action:        ActionCheck,
		Status:        ResultStatusReady,
		VolumeID:      volume.ID,
		VolumeStatus:  string(volume.Status),
		CheckedAt:     now.Format(time.RFC3339),
	}, nil
}

func ValidateSpec(spec VolumeSpec) error {
	if strings.TrimSpace(spec.VolumeID) == "" {
		return errors.New("volume_id is required")
	}
	if spec.Status != resources.VolumeStatusActive {
		return errors.New("default volume bootstrap requires active status")
	}
	now := time.Now().UTC()
	volume := resources.Volume{
		ID:             strings.TrimSpace(spec.VolumeID),
		Backend:        spec.Backend,
		IsolationClass: spec.IsolationClass,
		Status:         spec.Status,
		Capabilities:   cloneAnyMap(spec.Capabilities),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := volume.Validate(); err != nil {
		return fmt.Errorf("invalid volume spec: %w", err)
	}
	return nil
}

func (runner *Runner) now() time.Time {
	if runner == nil || runner.clock == nil {
		return time.Now().UTC()
	}
	return runner.clock().UTC()
}

func (runner *Runner) idempotencyKeyFor(volumeID string) string {
	if runner.idempotencyKey != "" {
		return runner.idempotencyKey
	}
	return "default-volume-bootstrap-" + volumeID
}

func invalidResult(action Action, volumeID string, now time.Time, code FindingCode, message string) Result {
	result := notReadyResult(action, volumeID, now, code, message)
	result.Status = ResultStatusInvalidConfig
	return result
}

func notReadyResult(action Action, volumeID string, now time.Time, code FindingCode, message string) Result {
	return Result{
		SchemaVersion: ResultSchemaVersion,
		Action:        action,
		Status:        ResultStatusNotReady,
		VolumeID:      strings.TrimSpace(volumeID),
		CheckedAt:     now.UTC().Format(time.RFC3339),
		Findings:      []Finding{{Code: code, Message: message, Severity: "critical"}},
	}
}

func canonicalRequest(spec VolumeSpec) map[string]any {
	return volumeInputSummary(spec)
}

func volumeInputSummary(spec VolumeSpec) map[string]any {
	return map[string]any{
		"volume_id":       strings.TrimSpace(spec.VolumeID),
		"backend":         string(spec.Backend),
		"isolation_class": string(spec.IsolationClass),
		"status":          string(spec.Status),
		"capabilities":    cloneAnyMap(spec.Capabilities),
	}
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
