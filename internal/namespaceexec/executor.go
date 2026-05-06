package namespaceexec

import (
	"context"
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
	unsupportedOperationReason = "unsupported_namespace_upsert_operation"
	unsupportedPhaseReason     = "unsupported_namespace_upsert_phase"
	unsupportedActionReason    = "unsupported_namespace_upsert_recovery_action"
)

var (
	ErrUnsupportedOperationRecovery = errors.New("unsupported namespace upsert operation recovery")
	ErrInvalidRecoveryPlan          = errors.New("invalid namespace upsert recovery plan")
	ErrInvalidRecoveryRecord        = errors.New("invalid namespace upsert recovery record")
)

type AuditEventIDGenerator func() string

type Config struct {
	CommitStore  store.NamespaceUpsertOperationCommitStore
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
}

type DisableConfig struct {
	CommitStore  store.NamespaceDisableOperationCommitStore
	Owner        string
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
}

type DisableExecutor struct {
	commitStore  store.NamespaceDisableOperationCommitStore
	owner        string
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
}

type Executor struct {
	commitStore  store.NamespaceUpsertOperationCommitStore
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
}

var _ recovery.OperationExecutor = (*Executor)(nil)
var _ recovery.OperationExecutor = (*DisableExecutor)(nil)

func NewExecutor(config Config) (*Executor, error) {
	if config.CommitStore == nil {
		return nil, errors.New("namespace upsert recovery commit store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("namespace upsert recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("namespace upsert recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("namespace upsert recovery audit event id generator is required")
	}
	return &Executor{
		commitStore:  config.CommitStore,
		owner:        config.Owner,
		now:          config.Now,
		clock:        config.Clock,
		auditEventID: config.AuditEventID,
	}, nil
}

func NewDisableExecutor(config DisableConfig) (*DisableExecutor, error) {
	if config.CommitStore == nil {
		return nil, errors.New("namespace disable recovery commit store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("namespace disable recovery owner is required")
	}
	if config.Clock == nil {
		return nil, errors.New("namespace disable recovery clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("namespace disable recovery audit event id generator is required")
	}
	return &DisableExecutor{commitStore: config.CommitStore, owner: config.Owner, clock: config.Clock, auditEventID: config.AuditEventID}, nil
}

func (executor *DisableExecutor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationNamespaceDisable {
		return recovery.OperationSupport{Reason: "unsupported_namespace_disable_operation"}
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseNamespaceDisableValidate {
		return recovery.OperationSupport{Reason: "unsupported_namespace_disable_phase"}
	}
	if !supportedPlanAction(plan.Action) {
		return recovery.OperationSupport{Reason: "unsupported_namespace_disable_recovery_action"}
	}
	return recovery.OperationSupport{Supported: true}
}

func (executor *DisableExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return ErrUnsupportedOperationRecovery
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("%w: %s", ErrUnsupportedOperationRecovery, support.Reason)
	}
	if err := validateLeasedRunningRecord(record, executor.owner); err != nil {
		return err
	}
	now := executor.clock().UTC()
	if now.IsZero() {
		return errors.New("namespace disable recovery time must be set")
	}
	reason := strings.TrimSpace(stringFromSummary(record.InputSummary, "reason"))
	if reason == "" {
		return fmt.Errorf("%w: missing disable reason", ErrInvalidRecoveryRecord)
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	disabledAt := now
	namespace := resources.Namespace{ID: record.NamespaceID, Status: resources.NamespaceStatusDisabled, DisabledReason: reason, DisabledAt: &disabledAt, CreatedAt: createdAt, UpdatedAt: now}
	if err := namespace.Validate(); err != nil {
		return err
	}
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseNamespaceDisableCommitted
	operation.Error = nil
	operation.FinishedAt = &now
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return errors.New("namespace disable recovery audit event id must be set")
	}
	event := audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeNamespaceDisable,
		Time:            now,
		CallerService:   operation.CallerService,
		AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID},
		CorrelationID:   operation.CorrelationID,
		OperationID:     operation.ID,
		Resource:        audit.Resource{Type: "namespace", ID: operation.NamespaceID, NamespaceID: operation.NamespaceID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "namespace_disable_committed",
		Details:         map[string]any{"namespace_id": operation.NamespaceID, "reason": reason},
	})
	_, _, err := executor.commitStore.CommitNamespaceDisableWithLease(ctx, namespace, operation.SanitizedForPersistence(), executor.owner, now, event)
	return err
}

func (executor *Executor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil {
		return recovery.OperationSupport{Reason: unsupportedOperationReason}
	}
	if record.Type != operations.OperationNamespaceUpsert {
		return recovery.OperationSupport{Reason: unsupportedOperationReason}
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseNamespaceUpsertValidate {
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
		return errors.New("namespace upsert recovery time must be set")
	}

	namespace, operation, event, err := executor.commitRequest(record, now)
	if err != nil {
		return err
	}
	_, _, err = executor.commitStore.CommitNamespaceUpsertWithLease(ctx, namespace, operation.SanitizedForPersistence(), executor.owner, now, event)
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

func stringFromSummary(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
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
	if record.Resource.Type != "namespace" {
		return fmt.Errorf("%w: operation resource must be namespace", ErrInvalidRecoveryRecord)
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

func (executor *Executor) commitRequest(record operations.OperationRecord, now time.Time) (resources.Namespace, operations.OperationRecord, audit.Event, error) {
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	namespace := resources.Namespace{
		ID:        record.NamespaceID,
		Status:    resources.NamespaceStatusActive,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}
	if err := namespace.Validate(); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, audit.Event{}, err
	}

	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseNamespaceUpsertCommitted
	operation.Error = nil
	operation.FinishedAt = &now

	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return resources.Namespace{}, operations.OperationRecord{}, audit.Event{}, errors.New("namespace upsert recovery audit event id must be set")
	}
	event := audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeNamespaceUpsert,
		Time:            now,
		CallerService:   operation.CallerService,
		AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID},
		CorrelationID:   operation.CorrelationID,
		OperationID:     operation.ID,
		Resource: audit.Resource{
			Type:        "namespace",
			ID:          operation.NamespaceID,
			NamespaceID: operation.NamespaceID,
		},
		Outcome: audit.OutcomeSucceeded,
		Reason:  "namespace_upsert_committed",
		Details: map[string]any{"namespace_id": operation.NamespaceID},
	})
	return namespace, operation, event, nil
}
