package mountbindingexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

type AuditEventIDGenerator func() string

type Config struct {
	CommitStore  store.WorkloadMountBindingOperationCommitStore
	Owner        string
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
}

type Executor struct {
	commitStore  store.WorkloadMountBindingOperationCommitStore
	owner        string
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
}

func NewExecutor(config Config) (*Executor, error) {
	if config.CommitStore == nil {
		return nil, errors.New("workload mount binding recovery commit store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("workload mount binding recovery owner is required")
	}
	if config.Clock == nil {
		return nil, errors.New("workload mount binding recovery clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("workload mount binding recovery audit event id generator is required")
	}
	return &Executor{commitStore: config.CommitStore, owner: config.Owner, clock: config.Clock, auditEventID: config.AuditEventID}, nil
}

func (executor *Executor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil {
		return recovery.OperationSupport{Reason: "unsupported_workload_mount_binding_operation"}
	}
	if !isMountOperation(record.Type) {
		return recovery.OperationSupport{Reason: "unsupported_workload_mount_binding_operation"}
	}
	if !isValidatePhase(record.Type, record.Phase) {
		return recovery.OperationSupport{Reason: "unsupported_workload_mount_binding_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_workload_mount_binding_recovery_action"}
	}
}

func (executor *Executor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported workload mount binding operation recovery: %s", support.Reason)
	}
	if err := validateLeasedRecord(record, executor.owner); err != nil {
		return err
	}
	now := executor.clock().UTC()
	if now.IsZero() {
		return errors.New("workload mount binding recovery time must be set")
	}
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = committedPhase(record.Type)
	operation.Error = nil
	operation.FinishedAt = &now
	event, err := executor.auditEvent(operation, now)
	if err != nil {
		return err
	}
	mountBindingID := record.MountBindingID
	switch record.Type {
	case operations.OperationMountBindingCreate:
		binding, err := bindingFromSummary(record.InputSummary, record.CreatedAt, now)
		if err != nil {
			return err
		}
		_, _, err = executor.commitStore.CommitWorkloadMountBindingCreateWithLease(ctx, binding, operation.SanitizedForPersistence(), executor.owner, now, event)
		return err
	case operations.OperationMountBindingStatusUpdate:
		status := sessionstate.MountStatus(stringValue(record.InputSummary, "status"))
		reason := stringValue(record.InputSummary, "reason")
		observedAt, err := timeValue(record.InputSummary, "observed_at")
		if err != nil {
			return err
		}
		leaseExpiresAt, err := optionalTimeValue(record.InputSummary, "lease_expires_at")
		if err != nil {
			return err
		}
		_, _, err = executor.commitStore.CommitWorkloadMountBindingStatusWithLease(ctx, mountBindingID, status, reason, observedAt, leaseExpiresAt, operation.SanitizedForPersistence(), executor.owner, now, event)
		return err
	case operations.OperationMountBindingHeartbeat:
		_, _, err = executor.commitStore.CommitWorkloadMountBindingHeartbeatWithLease(ctx, mountBindingID, operation.SanitizedForPersistence(), executor.owner, now, event)
		return err
	case operations.OperationMountBindingRelease:
		_, _, err = executor.commitStore.CommitWorkloadMountBindingReleaseWithLease(ctx, mountBindingID, operation.SanitizedForPersistence(), executor.owner, now, event)
		return err
	case operations.OperationMountBindingRevoke:
		_, _, err = executor.commitStore.CommitWorkloadMountBindingRevokeWithLease(ctx, mountBindingID, operation.SanitizedForPersistence(), executor.owner, now, event)
		return err
	default:
		return errors.New("unsupported workload mount binding operation")
	}
}

func (executor *Executor) auditEvent(record operations.OperationRecord, now time.Time) (audit.Event, error) {
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return audit.Event{}, errors.New("workload mount binding recovery audit event id must be set")
	}
	eventType, ok := audit.EventTypeForOperationType(record.Type.String())
	if !ok {
		return audit.Event{}, errors.New("workload mount binding operation has no audit event type")
	}
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            eventType,
		Time:            now,
		CallerService:   record.CallerService,
		AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource: audit.Resource{
			Type:        "workload_mount_binding",
			ID:          record.MountBindingID,
			NamespaceID: record.NamespaceID,
		},
		Outcome: audit.OutcomeSucceeded,
		Reason:  record.Phase,
		Details: map[string]any{"mount_binding_id": record.MountBindingID, "repo_id": record.RepoID},
	}), nil
}

