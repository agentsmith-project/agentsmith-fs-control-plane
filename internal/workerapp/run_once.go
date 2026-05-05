package workerapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync/atomic"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespacebindingexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store/postgres"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/volumeexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"

	_ "github.com/lib/pq"
)

type OperationRecoveryStore interface {
	store.VolumeEnsureOperationRecoveryStore
	store.NamespaceUpsertOperationRecoveryStore
	store.NamespaceVolumeBindingOperationRecoveryStore
	store.RepoCreateOperationRecoveryStore
}

type StoreFactory func(context.Context, string) (StoreHandle, error)
type JVSRunnerFactory func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error)

type StoreHandle struct {
	Store OperationRecoveryStore
	Close func() error
}

type Options struct {
	Source           config.Source
	StoreFactory     StoreFactory
	JVSRunnerFactory JVSRunnerFactory
	Clock            func() time.Time
	AuditEventID     namespaceexec.AuditEventIDGenerator
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
	scopedStore := operationRecoveryStore{store: handle.Store}

	eventID := options.AuditEventID
	if eventID == nil {
		eventID = NewAuditEventID
	}
	namespaceExecutor, err := namespaceexec.NewExecutor(namespaceexec.Config{
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
	bindingExecutor, err := namespacebindingexec.NewExecutor(namespacebindingexec.Config{
		CommitStore:  scopedStore,
		Owner:        opConfig.Owner,
		Clock:        now,
		AuditEventID: func() string { return eventID() },
	})
	if err != nil {
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	volumeExecutor, err := volumeexec.NewExecutor(volumeexec.Config{
		CommitStore:  scopedStore,
		Owner:        opConfig.Owner,
		Clock:        now,
		AuditEventID: func() string { return eventID() },
	})
	if err != nil {
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	executors := []recovery.OperationExecutor{volumeExecutor, namespaceExecutor, bindingExecutor}
	if opConfig.RepoCreate.Enabled {
		jvsFactory := options.JVSRunnerFactory
		if jvsFactory == nil {
			jvsFactory = NewJVSRunnerFromConfig
		}
		jvs, err := jvsFactory(opConfig.RepoCreate)
		if err != nil {
			if handle.Close != nil {
				err = errors.Join(err, handle.Close())
			}
			return nil, err
		}
		repoExecutor, err := repoexec.NewExecutor(repoexec.Config{
			Store:        scopedStore,
			JVSRunner:    jvs,
			Owner:        opConfig.Owner,
			Clock:        now,
			AuditEventID: func() string { return eventID() },
			VolumeRoots:  opConfig.RepoCreate.VolumeRoots,
		})
		if err != nil {
			if handle.Close != nil {
				err = errors.Join(err, handle.Close())
			}
			return nil, err
		}
		executors = append(executors, repoExecutor)
		scopedStore.repoCreateEnabled = true
	}
	operationRecovery := recovery.NewOperationCoordinator(recovery.OperationConfig{
		Reader:        scopedStore,
		LeaseStore:    scopedStore,
		Executor:      multiExecutor{executors: executors},
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

func NewJVSRunnerFromConfig(cfg config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
	if err := verifyFileSHA256(cfg.JVSBinaryPath, cfg.JVSBinarySHA256); err != nil {
		return nil, err
	}
	return jvsrunner.New(jvsrunner.Config{BinaryPath: cfg.JVSBinaryPath, CWD: cfg.JVSCWD})
}

func verifyFileSHA256(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("jvs binary verification failed")
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return errors.New("jvs binary verification failed")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return errors.New("jvs binary checksum mismatch")
	}
	return nil
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

type operationRecoveryStore struct {
	store             OperationRecoveryStore
	repoCreateEnabled bool
}

func (scoped operationRecoveryStore) ListOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	volumeRecords, err := scoped.store.ListVolumeEnsureOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	namespaceRecords, err := scoped.store.ListNamespaceUpsertOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	bindingRecords, err := scoped.store.ListNamespaceVolumeBindingPutOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records := append(volumeRecords, namespaceRecords...)
	records = append(records, bindingRecords...)
	if scoped.repoCreateEnabled {
		repoRecords, err := scoped.store.ListRepoCreateOperationsForRecovery(ctx, now, limit)
		if err != nil {
			return nil, err
		}
		records = append(records, repoRecords...)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (scoped operationRecoveryStore) AcquireOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	record, err := scoped.store.AcquireVolumeEnsureOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireNamespaceUpsertOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireNamespaceVolumeBindingPutOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) || !scoped.repoCreateEnabled {
		return record, err
	}
	return scoped.store.AcquireRepoCreateOperationLease(ctx, operationID, request)
}

func (scoped operationRecoveryStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, fmt.Errorf("%w: worker operation recovery does not renew leases", operations.ErrInvalidLeaseRequest)
}

func (scoped operationRecoveryStore) UpdateOperationWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, fmt.Errorf("%w: worker operation recovery does not perform generic operation updates", operations.ErrInvalidLeaseRequest)
}

func (scoped operationRecoveryStore) CommitVolumeEnsureWithLease(ctx context.Context, volume resources.Volume, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Volume, operations.OperationRecord, error) {
	return scoped.store.CommitVolumeEnsureWithLease(ctx, volume, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	return scoped.store.CommitNamespaceUpsertWithLease(ctx, namespace, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitNamespaceVolumeBindingPutWithLease(ctx context.Context, binding resources.NamespaceVolumeBinding, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.NamespaceVolumeBinding, operations.OperationRecord, error) {
	return scoped.store.CommitNamespaceVolumeBindingPutWithLease(ctx, binding, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitRepoCreateSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	return scoped.store.CommitRepoCreateSucceededWithLease(ctx, repo, record, owner, now, event, fenceID)
}

func (scoped operationRecoveryStore) CommitRepoCreateFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	return scoped.store.CommitRepoCreateFailedWithLease(ctx, record, owner, now, event, releaseFenceID)
}

func (scoped operationRecoveryStore) GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error) {
	return scoped.store.GetNamespace(ctx, namespaceID)
}

func (scoped operationRecoveryStore) GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error) {
	return scoped.store.GetNamespaceVolumeBinding(ctx, namespaceID)
}

func (scoped operationRecoveryStore) GetVolume(ctx context.Context, volumeID string) (resources.Volume, error) {
	return scoped.store.GetVolume(ctx, volumeID)
}

func (scoped operationRecoveryStore) ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error) {
	return scoped.store.ListHeldRepoFences(ctx, repoID)
}

func (scoped operationRecoveryStore) CreateRepoFence(ctx context.Context, fence fences.Fence) error {
	return scoped.store.CreateRepoFence(ctx, fence)
}

func operationRecoveryCountError(result recovery.OperationBatchResult) error {
	if result.Unsupported == 0 && result.Manual == 0 && result.Failed == 0 {
		return nil
	}
	return fmt.Errorf("operation recovery incomplete: unsupported=%d manual=%d failed=%d", result.Unsupported, result.Manual, result.Failed)
}

type multiExecutor struct {
	executors []recovery.OperationExecutor
}

func (executor multiExecutor) SupportsOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) recovery.OperationSupport {
	var reason string
	for _, candidate := range executor.executors {
		if candidate == nil {
			continue
		}
		support := candidate.SupportsOperationRecovery(ctx, record, plan)
		if support.Supported {
			return support
		}
		if reason == "" {
			reason = support.Reason
		}
	}
	if reason == "" {
		reason = "unsupported_operation_recovery"
	}
	return recovery.OperationSupport{Reason: reason}
}

func (executor multiExecutor) ExecuteOperationRecovery(ctx context.Context, record operations.OperationRecord, plan recovery.RecoveryPlan) error {
	for _, candidate := range executor.executors {
		if candidate == nil {
			continue
		}
		if candidate.SupportsOperationRecovery(ctx, record, plan).Supported {
			return candidate.ExecuteOperationRecovery(ctx, record, plan)
		}
	}
	return errors.New("unsupported operation recovery")
}

var (
	_ OperationRecoveryStore                           = (*postgres.Store)(nil)
	_ store.OperationRecoveryReader                    = operationRecoveryStore{}
	_ store.OperationLeaseStore                        = operationRecoveryStore{}
	_ store.VolumeEnsureOperationCommitStore           = operationRecoveryStore{}
	_ store.NamespaceUpsertOperationCommitStore        = operationRecoveryStore{}
	_ store.NamespaceVolumeBindingOperationCommitStore = operationRecoveryStore{}
	_ store.RepoCreateOperationCommitStore             = operationRecoveryStore{}
	_ recovery.OperationExecutor                       = multiExecutor{}
)
