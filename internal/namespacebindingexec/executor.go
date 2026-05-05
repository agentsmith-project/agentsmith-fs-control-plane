package namespacebindingexec

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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

const (
	unsupportedOperationReason = "unsupported_namespace_volume_binding_put_operation"
	unsupportedPhaseReason     = "unsupported_namespace_volume_binding_put_phase"
	unsupportedActionReason    = "unsupported_namespace_volume_binding_put_recovery_action"
)

var (
	ErrUnsupportedOperationRecovery = errors.New("unsupported namespace volume binding put operation recovery")
	ErrInvalidRecoveryPlan          = errors.New("invalid namespace volume binding put recovery plan")
	ErrInvalidRecoveryRecord        = errors.New("invalid namespace volume binding put recovery record")
)

type AuditEventIDGenerator func() string

type Config struct {
	CommitStore  store.NamespaceVolumeBindingOperationCommitStore
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
}

type Executor struct {
	commitStore  store.NamespaceVolumeBindingOperationCommitStore
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
}

var _ recovery.OperationExecutor = (*Executor)(nil)

func NewExecutor(config Config) (*Executor, error) {
	if config.CommitStore == nil {
		return nil, errors.New("namespace volume binding put recovery commit store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("namespace volume binding put recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("namespace volume binding put recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("namespace volume binding put recovery audit event id generator is required")
	}
	return &Executor{
		commitStore:  config.CommitStore,
		owner:        config.Owner,
		now:          config.Now,
		clock:        config.Clock,
		auditEventID: config.AuditEventID,
	}, nil
}

func (executor *Executor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil {
		return recovery.OperationSupport{Reason: unsupportedOperationReason}
	}
	if record.Type != operations.OperationNamespaceVolumeBindingPut {
		return recovery.OperationSupport{Reason: unsupportedOperationReason}
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseNamespaceVolumeBindingPutValidate {
		return recovery.OperationSupport{Reason: unsupportedPhaseReason}
	}
	if !supportedPlanAction(plan.Action) {
		return recovery.OperationSupport{Reason: unsupportedActionReason}
	}
	return recovery.OperationSupport{Supported: true}
}

func (executor *Executor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return ErrUnsupportedOperationRecovery
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("%w: %s", ErrUnsupportedOperationRecovery, support.Reason)
	}
	if err := validatePlan(plan); err != nil {
		return err
	}
	if err := validateLeasedRunningRecord(record, executor.owner); err != nil {
		return err
	}

	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	if now.IsZero() {
		return errors.New("namespace volume binding put recovery time must be set")
	}

	binding, operation, event, err := executor.commitRequest(record, now)
	if err != nil {
		return err
	}
	_, _, err = executor.commitStore.CommitNamespaceVolumeBindingPutWithLease(ctx, binding, operation.SanitizedForPersistence(), executor.owner, now, event)
	return err
}

func validatePlan(plan recovery.RecoveryPlan) error {
	if supportedPlanAction(plan.Action) {
		return nil
	}
	return fmt.Errorf("%w: action %q", ErrInvalidRecoveryPlan, plan.Action)
}

func supportedPlanAction(action recovery.RecoveryAction) bool {
	switch action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return true
	default:
		return false
	}
}

func validateLeasedRunningRecord(record operations.OperationRecord, owner string) error {
	if strings.TrimSpace(record.ID) == "" {
		return fmt.Errorf("%w: missing operation id", ErrInvalidRecoveryRecord)
	}
	if record.State != operations.OperationStateRunning {
		return fmt.Errorf("%w: operation must be leased running", ErrInvalidRecoveryRecord)
	}
	if strings.TrimSpace(record.LeaseOwner) == "" || record.LeaseOwner != owner {
		return fmt.Errorf("%w: operation lease owner must match executor owner", ErrInvalidRecoveryRecord)
	}
	if record.LeaseExpiresAt == nil {
		return fmt.Errorf("%w: operation lease expiry is required", ErrInvalidRecoveryRecord)
	}
	if strings.TrimSpace(record.NamespaceID) == "" {
		return fmt.Errorf("%w: missing namespace id", ErrInvalidRecoveryRecord)
	}
	if record.Resource.Type != "namespace_volume_binding" {
		return fmt.Errorf("%w: operation resource must be namespace_volume_binding", ErrInvalidRecoveryRecord)
	}
	if strings.TrimSpace(record.Resource.ID) == "" || record.Resource.ID != record.NamespaceID {
		return fmt.Errorf("%w: operation resource id must match namespace id", ErrInvalidRecoveryRecord)
	}
	if strings.TrimSpace(record.CallerService) == "" {
		return fmt.Errorf("%w: missing caller service", ErrInvalidRecoveryRecord)
	}
	if strings.TrimSpace(record.CorrelationID) == "" {
		return fmt.Errorf("%w: missing correlation id", ErrInvalidRecoveryRecord)
	}
	if strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return fmt.Errorf("%w: missing authorized actor", ErrInvalidRecoveryRecord)
	}
	return nil
}