func validateLeasedRecord(record operations.OperationRecord, owner string) error {
	if record.State != operations.OperationStateRunning || record.LeaseOwner != owner || record.LeaseExpiresAt == nil {
		return errors.New("workload mount binding operation must be lease-owned running")
	}
	if record.Resource.Type != "workload_mount_binding" || record.Resource.ID == "" || record.Resource.ID != record.MountBindingID {
		return errors.New("workload mount binding operation resource mismatch")
	}
	if record.NamespaceID == "" || record.RepoID == "" || record.MountBindingID == "" || record.CallerService == "" || record.CorrelationID == "" {
		return errors.New("workload mount binding operation missing required metadata")
	}
	return nil
}

func bindingFromSummary(summary map[string]any, createdAt, now time.Time) (workloadmount.Binding, error) {
	payload, err := json.Marshal(summary)
	if err != nil {
		return workloadmount.Binding{}, err
	}
	var decoded struct {
		MountBindingID string `json:"mount_binding_id"`
		NamespaceID    string `json:"namespace_id"`
		RepoID         string `json:"repo_id"`
		VolumeID       string `json:"volume_id"`
		MountPath      string `json:"mount_path"`
		ReadOnly       bool   `json:"read_only"`
		LeaseSeconds   int    `json:"lease_seconds"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return workloadmount.Binding{}, err
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	binding := workloadmount.Binding{
		ID:             decoded.MountBindingID,
		NamespaceID:    decoded.NamespaceID,
		RepoID:         decoded.RepoID,
		VolumeID:       decoded.VolumeID,
		MountPath:      decoded.MountPath,
		ReadOnly:       decoded.ReadOnly,
		Status:         sessionstate.MountStatusIssued,
		LeaseSeconds:   decoded.LeaseSeconds,
		LeaseExpiresAt: now.Add(time.Duration(decoded.LeaseSeconds) * time.Second),
		CreatedAt:      createdAt,
		UpdatedAt:      now,
	}
	if err := binding.Validate(); err != nil {
		return workloadmount.Binding{}, err
	}
	return binding, nil
}

func isMountOperation(typ operations.OperationType) bool {
	switch typ {
	case operations.OperationMountBindingCreate, operations.OperationMountBindingStatusUpdate, operations.OperationMountBindingHeartbeat, operations.OperationMountBindingRelease, operations.OperationMountBindingRevoke:
		return true
	default:
		return false
	}
}

func isValidatePhase(typ operations.OperationType, phase string) bool {
	return phase == validatePhase(typ)
}

func validatePhase(typ operations.OperationType) string {
	switch typ {
	case operations.OperationMountBindingCreate:
		return operations.OperationPhaseMountBindingCreateValidate
	case operations.OperationMountBindingStatusUpdate:
		return operations.OperationPhaseMountBindingStatusValidate
	case operations.OperationMountBindingHeartbeat:
		return operations.OperationPhaseMountBindingHeartbeatValidate
	case operations.OperationMountBindingRelease:
		return operations.OperationPhaseMountBindingReleaseValidate
	case operations.OperationMountBindingRevoke:
		return operations.OperationPhaseMountBindingRevokeValidate
	default:
		return ""
	}
}

func committedPhase(typ operations.OperationType) string {
	switch typ {
	case operations.OperationMountBindingCreate:
		return operations.OperationPhaseMountBindingCreateCommitted
	case operations.OperationMountBindingStatusUpdate:
		return operations.OperationPhaseMountBindingStatusCommitted
	case operations.OperationMountBindingHeartbeat:
		return operations.OperationPhaseMountBindingHeartbeatCommitted
	case operations.OperationMountBindingRelease:
		return operations.OperationPhaseMountBindingReleaseCommitted
	case operations.OperationMountBindingRevoke:
		return operations.OperationPhaseMountBindingRevokeCommitted
	default:
		return ""
	}
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func timeValue(values map[string]any, key string) (time.Time, error) {
	value := stringValue(values, key)
	if value == "" {
		return time.Time{}, fmt.Errorf("missing %s", key)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.IsZero() {
		return time.Time{}, fmt.Errorf("invalid %s", key)
	}
	return parsed.UTC(), nil
}

func optionalTimeValue(values map[string]any, key string) (*time.Time, error) {
	value := stringValue(values, key)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.IsZero() {
		return nil, fmt.Errorf("invalid %s", key)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
