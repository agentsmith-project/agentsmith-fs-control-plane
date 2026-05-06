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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespacebindingexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
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
	store.RepoLifecycleOperationRecoveryStore
	store.RepoPurgeOperationRecoveryStore
	store.SavePointCreateOperationRecoveryStore
	store.RestorePreviewOperationRecoveryStore
	store.RestorePreviewDiscardOperationRecoveryStore
}

type WorkerStore interface {
	OperationRecoveryStore
	store.AuditOutboxDeliveryStore
}

type StoreFactory func(context.Context, string) (StoreHandle, error)
type JVSRunnerFactory func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error)
type StoragePurgerFactory func(config.WorkerRepoCreateRecoveryConfig) (repoexec.StoragePurger, error)
type AuditDelivererFactory func(config.WorkerAuditDeliveryConfig) (auditdelivery.Deliverer, error)

type StoreHandle struct {
	Store          WorkerStore
	OperationStore OperationRecoveryStore
	AuditStore     store.AuditOutboxDeliveryStore
	Close          func() error
}

type Options struct {
	Source                config.Source
	StoreFactory          StoreFactory
	JVSRunnerFactory      JVSRunnerFactory
	StoragePurgerFactory  StoragePurgerFactory
	AuditDelivererFactory AuditDelivererFactory
	Clock                 func() time.Time
	AuditEventID          namespaceexec.AuditEventIDGenerator
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
	auditConfig := cfg.Worker.AuditDelivery
	if !opConfig.Enabled && !auditConfig.Enabled {
		return nil, errors.New("worker run-once requires operation recovery or audit delivery to be enabled")
	}

	now := nowFunc(options.Clock)
	storeFactory := options.StoreFactory
	if storeFactory == nil {
		storeFactory = OpenPostgresOperationRecoveryStore
	}
	openCtx, cancel := context.WithTimeout(context.Background(), cfg.Worker.RunOnceTimeout)
	defer cancel()
	dsn, err := workerStoreDSN(opConfig, auditConfig)
	if err != nil {
		return nil, err
	}
	handle, err := storeFactory(openCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open worker store: %w", err)
	}
	operationStore := handle.OperationStore
	auditStore := handle.AuditStore
	if handle.Store != nil {
		if operationStore == nil {
			operationStore = handle.Store
		}
		if auditStore == nil {
			auditStore = handle.Store
		}
	}
	if opConfig.Enabled && operationStore == nil {
		err := errors.New("worker operation recovery store is required")
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	if auditConfig.Enabled && auditStore == nil {
		err := errors.New("worker audit delivery store is required")
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	scopedStore := operationRecoveryStore{store: operationStore}
	workerConfig := worker.Config{}

	if opConfig.Enabled {
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
		if opConfig.RepoLifecycle.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.RepoLifecycle)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			lifecycleExecutor, err := repoexec.NewLifecycleExecutor(repoexec.LifecycleConfig{
				Store:        scopedStore,
				JVSRunner:    jvs,
				Owner:        opConfig.Owner,
				Clock:        now,
				AuditEventID: func() string { return eventID() },
				VolumeRoots:  opConfig.RepoLifecycle.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, lifecycleExecutor)
			scopedStore.repoLifecycleEnabled = true
		}
		if opConfig.RepoPurge.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.RepoPurge)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			purgerFactory := options.StoragePurgerFactory
			if purgerFactory == nil {
				purgerFactory = NewStoragePurgerFromConfig
			}
			purger, err := purgerFactory(opConfig.RepoPurge)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			purgeExecutor, err := repoexec.NewPurgeExecutor(repoexec.PurgeConfig{
				Store:         scopedStore,
				JVSRunner:     jvs,
				StoragePurger: purger,
				Owner:         opConfig.Owner,
				Clock:         now,
				AuditEventID:  func() string { return eventID() },
				VolumeRoots:   opConfig.RepoPurge.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, purgeExecutor)
			scopedStore.repoPurgeEnabled = true
		}
		if opConfig.SavePoint.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.SavePoint)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			savePointExecutor, err := repoexec.NewSavePointExecutor(repoexec.SavePointConfig{
				Store:        scopedStore,
				JVSRunner:    jvs,
				Owner:        opConfig.Owner,
				Clock:        now,
				AuditEventID: func() string { return eventID() },
				VolumeRoots:  opConfig.SavePoint.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, savePointExecutor)
			scopedStore.savePointEnabled = true
		}
		if opConfig.RestorePreview.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.RestorePreview)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			restorePreviewJVS, ok := jvs.(repoexec.RestorePreviewJVSRunner)
			if !ok {
				if handle.Close != nil {
					err = errors.Join(errors.New("restore preview jvs runner does not support restore preview"), handle.Close())
				} else {
					err = errors.New("restore preview jvs runner does not support restore preview")
				}
				return nil, err
			}
			restorePreviewExecutor, err := repoexec.NewRestorePreviewExecutor(repoexec.RestorePreviewConfig{
				Store:        scopedStore,
				JVSRunner:    restorePreviewJVS,
				Owner:        opConfig.Owner,
				Clock:        now,
				AuditEventID: func() string { return eventID() },
				VolumeRoots:  opConfig.RestorePreview.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, restorePreviewExecutor)
			scopedStore.restorePreviewEnabled = true
		}
		if opConfig.RestorePreviewDiscard.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.RestorePreviewDiscard)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			restorePreviewDiscardJVS, ok := jvs.(repoexec.RestorePreviewDiscardJVSRunner)
			if !ok {
				if handle.Close != nil {
					err = errors.Join(errors.New("restore preview discard jvs runner does not support restore discard"), handle.Close())
				} else {
					err = errors.New("restore preview discard jvs runner does not support restore discard")
				}
				return nil, err
			}
			restorePreviewDiscardExecutor, err := repoexec.NewRestorePreviewDiscardExecutor(repoexec.RestorePreviewDiscardConfig{
				Store:        scopedStore,
				JVSRunner:    restorePreviewDiscardJVS,
				Owner:        opConfig.Owner,
				Clock:        now,
				AuditEventID: func() string { return eventID() },
				VolumeRoots:  opConfig.RestorePreviewDiscard.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, restorePreviewDiscardExecutor)
			scopedStore.restorePreviewDiscardEnabled = true
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
		workerConfig.OperationRecovery = operationRecovery
	}

	if auditConfig.Enabled {
		delivererFactory := options.AuditDelivererFactory
		if delivererFactory == nil {
			delivererFactory = NewAuditDelivererFromConfig
		}
		deliverer, err := delivererFactory(auditConfig)
		if err != nil {
			if handle.Close != nil {
				err = errors.Join(err, handle.Close())
			}
			return nil, err
		}
		workerConfig.AuditStaleRecovery = auditdelivery.NewStaleRecoveryCoordinator(auditdelivery.StaleRecoveryConfig{
			Store:          auditStore,
			Owner:          auditConfig.Owner,
			StaleThreshold: auditConfig.StaleThreshold,
			Limit:          auditConfig.Limit,
			MaxAttempts:    auditConfig.MaxAttempts,
			RetryBackoff:   auditConfig.RetryBackoff,
			Clock:          now,
		})
		workerConfig.AuditDelivery = auditdelivery.NewCoordinator(auditdelivery.Config{
			Store:        auditStore,
			Deliverer:    deliverer,
			Owner:        auditConfig.Owner,
			Limit:        auditConfig.Limit,
			MaxAttempts:  auditConfig.MaxAttempts,
			RetryBackoff: auditConfig.RetryBackoff,
			Clock:        now,
		})
	}
	return &RunOnceRunner{
		runner:  worker.New(workerConfig),
		timeout: cfg.Worker.RunOnceTimeout,
		close:   handle.Close,
	}, nil
}

