package workerapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store/postgres"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"

	_ "github.com/lib/pq"
)

type OperationRecoveryStore interface {
	store.NamespaceUpsertOperationRecoveryStore
}

type StoreFactory func(context.Context, string) (StoreHandle, error)

type StoreHandle struct {
	Store OperationRecoveryStore
	Close func() error
}

type Options struct {
	Source       config.Source
	StoreFactory StoreFactory
	Clock        func() time.Time
	AuditEventID namespaceexec.AuditEventIDGenerator
}

type RunOnceRunner struct {
	runner  worker.Runner
	timeout time.Duration
	close   func() error
}

var eventCounter uint64

func NewRunOnceRunnerFromEnv() (*RunOnceRunner, error) {
	return NewRunOnceRunner(Options{Source: config.EnvSource{}})
}

func NewRunOnceRunner(options Options) (*RunOnceRunner, error) {
	source := options.Source
	if source == nil {
		source = config.EnvSource{}
	}
	cfg, err := config.Load(source)
	if err != nil {
		return nil, err
	}
	opConfig := cfg.Worker.OperationRecovery
	if !opConfig.Enabled {
		return nil, errors.New("worker operation recovery is disabled")
	}

	now := nowFunc(options.Clock)
	storeFactory := options.StoreFactory
	if storeFactory == nil {
		storeFactory = OpenPostgresOperationRecoveryStore
	}
	openCtx, cancel := context.WithTimeout(context.Background(), cfg.Worker.RunOnceTimeout)
	defer cancel()
	handle, err := storeFactory(openCtx, opConfig.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("open worker operation recovery store: %w", err)
	}
	if handle.Store == nil {
		err := errors.New("worker operation recovery store is required")
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	scopedStore := namespaceUpsertRecoveryStore{store: handle.Store}

	eventID := options.AuditEventID
	if eventID == nil {
		eventID = NewAuditEventID
	}
	executor, err := namespaceexec.NewExecutor(namespaceexec.Config{
		CommitStore:  scopedStore,
		Owner:        opConfig.Owner,
		Clock:        now,
		AuditEventID: eventID,
	})
	if err != nil {
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	operationRecovery := recovery.NewOperationCoordinator(recovery.OperationConfig{
		Reader:        scopedStore,
		LeaseStore:    scopedStore,
		Executor:      executor,
		Owner:         opConfig.Owner,
		LeaseDuration: opConfig.LeaseDuration,
		Limit:         opConfig.Limit,
		Clock:         now,
	})
	return &RunOnceRunner{
		runner:  worker.New(worker.Config{OperationRecovery: operationRecovery}),
		timeout: cfg.Worker.RunOnceTimeout,
		close:   handle.Close,
	}, nil
}

func (runner *RunOnceRunner) RunOnce(ctx context.Context) (worker.Result, error) {
	if runner == nil {
		return worker.Result{}, errors.New("worker run-once runner is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := runner.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := runner.runner.RunOnce(runCtx)
	if err == nil {
		err = operationRecoveryCountError(result.OperationRecovery)
	}
	if runner.close != nil {
		err = errors.Join(err, runner.close())
	}
	return result, err
}

func OpenPostgresOperationRecoveryStore(ctx context.Context, dsn string) (StoreHandle, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return StoreHandle{}, err
	}
	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		return StoreHandle{}, errors.Join(err, closeErr)
	}
	return StoreHandle{
		Store: postgres.New(db),
		Close: db.Close,
	}, nil
}

func NewAuditEventID() string {
	counter := atomic.AddUint64(&eventCounter, 1)
	return fmt.Sprintf("evt_worker_%d_%d", time.Now().UTC().UnixNano(), counter)
}

func nowFunc(clock func() time.Time) func() time.Time {
	if clock != nil {
		return func() time.Time { return clock().UTC() }
	}
	return func() time.Time { return time.Now().UTC() }
}

type namespaceUpsertRecoveryStore struct {
	store OperationRecoveryStore
}

func (scoped namespaceUpsertRecoveryStore) ListOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	return scoped.store.ListNamespaceUpsertOperationsForRecovery(ctx, now, limit)
}

func (scoped namespaceUpsertRecoveryStore) AcquireOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	return scoped.store.AcquireNamespaceUpsertOperationLease(ctx, operationID, request)
}

func (scoped namespaceUpsertRecoveryStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, fmt.Errorf("%w: namespace upsert recovery does not renew leases", operations.ErrInvalidLeaseRequest)
}

func (scoped namespaceUpsertRecoveryStore) UpdateOperationWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, fmt.Errorf("%w: namespace upsert recovery does not perform generic operation updates", operations.ErrInvalidLeaseRequest)
}

func (scoped namespaceUpsertRecoveryStore) CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	return scoped.store.CommitNamespaceUpsertWithLease(ctx, namespace, record, owner, now, event)
}

func operationRecoveryCountError(result recovery.OperationBatchResult) error {
	if result.Unsupported == 0 && result.Manual == 0 && result.Failed == 0 {
		return nil
	}
	return fmt.Errorf("operation recovery incomplete: unsupported=%d manual=%d failed=%d", result.Unsupported, result.Manual, result.Failed)
}

var (
	_ OperationRecoveryStore                    = (*postgres.Store)(nil)
	_ store.OperationRecoveryReader             = namespaceUpsertRecoveryStore{}
	_ store.OperationLeaseStore                 = namespaceUpsertRecoveryStore{}
	_ store.NamespaceUpsertOperationCommitStore = namespaceUpsertRecoveryStore{}
)