func (executor *Executor) commitRequest(record operations.OperationRecord, now time.Time) (resources.NamespaceVolumeBinding, operations.OperationRecord, audit.Event, error) {
	binding, err := bindingFromInputSummary(record.InputSummary, record.NamespaceID, record.CreatedAt, now)
	if err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, audit.Event{}, err
	}

	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseNamespaceVolumeBindingPutCommitted
	operation.Error = nil
	operation.FinishedAt = &now

	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, audit.Event{}, errors.New("namespace volume binding put recovery audit event id must be set")
	}
	event := audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeNamespaceVolumeBindingPut,
		Time:            now,
		CallerService:   operation.CallerService,
		AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID},
		CorrelationID:   operation.CorrelationID,
		OperationID:     operation.ID,
		Resource: audit.Resource{
			Type:        "namespace_volume_binding",
			ID:          operation.NamespaceID,
			NamespaceID: operation.NamespaceID,
		},
		Outcome: audit.OutcomeSucceeded,
		Reason:  "namespace_volume_binding_put_committed",
		Details: map[string]any{"namespace_id": operation.NamespaceID, "default_volume_id": binding.DefaultVolumeID},
	})
	return binding, operation, event, nil
}

type bindingSummary struct {
	NamespaceID       string                    `json:"namespace_id"`
	DefaultVolumeID   string                    `json:"default_volume_id"`
	AllowedCallers    []resources.AllowedCaller `json:"allowed_callers"`
	QuotaBytesDefault int64                     `json:"quota_bytes_default"`
	ExportPolicy      map[string]any            `json:"export_policy"`
	LifecyclePolicy   map[string]any            `json:"lifecycle_policy"`
	MountPolicy       map[string]any            `json:"mount_policy"`
	TemplatePolicy    map[string]any            `json:"template_policy"`
	Status            resources.NamespaceStatus `json:"status"`
}

func bindingFromInputSummary(summary map[string]any, namespaceID string, createdAt, now time.Time) (resources.NamespaceVolumeBinding, error) {
	if summary == nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("%w: missing binding input summary", ErrInvalidRecoveryRecord)
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("%w: invalid binding input summary", ErrInvalidRecoveryRecord)
	}
	var decoded bindingSummary
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("%w: invalid binding input summary", ErrInvalidRecoveryRecord)
	}
	if decoded.NamespaceID != namespaceID {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("%w: binding summary namespace mismatch", ErrInvalidRecoveryRecord)
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	binding := resources.NamespaceVolumeBinding{
		NamespaceID:       decoded.NamespaceID,
		DefaultVolumeID:   decoded.DefaultVolumeID,
		AllowedCallers:    decoded.AllowedCallers,
		QuotaBytesDefault: decoded.QuotaBytesDefault,
		ExportPolicy:      decoded.ExportPolicy,
		LifecyclePolicy:   decoded.LifecyclePolicy,
		MountPolicy:       decoded.MountPolicy,
		TemplatePolicy:    decoded.TemplatePolicy,
		Status:            decoded.Status,
		CreatedAt:         createdAt,
		UpdatedAt:         now,
	}
	if err := binding.Validate(); err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("%w: invalid binding input summary", ErrInvalidRecoveryRecord)
	}
	return binding, nil
}
