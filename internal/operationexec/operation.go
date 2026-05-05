package operationexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

const unsupportedHandlerReason = "unsupported_operation_recovery_handler"

var (
	ErrUnsupportedOperationRecovery     = errors.New("unsupported operation recovery")
	ErrOperationAlreadyCommitted        = errors.New("operation recovery already committed")
	ErrOperationCommitIDMismatch        = errors.New("operation recovery commit operation id mismatch")
	ErrOperationCommitEventTypeMismatch = errors.New("operation recovery commit audit event type mismatch")
	ErrOperationHandlerAfterCommit      = errors.New("operation recovery handler returned error after durable commit")
)

type Config struct {
	CommitStore   store.OperationWorkerCommitStore
	Owner         string
	Now           time.Time
	Clock         func() time.Time
	Registrations []Registration
}

type Registration struct {
	OperationType operations.OperationType
	Phase         string
	Handler       Handler
}

// Handler registrations are keyed only by operation type and phase. A registered
// handler must be able to process every claim/retry/reclaim recovery plan the
// coordinator can pass for that type/phase; the concrete action remains visible
// on the RecoveryPlan argument.
type Handler interface {
	HandleOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error
}

type HandlerFunc func(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error

func (fn HandlerFunc) HandleOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan, committer Committer) error {
	return fn(ctx, record, plan, committer)
}

type Committer interface {
	Commit(ctx context.Context, record operations.SanitizedOperationRecord, event audit.Event) (operations.OperationRecord, error)
}

type Executor struct {
	commitStore store.OperationWorkerCommitStore
	owner       string
	now         time.Time
	clock       func() time.Time
	handlers    map[registrationKey]Handler
}

type registrationKey struct {
	operationType operations.OperationType
	phase         string
}

var _ recovery.OperationExecutor = (*Executor)(nil)

func NewExecutor(config Config) (*Executor, error) {
	if config.CommitStore == nil {
		return nil, errors.New("operation recovery commit store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, errors.New("operation recovery owner is required")
	}
	if config.Now.IsZero() && config.Clock == nil {
		return nil, errors.New("operation recovery time or clock is required")
	}

	handlers := make(map[registrationKey]Handler, len(config.Registrations))
	for _, registration := range config.Registrations {
		key, err := validateRegistration(registration)
		if err != nil {
			return nil, err
		}
		if _, exists := handlers[key]; exists {
			return nil, fmt.Errorf("duplicate operation recovery handler for %s/%s", key.operationType, key.phase)
		}
		handlers[key] = registration.Handler
	}

	return &Executor{
		commitStore: config.CommitStore,
		owner:       config.Owner,
		now:         config.Now,
		clock:       config.Clock,
		handlers:    handlers,
	}, nil
}

func validateRegistration(registration Registration) (registrationKey, error) {
	key := registrationKey{
		operationType: registration.OperationType,
		phase:         strings.TrimSpace(registration.Phase),
	}
	if key.operationType == "" {
		return registrationKey{}, errors.New("operation recovery registration operation type is required")
	}
	if !knownOperationType(key.operationType) {
		return registrationKey{}, fmt.Errorf("unknown operation recovery registration operation type %q", key.operationType)
	}
	if key.phase == "" {
		return registrationKey{}, errors.New("operation recovery registration phase is required")
	}
	if registration.Handler == nil {
		return registrationKey{}, errors.New("operation recovery registration handler is required")
	}
	return key, nil
}

func (executor *Executor) SupportsOperationRecovery(_ context.Context, record operations.OperationRecord, _ recovery.RecoveryPlan) recovery.OperationSupport {
	if executor == nil {
		return recovery.OperationSupport{Reason: unsupportedHandlerReason}
	}
	if _, ok := executor.handlers[keyForRecord(record)]; !ok {
		return recovery.OperationSupport{Reason: unsupportedHandlerReason}
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
	handler, ok := executor.handlers[keyForRecord(record)]
	if !ok {
		return fmt.Errorf("%w: %s phase %q", ErrUnsupportedOperationRecovery, record.Type, record.Phase)
	}
	now := executor.now
	if executor.clock != nil {
		now = executor.clock()
	}
	if now.IsZero() {
		return errors.New("operation recovery time must be set")
	}

	committer := &operationCommitter{store: executor.commitStore, owner: executor.owner, now: now, operationID: record.ID, operationType: record.Type}
	if err := handler.HandleOperationRecovery(ctx, record, plan, committer); err != nil {
		if committer.committed {
			return errors.Join(ErrOperationHandlerAfterCommit, err)
		}
		return err
	}
	if committer.err != nil {
		return committer.err
	}
	if !committer.committed {
		return errors.New("operation recovery handler returned without durable commit")
	}
	return nil
}

func keyForRecord(record operations.OperationRecord) registrationKey {
	return registrationKey{operationType: record.Type, phase: strings.TrimSpace(record.Phase)}
}

type operationCommitter struct {
	store         store.OperationWorkerCommitStore
	owner         string
	now           time.Time
	operationID   string
	operationType operations.OperationType
	attempted     bool
	committed     bool
	err           error
}

func (committer *operationCommitter) Commit(ctx context.Context, record operations.SanitizedOperationRecord, event audit.Event) (operations.OperationRecord, error) {
	if committer.attempted {
		committer.err = ErrOperationAlreadyCommitted
		return operations.OperationRecord{}, committer.err
	}
	committer.attempted = true
	if record.Record().ID != committer.operationID {
		committer.err = fmt.Errorf("%w: executing %q but commit record is %q", ErrOperationCommitIDMismatch, committer.operationID, record.Record().ID)
		return operations.OperationRecord{}, committer.err
	}
	wantEventType, ok := audit.EventTypeForOperationType(committer.operationType.String())
	if !ok || event.Type != wantEventType {
		committer.err = fmt.Errorf("%w: operation type %q requires audit event type %q, got %q", ErrOperationCommitEventTypeMismatch, committer.operationType, wantEventType, event.Type)
		return operations.OperationRecord{}, committer.err
	}
	updated, err := committer.store.CommitOperationWithLease(ctx, record, committer.owner, committer.now, event)
	if err != nil {
		committer.err = err
		return operations.OperationRecord{}, err
	}
	committer.committed = true
	return updated, nil
}

func knownOperationType(operationType operations.OperationType) bool {
	for _, candidate := range operations.OperationTypes() {
		if candidate == operationType {
			return true
		}
	}
	return false
}