func workerStoreDSN(opConfig config.WorkerOperationRecoveryConfig, auditConfig config.WorkerAuditDeliveryConfig) (string, error) {
	if opConfig.Enabled && auditConfig.Enabled && opConfig.PostgresDSN != auditConfig.PostgresDSN {
		return "", errors.New("worker operation recovery and audit delivery must use the same postgres dsn")
	}
	if opConfig.Enabled {
		return opConfig.PostgresDSN, nil
	}
	return auditConfig.PostgresDSN, nil
}

func NewAuditDelivererFromConfig(cfg config.WorkerAuditDeliveryConfig) (auditdelivery.Deliverer, error) {
	return NewHTTPAuditDeliverer(HTTPAuditDelivererConfig{
		Endpoint:    cfg.Endpoint,
		BearerToken: cfg.BearerToken,
		Timeout:     cfg.Timeout,
	})
}

func NewJVSRunnerFromConfig(cfg config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error) {
	if err := verifyFileSHA256(cfg.JVSBinaryPath, cfg.JVSBinarySHA256); err != nil {
		return nil, err
	}
	return jvsrunner.New(jvsrunner.Config{BinaryPath: cfg.JVSBinaryPath, CWD: cfg.JVSCWD})
}

func NewStoragePurgerFromConfig(config.WorkerRepoCreateRecoveryConfig) (repoexec.StoragePurger, error) {
	return repoexec.FilesystemStoragePurger{}, nil
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
		err = errors.Join(operationRecoveryCountError(result.OperationRecovery), auditDeliveryCountError(result.AuditStaleRecovery, result.AuditDelivery))
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
	st := postgres.New(db)
	return StoreHandle{
		Store:          st,
		OperationStore: st,
		AuditStore:     st,
		Close:          db.Close,
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
	store                        OperationRecoveryStore
	repoCreateEnabled            bool
	repoLifecycleEnabled         bool
	repoPurgeEnabled             bool
	savePointEnabled             bool
	restorePreviewEnabled        bool
	restorePreviewDiscardEnabled bool
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
	if scoped.repoLifecycleEnabled {
		lifecycleRecords, err := scoped.store.ListRepoLifecycleOperationsForRecovery(ctx, now, limit)
		if err != nil {
			return nil, err
		}
		records = append(records, lifecycleRecords...)
	}
	if scoped.repoPurgeEnabled {
		purgeRecords, err := scoped.store.ListRepoPurgeOperationsForRecovery(ctx, now, limit)
		if err != nil {
			return nil, err
		}
		records = append(records, purgeRecords...)
	}
	if scoped.savePointEnabled {
		savePointRecords, err := scoped.store.ListSavePointCreateOperationsForRecovery(ctx, now, limit)
		if err != nil {
			return nil, err
		}
		records = append(records, savePointRecords...)
	}
	if scoped.restorePreviewEnabled {
		restorePreviewRecords, err := scoped.store.ListRestorePreviewOperationsForRecovery(ctx, now, limit)
		if err != nil {
			return nil, err
		}
		records = append(records, restorePreviewRecords...)
	}
	if scoped.restorePreviewDiscardEnabled {
		restorePreviewDiscardRecords, err := scoped.store.ListRestorePreviewDiscardOperationsForRecovery(ctx, now, limit)
		if err != nil {
			return nil, err
		}
		records = append(records, restorePreviewDiscardRecords...)
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
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	if scoped.repoCreateEnabled {
		record, err = scoped.store.AcquireRepoCreateOperationLease(ctx, operationID, request)
		if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) || (!scoped.repoLifecycleEnabled && !scoped.repoPurgeEnabled && !scoped.savePointEnabled && !scoped.restorePreviewEnabled && !scoped.restorePreviewDiscardEnabled) {
			return record, err
		}
	}
	if scoped.repoLifecycleEnabled {
		record, err = scoped.store.AcquireRepoLifecycleOperationLease(ctx, operationID, request)
		if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) || (!scoped.repoPurgeEnabled && !scoped.savePointEnabled && !scoped.restorePreviewEnabled && !scoped.restorePreviewDiscardEnabled) {
			return record, err
		}
	}
	if scoped.repoPurgeEnabled {
		record, err = scoped.store.AcquireRepoPurgeOperationLease(ctx, operationID, request)
		if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) || (!scoped.savePointEnabled && !scoped.restorePreviewEnabled && !scoped.restorePreviewDiscardEnabled) {
			return record, err
		}
	}
	if scoped.savePointEnabled {
		record, err = scoped.store.AcquireSavePointCreateOperationLease(ctx, operationID, request)
		if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) || (!scoped.restorePreviewEnabled && !scoped.restorePreviewDiscardEnabled) {
			return record, err
		}
	}
	if scoped.restorePreviewEnabled {
		record, err = scoped.store.AcquireRestorePreviewOperationLease(ctx, operationID, request)
		if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) || !scoped.restorePreviewDiscardEnabled {
			return record, err
		}
	}
	if scoped.restorePreviewDiscardEnabled {
		return scoped.store.AcquireRestorePreviewDiscardOperationLease(ctx, operationID, request)
	}
	return operations.OperationRecord{}, operations.ErrLeaseUnavailable
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

