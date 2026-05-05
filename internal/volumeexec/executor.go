package volumeexec

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

type AuditEventIDGenerator func() string

type Config struct {
	CommitStore  store.VolumeEnsureOperationCommitStore
	Owner        string
	Now          time.Time
	Clock        func() time.Time
	AuditEventID AuditEventIDGenerator
}

type Executor struct {
	commitStore  store.VolumeEnsureOperationCommitStore
	owner        string
	now          time.Time
	clock        func() time.Time
	auditEventID AuditEventIDGenerator
}

func NewExecutor(config Config) (*Executor, error) {
	if config.CommitStore == nil {
		return nil, errors.New("volume ensure recovery commit store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("volume ensure recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("volume ensure recovery time or clock is required")
	}
	if config.AuditEventID == nil {
		return nil, errors.New("volume ensure recovery audit event id generator is required")
	}
	return &Executor{commitStore: config.CommitStore, owner: config.Owner, now: config.Now, clock: config.Clock, auditEventID: config.AuditEventID}, nil
}

func (executor *Executor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil || record.Type != operations.OperationVolumeEnsure {
		return recovery.OperationSupport{Reason: "unsupported_volume_ensure_operation"}
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseVolumeEnsureValidate {
		return recovery.OperationSupport{Reason: "unsupported_volume_ensure_phase"}
	}
	switch plan.Action {
	case recovery.RecoveryActionClaimable, recovery.RecoveryActionRetry, recovery.RecoveryActionReclaim:
		return recovery.OperationSupport{Supported: true}
	default:
		return recovery.OperationSupport{Reason: "unsupported_volume_ensure_recovery_action"}
	}
}

func (executor *Executor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if executor == nil {
		return errors.New("unsupported volume ensure operation recovery")
	}
	if support := executor.SupportsOperationRecovery(ctx, record, plan); !support.Supported {
		return fmt.Errorf("unsupported volume ensure operation recovery: %s", support.Reason)
	}
	if record.State != operations.OperationStateRunning || record.LeaseOwner != executor.owner || record.LeaseExpiresAt == nil || record.Resource.Type != "volume" || record.Resource.ID == "" {
		return errors.New("invalid volume ensure recovery record")
	}
	if strings.TrimSpace(record.NamespaceID) != "" {
		return errors.New("invalid volume ensure recovery record")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return errors.New("invalid volume ensure recovery record")
	}
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	if now.IsZero() {
		return errors.New("volume ensure recovery time must be set")
	}
	volume, err := volumeFromSummary(record.InputSummary, record.CreatedAt, now)
	if err != nil {
		return err
	}
	if volume.ID != record.Resource.ID {
		return errors.New("invalid volume ensure recovery record")
	}
	operation := record
	operation.State = operations.OperationStateSucceeded
	operation.Phase = operations.OperationPhaseVolumeEnsureCommitted
	operation.Error = nil
	operation.FinishedAt = &now
	eventID := strings.TrimSpace(executor.auditEventID())
	if eventID == "" {
		return errors.New("volume ensure recovery audit event id must be set")
	}
	event := audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeVolumeEnsure,
		Time:            now,
		CallerService:   operation.CallerService,
		AuthorizedActor: audit.Actor{Type: operation.AuthorizedActor.Type, ID: operation.AuthorizedActor.ID},
		CorrelationID:   operation.CorrelationID,
		OperationID:     operation.ID,
		Resource:        audit.Resource{Type: "volume", ID: volume.ID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "volume_ensure_committed",
		Details:         map[string]any{"volume_id": volume.ID},
	})
	_, _, err = executor.commitStore.CommitVolumeEnsureWithLease(ctx, volume, operation.SanitizedForPersistence(), executor.owner, now, event)
	return err
}

type volumeSummary struct {
	VolumeID       string                         `json:"volume_id"`
	Backend        resources.VolumeBackend        `json:"backend"`
	IsolationClass resources.VolumeIsolationClass `json:"isolation_class"`
	Status         resources.VolumeStatus         `json:"status"`
	Capabilities   map[string]any                 `json:"capabilities"`
}

func volumeFromSummary(summary map[string]any, createdAt, now time.Time) (resources.Volume, error) {
	if err := validateVolumeSummaryKeys(summary); err != nil {
		return resources.Volume{}, err
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		return resources.Volume{}, err
	}
	var decoded volumeSummary
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return resources.Volume{}, err
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	volume := resources.Volume{ID: decoded.VolumeID, Backend: decoded.Backend, IsolationClass: decoded.IsolationClass, Status: decoded.Status, Capabilities: decoded.Capabilities, CreatedAt: createdAt, UpdatedAt: now}
	if err := volume.Validate(); err != nil {
		return resources.Volume{}, err
	}
	return volume, nil
}

func validateVolumeSummaryKeys(summary map[string]any) error {
	allowed := map[string]struct{}{
		"volume_id":       {},
		"backend":         {},
		"isolation_class": {},
		"status":          {},
		"capabilities":    {},
	}
	for key := range summary {
		if _, ok := allowed[key]; !ok {
			return errors.New("invalid volume ensure input summary")
		}
	}
	return nil
}

var _ recovery.OperationExecutor = (*Executor)(nil)
