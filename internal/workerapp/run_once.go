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
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportreconcile"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/mountbindingexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespacebindingexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restorereconcile"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store/postgres"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/volumeexec"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"

	_ "github.com/lib/pq"
)

type OperationRecoveryStore interface {
	store.OperationWorkerCommitStore
	store.VolumeEnsureOperationRecoveryStore
	store.NamespaceUpsertOperationRecoveryStore
	store.NamespaceDisableOperationRecoveryStore
	store.NamespaceVolumeBindingOperationRecoveryStore
	store.RepoCreateOperationRecoveryStore
	store.RepoLifecycleOperationRecoveryStore
	store.RepoPurgeOperationRecoveryStore
	store.SavePointCreateOperationRecoveryStore
	store.TemplateOperationRecoveryStore
	store.RestorePreviewOperationRecoveryStore
	store.RestorePreviewDiscardOperationRecoveryStore
	store.RestoreRunOperationRecoveryStore
	store.WorkloadMountBindingOperationRecoveryStore
}

type WorkerStore interface {
	OperationRecoveryStore
	store.AuditOutboxDeliveryStore
}

type ExportReconcileStore interface {
	store.ExportSessionReconcileStore
}

type WorkloadMountStaleLeaseStore interface {
	store.WorkloadMountStaleLeaseReader
}

type RestoreReconciliationStore interface {
	restorereconcile.Store
	RestoreReconciliationWriteBlocked(ctx context.Context, namespaceID, repoID string) (bool, error)
}

type StoreFactory func(context.Context, string) (StoreHandle, error)
type JVSRunnerFactory func(config.WorkerRepoCreateRecoveryConfig) (repoexec.JVSRunner, error)
type StoragePurgerFactory func(config.WorkerRepoCreateRecoveryConfig) (repoexec.StoragePurger, error)
type AuditDelivererFactory func(config.WorkerAuditDeliveryConfig) (auditdelivery.Deliverer, error)

var ErrJVSRuntimeUnavailable = errors.New("jvs runtime unavailable")

func IsJVSRuntimeUnavailable(err error) bool {
	return errors.Is(err, ErrJVSRuntimeUnavailable)
}