func (scoped operationRecoveryStore) CommitRepoLifecycleSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	return scoped.store.CommitRepoLifecycleSucceededWithLease(ctx, repo, record, owner, now, event, fenceID)
}

func (scoped operationRecoveryStore) CommitRepoLifecycleFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	return scoped.store.CommitRepoLifecycleFailedWithLease(ctx, record, owner, now, event, releaseFenceID)
}

func (scoped operationRecoveryStore) CommitRepoPurgeSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	return scoped.store.CommitRepoPurgeSucceededWithLease(ctx, repo, record, owner, now, event, fenceID)
}

func (scoped operationRecoveryStore) CommitRepoPurgeFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	return scoped.store.CommitRepoPurgeFailedWithLease(ctx, record, owner, now, event, releaseFenceID)
}

func (scoped operationRecoveryStore) UpdateSavePointCreateProgressWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	return scoped.store.UpdateSavePointCreateProgressWithLease(ctx, record, owner, now)
}

func (scoped operationRecoveryStore) CommitSavePointCreateSucceededWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitSavePointCreateSucceededWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitSavePointCreateFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitSavePointCreateFailedWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) UpdateRestorePreviewPreflightWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	return scoped.store.UpdateRestorePreviewPreflightWithLease(ctx, record, owner, now)
}