type StoreHandle struct {
	Store                WorkerStore
	OperationStore       OperationRecoveryStore
	AuditStore           store.AuditOutboxDeliveryStore
	ExportReconcileStore ExportReconcileStore
	WorkloadMountStale   WorkloadMountStaleLeaseStore
	RestoreReconcile     RestoreReconciliationStore
	Close                func() error
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
	exportConfig := cfg.Worker.ExportSessionReconcile
	restoreConfig := cfg.Worker.RestoreReconciliation
	workloadMountStaleConfig := cfg.Worker.WorkloadMountStale
	auditConfig := cfg.Worker.AuditDelivery
	if !exportConfig.Enabled && !opConfig.Enabled && !workloadMountStaleConfig.Enabled && !restoreConfig.Enabled && !auditConfig.Enabled {
		return nil, errors.New("worker run-once requires export session reconcile, workload mount stale lease scan, operation recovery, or audit delivery to be enabled")
	}

	now := nowFunc(options.Clock)
	storeFactory := options.StoreFactory
	if storeFactory == nil {
		storeFactory = OpenPostgresOperationRecoveryStore
	}
	openCtx, cancel := context.WithTimeout(context.Background(), cfg.Worker.RunOnceTimeout)
	defer cancel()
	dsn, err := workerStoreDSN(exportConfig, opConfig, workloadMountStaleConfig, auditConfig, restoreConfig)
	if err != nil {
		return nil, err
	}
	handle, err := storeFactory(openCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open worker store: %w", err)
	}
	operationStore := handle.OperationStore
	auditStore := handle.AuditStore
	exportStore := handle.ExportReconcileStore
	workloadMountStaleStore := handle.WorkloadMountStale
	restoreReconcileStore := handle.RestoreReconcile
	if handle.Store != nil {
		if operationStore == nil {
			operationStore = handle.Store
		}
		if auditStore == nil {
			auditStore = handle.Store
		}
		if exportStore == nil {
			if candidate, ok := any(handle.Store).(ExportReconcileStore); ok {
				exportStore = candidate
			}
		}
		if workloadMountStaleStore == nil {
			if candidate, ok := any(handle.Store).(WorkloadMountStaleLeaseStore); ok {
				workloadMountStaleStore = candidate
			}
		}
		if restoreReconcileStore == nil {
			if candidate, ok := any(handle.Store).(RestoreReconciliationStore); ok {
				restoreReconcileStore = candidate
			}
		}
	}
	if exportConfig.Enabled && exportStore == nil {
		err := errors.New("worker export session reconcile store is required")
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
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
	if workloadMountStaleConfig.Enabled && workloadMountStaleStore == nil {
		err := errors.New("worker workload mount stale lease store is required")
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	if restoreConfig.Enabled && restoreReconcileStore == nil {
		err := errors.New("worker restore reconciliation store is required")
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}
	scopedStore := operationRecoveryStore{store: operationStore}
	workerConfig := worker.Config{}
	eventID := options.AuditEventID
	if eventID == nil {
		eventID = NewAuditEventID
	}

	if exportConfig.Enabled {
		workerConfig.ExportSessionReconcile = exportreconcile.New(exportreconcile.Config{
			Store:        exportStore,
			Owner:        exportConfig.Owner,
			Limit:        exportConfig.Limit,
			Clock:        now,
			AuditEventID: func() string { return eventID() },
		})
	}

	if workloadMountStaleConfig.Enabled {
		reconciler, err := workloadmount.NewStaleLeaseReconciler(workloadmount.StaleLeaseReconcilerConfig{
			Store: workloadMountStaleStore,
			Clock: now,
			Limit: workloadMountStaleConfig.Limit,
		})
		if err != nil {
			if handle.Close != nil {
				err = errors.Join(err, handle.Close())
			}
			return nil, err
		}
		workerConfig.WorkloadMountStale = reconciler
	}

	if restoreConfig.Enabled {
		workerConfig.RestoreReconciliation = restorereconcile.NewRunner(restorereconcile.Config{
			Store:             restoreReconcileStore,
			ExplicitlyEnabled: true,
			Owner:             restoreConfig.Owner,
			AuditEventID:      func() string { return eventID() },
			Clock:             now,
		})
	}

	if opConfig.Enabled {
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
		disableExecutor, err := namespaceexec.NewDisableExecutor(namespaceexec.DisableConfig{
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
		mountExecutor, err := mountbindingexec.NewExecutor(mountbindingexec.Config{
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
		executors := []recovery.OperationExecutor{volumeExecutor, namespaceExecutor, disableExecutor, bindingExecutor, mountExecutor}
		if opConfig.RepoCreate.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.RepoCreate)
			if err != nil {
				if !IsJVSRuntimeUnavailable(err) {
					if handle.Close != nil {
						err = errors.Join(err, handle.Close())
					}
					return nil, err
				}
			} else {
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
		if opConfig.TemplateCreate.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.TemplateCreate)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			templateJVS, ok := jvs.(repoexec.TemplateJVSRunner)
			if !ok {
				err = errors.New("template create jvs runner does not support repo clone")
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			templateCreateExecutor, err := repoexec.NewTemplateCreateExecutor(repoexec.TemplateConfig{
				Store:        scopedStore,
				JVSRunner:    templateJVS,
				Owner:        opConfig.Owner,
				Clock:        now,
				AuditEventID: func() string { return eventID() },
				VolumeRoots:  opConfig.TemplateCreate.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, templateCreateExecutor)
			scopedStore.templateCreateEnabled = true
		}
		if opConfig.TemplateClone.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.TemplateClone)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			templateJVS, ok := jvs.(repoexec.TemplateJVSRunner)
			if !ok {
				err = errors.New("template clone jvs runner does not support repo clone")
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			templateCloneExecutor, err := repoexec.NewTemplateCloneExecutor(repoexec.TemplateConfig{
				Store:        scopedStore,
				JVSRunner:    templateJVS,
				Owner:        opConfig.Owner,
				Clock:        now,
				AuditEventID: func() string { return eventID() },
				VolumeRoots:  opConfig.TemplateClone.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, templateCloneExecutor)
			scopedStore.templateCloneEnabled = true
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
		if opConfig.RestoreRun.Enabled {
			jvsFactory := options.JVSRunnerFactory
			if jvsFactory == nil {
				jvsFactory = NewJVSRunnerFromConfig
			}
			jvs, err := jvsFactory(opConfig.RestoreRun)
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			restoreRunJVS, ok := jvs.(repoexec.RestoreRunJVSRunner)
			if !ok {
				if handle.Close != nil {
					err = errors.Join(errors.New("restore run jvs runner does not support restore run"), handle.Close())
				} else {
					err = errors.New("restore run jvs runner does not support restore run")
				}
				return nil, err
			}
			restoreRunExecutor, err := repoexec.NewRestoreRunExecutor(repoexec.RestoreRunConfig{
				Store:        scopedStore,
				JVSRunner:    restoreRunJVS,
				Owner:        opConfig.Owner,
				Clock:        now,
				AuditEventID: func() string { return eventID() },
				VolumeRoots:  opConfig.RestoreRun.VolumeRoots,
			})
			if err != nil {
				if handle.Close != nil {
					err = errors.Join(err, handle.Close())
				}
				return nil, err
			}
			executors = append(executors, restoreRunExecutor)
			scopedStore.restoreRunEnabled = true
		}
		operationRecovery := recovery.NewOperationCoordinator(recovery.OperationConfig{
			Reader:        scopedStore,
			LeaseStore:    scopedStore,
			CommitStore:   scopedStore,
			Executor:      multiExecutor{executors: executors},
			Owner:         opConfig.Owner,
			LeaseDuration: opConfig.LeaseDuration,
			Limit:         opConfig.Limit,
			Clock:         now,
			AuditEventID:  func() string { return eventID() },
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

func workerStoreDSN(exportConfig config.WorkerExportSessionReconcileConfig, opConfig config.WorkerOperationRecoveryConfig, workloadMountStaleConfig config.WorkerWorkloadMountStaleLeaseConfig, auditConfig config.WorkerAuditDeliveryConfig, restoreConfig ...config.WorkerRestoreReconciliationConfig) (string, error) {
	if opConfig.Enabled && auditConfig.Enabled && opConfig.PostgresDSN != auditConfig.PostgresDSN {
		return "", errors.New("worker operation recovery and audit delivery must use the same postgres dsn")
	}
	if exportConfig.Enabled && opConfig.Enabled && exportConfig.PostgresDSN != opConfig.PostgresDSN {
		return "", errors.New("worker export session reconcile and operation recovery must use the same postgres dsn")
	}
	if exportConfig.Enabled && auditConfig.Enabled && exportConfig.PostgresDSN != auditConfig.PostgresDSN {
		return "", errors.New("worker export session reconcile and audit delivery must use the same postgres dsn")
	}
	if workloadMountStaleConfig.Enabled && opConfig.Enabled && workloadMountStaleConfig.PostgresDSN != opConfig.PostgresDSN {
		return "", errors.New("worker workload mount stale lease scan and operation recovery must use the same postgres dsn")
	}
	if workloadMountStaleConfig.Enabled && exportConfig.Enabled && workloadMountStaleConfig.PostgresDSN != exportConfig.PostgresDSN {
		return "", errors.New("worker workload mount stale lease scan and export session reconcile must use the same postgres dsn")
	}
	if workloadMountStaleConfig.Enabled && auditConfig.Enabled && workloadMountStaleConfig.PostgresDSN != auditConfig.PostgresDSN {
		return "", errors.New("worker workload mount stale lease scan and audit delivery must use the same postgres dsn")
	}
	if len(restoreConfig) > 0 && restoreConfig[0].Enabled {
		restore := restoreConfig[0]
		if opConfig.Enabled && restore.PostgresDSN != opConfig.PostgresDSN {
			return "", errors.New("worker restore reconciliation and operation recovery must use the same postgres dsn")
		}
		if exportConfig.Enabled && restore.PostgresDSN != exportConfig.PostgresDSN {
			return "", errors.New("worker restore reconciliation and export session reconcile must use the same postgres dsn")
		}
		if workloadMountStaleConfig.Enabled && restore.PostgresDSN != workloadMountStaleConfig.PostgresDSN {
			return "", errors.New("worker restore reconciliation and workload mount stale lease scan must use the same postgres dsn")
		}
		if auditConfig.Enabled && restore.PostgresDSN != auditConfig.PostgresDSN {
			return "", errors.New("worker restore reconciliation and audit delivery must use the same postgres dsn")
		}
	}
	if exportConfig.Enabled {
		return exportConfig.PostgresDSN, nil
	}
	if opConfig.Enabled {
		return opConfig.PostgresDSN, nil
	}
	if workloadMountStaleConfig.Enabled {
		return workloadMountStaleConfig.PostgresDSN, nil
	}
	if len(restoreConfig) > 0 && restoreConfig[0].Enabled {
		return restoreConfig[0].PostgresDSN, nil
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
	if err := verifyFileSHA256(cfg.JVSBinaryPath, config.JVSAcceptedLinuxAMD64SHA256); err != nil {
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
		return fmt.Errorf("%w: jvs binary verification failed", ErrJVSRuntimeUnavailable)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("%w: jvs binary verification failed", ErrJVSRuntimeUnavailable)
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
		err = errors.Join(exportSessionReconcileCountError(result.ExportSessionReconcile), workloadMountStaleLeaseCountError(result.WorkloadMountStale), operationRecoveryCountError(result.OperationRecovery), auditDeliveryCountError(result.AuditStaleRecovery, result.AuditDelivery))
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
		Store:                st,
		OperationStore:       st,
		AuditStore:           st,
		ExportReconcileStore: st,
		WorkloadMountStale:   st,
		RestoreReconcile:     st,
		Close:                db.Close,
	}, nil
}

func NewAuditEventID() string {
	counter := atomic.AddUint64(&eventCounter, 1)
	return fmt.Sprintf("evt_worker_%d_%d", time.Now().UTC().UnixNano(), counter)
}

func workerCapabilityMatrixExecutionOperationTypes() map[operations.OperationType]bool {
	return workerRuntimeOperationTypes()
}

func workerCapabilityMatrixRecoveryOperationTypes() map[operations.OperationType]bool {
	return workerRuntimeOperationTypes()
}

func workerCapabilityMatrixUnsupportedTerminalizationOperationTypes() map[operations.OperationType]bool {
	return workerCapabilityMatrixRecoveryOperationTypes()
}

func workerRuntimeOperationTypes() map[operations.OperationType]bool {
	operationTypes := map[operations.OperationType]bool{
		operations.OperationVolumeEnsure:              true,
		operations.OperationNamespaceUpsert:           true,
		operations.OperationNamespaceDisable:          true,
		operations.OperationNamespaceVolumeBindingPut: true,
		operations.OperationRepoCreate:                true,
		operations.OperationRepoArchive:               true,
		operations.OperationRepoRestoreArchived:       true,
		operations.OperationRepoDelete:                true,
		operations.OperationRepoRestoreTombstoned:     true,
		operations.OperationRepoPurge:                 true,
		operations.OperationSavePointCreate:           true,
		operations.OperationRestorePreview:            true,
		operations.OperationRestorePreviewDiscard:     true,
		operations.OperationRestoreRun:                true,
		operations.OperationTemplateCreate:            true,
		operations.OperationTemplateClone:             true,
		operations.OperationMountBindingCreate:        true,
		operations.OperationMountBindingStatusUpdate:  true,
		operations.OperationMountBindingHeartbeat:     true,
		operations.OperationMountBindingRelease:       true,
		operations.OperationMountBindingRevoke:        true,
	}
	return operationTypes
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
	restoreRunEnabled            bool
	templateCreateEnabled        bool
	templateCloneEnabled         bool
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
	namespaceDisableRecords, err := scoped.store.ListNamespaceDisableOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	bindingRecords, err := scoped.store.ListNamespaceVolumeBindingPutOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	mountRecords, err := scoped.store.ListWorkloadMountBindingOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records := append(volumeRecords, namespaceRecords...)
	records = append(records, namespaceDisableRecords...)
	records = append(records, bindingRecords...)
	records = append(records, mountRecords...)
	repoRecords, err := scoped.store.ListRepoCreateOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, repoRecords...)
	lifecycleRecords, err := scoped.store.ListRepoLifecycleOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, lifecycleRecords...)
	purgeRecords, err := scoped.store.ListRepoPurgeOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, purgeRecords...)
	savePointRecords, err := scoped.store.ListSavePointCreateOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, savePointRecords...)
	templateCreateRecords, err := scoped.store.ListTemplateCreateOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, templateCreateRecords...)
	templateCloneRecords, err := scoped.store.ListTemplateCloneOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, templateCloneRecords...)
	restorePreviewRecords, err := scoped.store.ListRestorePreviewOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, restorePreviewRecords...)
	restorePreviewDiscardRecords, err := scoped.store.ListRestorePreviewDiscardOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, restorePreviewDiscardRecords...)
	restoreRunRecords, err := scoped.store.ListRestoreRunOperationsForRecovery(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	records = append(records, restoreRunRecords...)
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
	record, err = scoped.store.AcquireNamespaceDisableOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireNamespaceVolumeBindingPutOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireWorkloadMountBindingOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireRepoCreateOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireRepoLifecycleOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireRepoPurgeOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireSavePointCreateOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireTemplateCreateOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireTemplateCloneOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireRestorePreviewOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	record, err = scoped.store.AcquireRestorePreviewDiscardOperationLease(ctx, operationID, request)
	if err == nil || !errors.Is(err, operations.ErrLeaseUnavailable) {
		return record, err
	}
	return scoped.store.AcquireRestoreRunOperationLease(ctx, operationID, request)
}

func (scoped operationRecoveryStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, fmt.Errorf("%w: worker operation recovery does not renew leases", operations.ErrInvalidLeaseRequest)
}

func (scoped operationRecoveryStore) UpdateOperationWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, fmt.Errorf("%w: worker operation recovery does not perform generic update-only commits", operations.ErrInvalidLeaseRequest)
}

func (scoped operationRecoveryStore) CommitOperationWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitOperationWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitVolumeEnsureWithLease(ctx context.Context, volume resources.Volume, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Volume, operations.OperationRecord, error) {
	return scoped.store.CommitVolumeEnsureWithLease(ctx, volume, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	return scoped.store.CommitNamespaceUpsertWithLease(ctx, namespace, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitNamespaceDisableWithLease(ctx context.Context, namespace resources.Namespace, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	return scoped.store.CommitNamespaceDisableWithLease(ctx, namespace, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitNamespaceVolumeBindingPutWithLease(ctx context.Context, binding resources.NamespaceVolumeBinding, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.NamespaceVolumeBinding, operations.OperationRecord, error) {
	return scoped.store.CommitNamespaceVolumeBindingPutWithLease(ctx, binding, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitWorkloadMountBindingCreateWithLease(ctx context.Context, binding workloadmount.Binding, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return scoped.store.CommitWorkloadMountBindingCreateWithLease(ctx, binding, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitWorkloadMountBindingStatusWithLease(ctx context.Context, mountBindingID string, status sessionstate.MountStatus, reason string, observedAt time.Time, leaseExpiresAt *time.Time, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return scoped.store.CommitWorkloadMountBindingStatusWithLease(ctx, mountBindingID, status, reason, observedAt, leaseExpiresAt, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitWorkloadMountBindingHeartbeatWithLease(ctx context.Context, mountBindingID string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return scoped.store.CommitWorkloadMountBindingHeartbeatWithLease(ctx, mountBindingID, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitWorkloadMountBindingReleaseWithLease(ctx context.Context, mountBindingID string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return scoped.store.CommitWorkloadMountBindingReleaseWithLease(ctx, mountBindingID, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitWorkloadMountBindingRevokeWithLease(ctx context.Context, mountBindingID string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return scoped.store.CommitWorkloadMountBindingRevokeWithLease(ctx, mountBindingID, record, owner, now, event)
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

func (scoped operationRecoveryStore) CommitTemplateCreateSucceededWithLease(ctx context.Context, template resources.Repo, sourceRepoID, sourceSavePointID, cloneHistoryMode string, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	return scoped.store.CommitTemplateCreateSucceededWithLease(ctx, template, sourceRepoID, sourceSavePointID, cloneHistoryMode, record, owner, now, event)
}

func (scoped operationRecoveryStore) MarkTemplateCreateWriterFencedWithLease(ctx context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error) {
	return scoped.store.MarkTemplateCreateWriterFencedWithLease(ctx, fence, record, owner, now)
}

func (scoped operationRecoveryStore) CommitTemplateCreateFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitTemplateCreateFailedWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitTemplateCloneSucceededWithLease(ctx context.Context, repo resources.Repo, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	return scoped.store.CommitTemplateCloneSucceededWithLease(ctx, repo, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitTemplateCloneFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitTemplateCloneFailedWithLease(ctx, record, owner, now, event)
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

func (scoped operationRecoveryStore) MarkRestoreRunWriterFencedWithLease(ctx context.Context, fence fences.Fence, record operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error) {
	return scoped.store.MarkRestoreRunWriterFencedWithLease(ctx, fence, record, owner, now)
}

func (scoped operationRecoveryStore) MarkRestoreRunConsumingWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	return scoped.store.MarkRestoreRunConsumingWithLease(ctx, record, owner, now)
}

func (scoped operationRecoveryStore) CommitRestoreRunSucceededWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return scoped.store.CommitRestoreRunSucceededWithLease(ctx, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitRestoreRunStalePreviewWithLease(ctx context.Context, plan restoreplan.Plan, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return scoped.store.CommitRestoreRunStalePreviewWithLease(ctx, plan, record, owner, now, event)
}

func (scoped operationRecoveryStore) CommitRestoreRunFailedWithLease(ctx context.Context, record operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	return scoped.store.CommitRestoreRunFailedWithLease(ctx, record, owner, now, event)
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

func exportSessionReconcileCountError(result exportreconcile.Result) error {
	if result.Failed == 0 {
		return nil
	}
	return fmt.Errorf("export session reconcile incomplete: failed=%d", result.Failed)
}

func workloadMountStaleLeaseCountError(result workloadmount.StaleLeaseResult) error {
	if result.Failed == 0 {
		return nil
	}
	return fmt.Errorf("workload mount stale lease scan incomplete: failed=%d kept_blocked=%d", result.Failed, result.KeptBlocked)
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
		if reason == "" && !strings.HasSuffix(support.Reason, "_operation") {
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
	_ store.OperationWorkerCommitStore                 = operationRecoveryStore{}
	_ store.VolumeEnsureOperationCommitStore           = operationRecoveryStore{}
	_ store.NamespaceUpsertOperationCommitStore        = operationRecoveryStore{}
	_ store.NamespaceDisableOperationCommitStore       = operationRecoveryStore{}
	_ store.NamespaceVolumeBindingOperationCommitStore = operationRecoveryStore{}
	_ store.WorkloadMountBindingOperationCommitStore   = operationRecoveryStore{}
	_ store.RepoCreateOperationCommitStore             = operationRecoveryStore{}
	_ store.RepoLifecycleOperationCommitStore          = operationRecoveryStore{}
	_ store.RepoPurgeOperationCommitStore              = operationRecoveryStore{}
	_ store.SavePointCreateOperationCommitStore        = operationRecoveryStore{}
	_ store.TemplateOperationCommitStore               = operationRecoveryStore{}
	_ store.RestorePreviewOperationCommitStore         = operationRecoveryStore{}
	_ store.RestorePreviewDiscardOperationCommitStore  = operationRecoveryStore{}
	_ store.RestoreRunOperationCommitStore             = operationRecoveryStore{}
	_ recovery.OperationExecutor                       = multiExecutor{}
)