func (scoped operationRecoveryStore) CommitRestorePreviewSucceededWithLease(ctx context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return scoped.store.CommitRestorePreviewSucceededWithLease(ctx, plan, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitRestorePreviewFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitRestorePreviewFailedWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) MarkRestorePreviewDiscardingWithLease(ctx context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, owner string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	return scoped.store.MarkRestorePreviewDiscardingWithLease(ctx, plan, record, owner, now)
}

func (scoped operationRecoveryStore) CommitRestorePreviewDiscardSucceededWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return scoped.store.CommitRestorePreviewDiscardSucceededWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitRestorePreviewDiscardFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitRestorePreviewDiscardFailedWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error) {
	return scoped.store.GetOperation(ctx, operationID)
}

func (scoped operationRecoveryStore) GetRestorePlanByPreviewOperation(ctx context.Context, previewOperationID string) (restoreplan.Plan, error) {
	return scoped.store.GetRestorePlanByPreviewOperation(ctx, previewOperationID)
}

func (scoped operationRecoveryStore) GetActiveRestorePlanByRepo(ctx context.Context, repoID string) (restoreplan.Plan, error) {
	return scoped.store.GetActiveRestorePlanByRepo(ctx, repoID)
}

func (scoped operationRecoveryStore) GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error) {
	return scoped.store.GetRepoInNamespace(ctx, namespaceID, repoID)
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

func (scoped operationRecoveryStore) ListExportSessionsByRepo(ctx context.Context, repoID string) ([]sessionstate.ExportSession, error) {
	return scoped.store.ListExportSessionsByRepo(ctx, repoID)
}

func (scoped operationRecoveryStore) ListWorkloadMountBindingsByRepo(ctx context.Context, repoID string) ([]sessionstate.WorkloadMountBinding, error) {
	return scoped.store.ListWorkloadMountBindingsByRepo(ctx, repoID)
}

func (scoped operationRecoveryStore) ListEarlierNonTerminalRepoLifecycleOperations(ctx context.Context, repoID, operationID string, createdAt time.Time) ([]operations.OperationRecord, error) {
	return scoped.store.ListEarlierNonTerminalRepoLifecycleOperations(ctx, repoID, operationID, createdAt)
}

func operationRecoveryCountError(result recovery.OperationBatchResult) error {
	if result.Unsupported == 0 && result.Manual == 0 && result.Failed == 0 {
		return nil
	}
	return fmt.Errorf("operation recovery incomplete: unsupported=%d manual=%d failed=%d", result.Unsupported, result.Manual, result.Failed)
}

func auditDeliveryCountError(stale auditdelivery.StaleRecoveryResult, delivery auditdelivery.BatchResult) error {
	if stale.Failed == 0 && stale.FailedTerminal == 0 && delivery.Failed == 0 && delivery.DeliveryFailuresRecorded == 0 {
		return nil
	}
	return fmt.Errorf("audit delivery incomplete: stale_failed=%d stale_failed_terminal=%d delivery_failures_recorded=%d failed=%d", stale.Failed, stale.FailedTerminal, delivery.DeliveryFailuresRecorded, delivery.Failed)
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
	_ store.RepoLifecycleOperationCommitStore          = operationRecoveryStore{}
	_ store.RepoPurgeOperationCommitStore              = operationRecoveryStore{}
	_ store.SavePointCreateOperationCommitStore        = operationRecoveryStore{}
	_ store.RestorePreviewOperationCommitStore         = operationRecoveryStore{}
	_ store.RestorePreviewDiscardOperationCommitStore  = operationRecoveryStore{}
	_ recovery.OperationExecutor                       = multiExecutor{}
)
