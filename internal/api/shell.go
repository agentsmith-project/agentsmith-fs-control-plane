package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

func NewNeutralShell() http.Handler {
	return NewNeutralShellWithLoggerAndAuditSink(nil, nil)
}

func NewNeutralShellWithLogger(logger *slog.Logger) http.Handler {
	return NewNeutralShellWithLoggerAndAuditSink(logger, nil)
}

func NewNeutralShellWithAuditSink(sink audit.Sink) http.Handler {
	return NewNeutralShellWithLoggerAndAuditSink(nil, sink)
}

func NewNeutralShellWithLoggerAndAuditSink(logger *slog.Logger, sink audit.Sink) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", requestLogHandler(HealthHandler(), logger, slog.LevelInfo, "afscp.health", "health request", "/healthz", ""))
	mux.Handle("/readyz", requestLogHandler(ReadinessHandler(NeutralReadiness()), logger, slog.LevelInfo, "afscp.readiness", "readiness request", "/readyz", ""))
	mux.Handle("/", neutralFallbackHandler(logger, sink))
	return mux
}

type InternalAPIShellConfig struct {
	Logger                         *slog.Logger
	AuditSink                      audit.Sink
	PrincipalResolver              PrincipalResolver
	NamespaceBindingReader         NamespaceVolumeBindingReader
	NamespaceReader                NamespaceReader
	RepoReader                     RepoReader
	VolumeReader                   VolumeReader
	VolumeBackendHealthProbe       VolumeBackendHealthProbe
	WorkloadMountBindingReader     WorkloadMountBindingReader
	WorkloadMountPlanReader        WorkloadMountPlanReader
	ExportStore                    ExportStore
	RepoFenceReader                RepoFenceReader
	SavePointHistoryReader         SavePointHistoryReader
	SavePointMutationGate          RepoJVSMutationGateReader
	SavePointHistoryJVSRunner      JVSHistoryRunner
	SavePointHistoryVolumeRoots    map[string]string
	OperationInspectionReader      OperationInspectionStoreReader
	RepoCreateIntakeStore          RepoCreateOperationIntakeStore
	TemplateIntakeStore            TemplateOperationIntakeStore
	DeploymentGlobalPolicy         AllowedCallerPolicy
	DeploymentNamespacePolicy      AllowedCallerPolicy
	DeploymentGlobalCallers        []auth.AllowedCaller
	DeploymentNamespaceCallers     []auth.AllowedCaller
	OperationIntakeStore           OperationIntakeStore
	GenerateOperationID            OperationIDGenerator
	Now                            func() time.Time
	WebDAVExportPublicBaseURL      string
	Readiness                      ReadinessResponse
	ReadinessProvider              func(context.Context) ReadinessResponse
	WebDAVExportAdmissionDisabled  bool
	WorkloadMountAdmissionDisabled bool
}

func NewInternalAPIShell(config InternalAPIShellConfig) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", requestLogHandler(HealthHandler(), config.Logger, slog.LevelInfo, "afscp.health", "health request", "/healthz", ""))
	readiness := config.Readiness
	if len(readiness.Capabilities) == 0 {
		readiness = NeutralReadiness()
	}
	readinessHandler := ReadinessHandler(readiness)
	if config.ReadinessProvider != nil {
		readinessHandler = ReadinessHandlerFunc(config.ReadinessProvider)
	}
	mux.Handle("/readyz", requestLogHandler(readinessHandler, config.Logger, slog.LevelInfo, "afscp.readiness", "readiness request", "/readyz", ""))

	volumeHandler := EnsureVolumeHandler(EnsureVolumeHandlerConfig{
		IntakeStore:       config.OperationIntakeStore,
		PrincipalResolver: config.PrincipalResolver,
		DeploymentPolicy: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	volumeHandler = requestLogHandler(volumeHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/volumes/{volumeId}:ensure", "ensureVolume")
	volumeHealthHandler := VolumeHealthHandler(VolumeHealthHandlerConfig{
		Reader:            config.VolumeReader,
		BackendProbe:      config.VolumeBackendHealthProbe,
		PrincipalResolver: config.PrincipalResolver,
		DeploymentPolicy: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		Now:       config.Now,
		AuditSink: config.AuditSink,
	})
	volumeHealthHandler = requestLogHandler(volumeHealthHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/volumes/{volumeId}/health", "getVolumeHealth")

	createRepoHandler := CreateRepoHandler(CreateRepoHandlerConfig{
		IntakeStore:       config.RepoCreateIntakeStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	createRepoHandler = requestLogHandler(createRepoHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos", "createRepo")

	repoReadHandler := RepoReadHandler(RepoReadHandlerConfig{
		Reader:            config.RepoReader,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		AuditSink: config.AuditSink,
	})
	getRepoHandler := requestLogHandler(repoReadHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}", "getRepo")
	listReposHandler := requestLogHandler(repoReadHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos", "listRepos")

	repoLifecycleHandler := RepoLifecycleHandler(RepoLifecycleHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		FenceReader:       config.RepoFenceReader,
		IntakeStore:       config.OperationIntakeStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		BreakGlassCallers: deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
		OperationID:       config.GenerateOperationID,
		Now:               config.Now,
		AuditSink:         config.AuditSink,
	})
	archiveRepoHandler := requestLogHandler(repoLifecycleHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}:archive", "archiveRepo")
	restoreArchivedRepoHandler := requestLogHandler(repoLifecycleHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}:restore-archived", "restoreArchivedRepo")
	deleteRepoHandler := requestLogHandler(repoLifecycleHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}:delete", "deleteRepo")
	restoreTombstonedRepoHandler := requestLogHandler(repoLifecycleHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}:restore-tombstoned", "restoreTombstonedRepo")
	purgeRepoHandler := requestLogHandler(repoLifecycleHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}:purge", "purgeRepo")

	savePointHistoryReader := config.SavePointHistoryReader
	if savePointHistoryReader == nil && config.SavePointHistoryJVSRunner != nil {
		if reader, err := NewJVSBackedSavePointHistoryReader(JVSBackedSavePointHistoryReaderConfig{
			RepoReader:   config.RepoReader,
			VolumeReader: config.VolumeReader,
			JVSRunner:    config.SavePointHistoryJVSRunner,
			VolumeRoots:  config.SavePointHistoryVolumeRoots,
		}); err == nil {
			savePointHistoryReader = reader
		}
	}
	savePointMutationGate := config.SavePointMutationGate
	if savePointMutationGate == nil {
		if typed, ok := config.OperationIntakeStore.(RepoJVSMutationGateReader); ok {
			savePointMutationGate = typed
		}
	}
	var restorePreviewIntakeStore RestorePreviewOperationIntakeStore
	if typed, ok := config.OperationIntakeStore.(RestorePreviewOperationIntakeStore); ok {
		restorePreviewIntakeStore = typed
	}
	var restorePreviewDiscardIntakeStore RestorePreviewDiscardOperationIntakeStore
	if typed, ok := config.OperationIntakeStore.(RestorePreviewDiscardOperationIntakeStore); ok {
		restorePreviewDiscardIntakeStore = typed
	}
	var restoreRunIntakeStore RestoreRunOperationIntakeStore
	if typed, ok := config.OperationIntakeStore.(RestoreRunOperationIntakeStore); ok {
		restoreRunIntakeStore = typed
	}
	var operationLookupStore OperationIdempotencyLookupStore
	if typed, ok := config.OperationIntakeStore.(OperationIdempotencyLookupStore); ok {
		operationLookupStore = typed
	}
	exportStore := config.ExportStore
	if exportStore == nil {
		if typed, ok := config.OperationIntakeStore.(ExportStore); ok {
			exportStore = typed
		}
	}
	var restorePreviewPlanReader RestorePreviewPlanGateReader
	if typed, ok := config.OperationIntakeStore.(RestorePreviewPlanGateReader); ok {
		restorePreviewPlanReader = typed
	}
	var restoreRunMetadataReader RestoreRunMetadataReader
	if typed, ok := config.OperationIntakeStore.(RestoreRunMetadataReader); ok {
		restoreRunMetadataReader = typed
	}
	var restorePreviewDiscardMetadataReader RestorePreviewDiscardMetadataReader
	if typed, ok := config.OperationIntakeStore.(RestorePreviewDiscardMetadataReader); ok {
		restorePreviewDiscardMetadataReader = typed
	}
	var restoreRunGate RestoreRunIntakeGateReader
	if typed, ok := config.OperationIntakeStore.(RestoreRunIntakeGateReader); ok {
		restoreRunGate = typed
	}
	templateIntakeStore := config.TemplateIntakeStore
	if templateIntakeStore == nil {
		if typed, ok := config.OperationIntakeStore.(TemplateOperationIntakeStore); ok {
			templateIntakeStore = typed
		}
	}
	mountReader := config.WorkloadMountBindingReader
	if mountReader == nil {
		if typed, ok := config.OperationIntakeStore.(WorkloadMountBindingReader); ok {
			mountReader = typed
		}
	}
	mountPlanReader := config.WorkloadMountPlanReader
	if mountPlanReader == nil {
		if typed, ok := config.OperationIntakeStore.(WorkloadMountPlanReader); ok {
			mountPlanReader = typed
		}
	}

	savePointHandler := SavePointHandler(SavePointHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		FenceReader:       config.RepoFenceReader,
		HistoryReader:     savePointHistoryReader,
		MutationGate:      savePointMutationGate,
		IntakeStore:       config.OperationIntakeStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	createSavePointHandler := requestLogHandler(savePointHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}/save-points", "createSavePoint")
	listSavePointsHandler := requestLogHandler(savePointHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}/save-points", "listSavePoints")

	restorePreviewHandler := RestorePreviewHandler(RestorePreviewHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		FenceReader:       config.RepoFenceReader,
		MutationGate:      savePointMutationGate,
		RestorePlanReader: restorePreviewPlanReader,
		IntakeStore:       restorePreviewIntakeStore,
		IntakeLookupStore: operationLookupStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	restorePreviewHandler = requestLogHandler(restorePreviewHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}/restore-preview", "restorePreview")

	operationInspectionHandler := OperationInspectionHandler(OperationInspectionHandlerConfig{
		StoreReader: config.OperationInspectionReader,
		StoredNamespaceAuthorizer: operationInspectionNamespaceBindingAuthorizer{
			Reader: config.NamespaceBindingReader,
		},
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: OperationInspectionPreflightPolicy{
			DeploymentGlobal: deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
		},
		AuditSink: config.AuditSink,
	})
	operationInspectionHandler = requestLogHandler(operationInspectionHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/operations/{operationId}", "getOperation")

	restorePreviewDiscardHandler := RestorePreviewDiscardHandler(RestorePreviewDiscardHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		FenceReader:       config.RepoFenceReader,
		MetadataReader:    restorePreviewDiscardMetadataReader,
		IntakeStore:       restorePreviewDiscardIntakeStore,
		IntakeLookupStore: operationLookupStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	restorePreviewDiscardHandler = requestLogHandler(restorePreviewDiscardHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}/restore-preview:discard", "restorePreviewDiscard")

	restoreRunHandler := RestoreRunHandler(RestoreRunHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		FenceReader:       config.RepoFenceReader,
		MetadataReader:    restoreRunMetadataReader,
		RunGate:           restoreRunGate,
		IntakeStore:       restoreRunIntakeStore,
		IntakeLookupStore: operationLookupStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	restoreRunHandler = requestLogHandler(restoreRunHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}/restore-run", "restoreRun")

	repoTemplateHandler := RepoTemplateHandler(RepoTemplateHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		FenceReader:       config.RepoFenceReader,
		MutationGate:      savePointMutationGate,
		IntakeStore:       templateIntakeStore,
		IntakeLookupStore: operationLookupStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	createRepoTemplateHandler := requestLogHandler(repoTemplateHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repo-templates", "createRepoTemplate")
	cloneRepoTemplateHandler := requestLogHandler(repoTemplateHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repo-templates/{templateId}:clone", "cloneRepoTemplate")

	bindingHandler := NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
		Reader:            config.NamespaceBindingReader,
		IntakeStore:       config.OperationIntakeStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	getBindingHandler := requestLogHandler(bindingHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/namespaces/{namespaceId}/volume-binding", "getNamespaceVolumeBinding")
	putBindingHandler := requestLogHandler(bindingHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/namespaces/{namespaceId}/volume-binding", "putNamespaceVolumeBinding")
	upsertNamespaceHandler := NamespaceUpsertHandler(NamespaceUpsertHandlerConfig{
		IntakeStore:       config.OperationIntakeStore,
		PrincipalResolver: config.PrincipalResolver,
		DeploymentPolicy: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	upsertNamespaceHandler = requestLogHandler(upsertNamespaceHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/namespaces/{namespaceId}", "upsertNamespace")
	disableNamespaceHandler := DisableNamespaceHandler(DisableNamespaceHandlerConfig{
		IntakeStore:       config.OperationIntakeStore,
		PrincipalResolver: config.PrincipalResolver,
		DeploymentPolicy: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID: config.GenerateOperationID,
		Now:         config.Now,
		AuditSink:   config.AuditSink,
	})
	disableNamespaceHandler = requestLogHandler(disableNamespaceHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/namespaces/{namespaceId}:disable", "disableNamespace")

	workloadMountHandler := WorkloadMountHandler(WorkloadMountHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		VolumeReader:      config.VolumeReader,
		FenceReader:       config.RepoFenceReader,
		MountReader:       mountReader,
		PlanReader:        mountPlanReader,
		IntakeStore:       config.OperationIntakeStore,
		IntakeLookupStore: operationLookupStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID:       config.GenerateOperationID,
		Now:               config.Now,
		AdmissionDisabled: config.WorkloadMountAdmissionDisabled,
		AuditSink:         config.AuditSink,
	})
	createWorkloadMountHandler := requestLogHandler(workloadMountHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}/workload-mount-bindings", "createWorkloadMountBinding")
	getWorkloadMountHandler := requestLogHandler(workloadMountHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/workload-mount-bindings/{mountBindingId}", "getWorkloadMountBinding")
	updateWorkloadMountStatusHandler := requestLogHandler(workloadMountHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/workload-mount-bindings/{mountBindingId}/status", "updateWorkloadMountBindingStatus")
	getOrchestratorMountPlanHandler := requestLogHandler(workloadMountHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/workload-mount-bindings/{mountBindingId}/orchestrator-plan", "getOrchestratorMountPlan")
	heartbeatWorkloadMountHandler := requestLogHandler(workloadMountHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/workload-mount-bindings/{mountBindingId}:heartbeat", "heartbeatWorkloadMountBinding")
	releaseWorkloadMountHandler := requestLogHandler(workloadMountHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/workload-mount-bindings/{mountBindingId}:release", "releaseWorkloadMountBinding")
	revokeWorkloadMountHandler := requestLogHandler(workloadMountHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/workload-mount-bindings/{mountBindingId}:revoke", "revokeWorkloadMountBinding")

	exportHandler := ExportHandler(ExportHandlerConfig{
		RepoReader:        config.RepoReader,
		NamespaceReader:   config.NamespaceReader,
		BindingReader:     config.NamespaceBindingReader,
		VolumeReader:      config.VolumeReader,
		FenceReader:       config.RepoFenceReader,
		Store:             exportStore,
		PrincipalResolver: config.PrincipalResolver,
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentGlobal:    deploymentPolicyOrStatic(config.DeploymentGlobalPolicy, config.DeploymentGlobalCallers),
			DeploymentNamespace: deploymentPolicyOrStatic(config.DeploymentNamespacePolicy, config.DeploymentNamespaceCallers),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: config.NamespaceBindingReader},
		},
		OperationID:   config.GenerateOperationID,
		Now:           config.Now,
		PublicBaseURL: config.WebDAVExportPublicBaseURL,
		AuditSink:     config.AuditSink,
	})
	createExportHandler := requestLogHandler(exportHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/repos/{repoId}/exports", "createExport")
	getExportHandler := requestLogHandler(exportHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/exports/{exportId}", "getExport")
	revokeExportHandler := requestLogHandler(exportHandler, config.Logger, slog.LevelInfo, "afscp.request", "request handled", "/internal/v1/exports/{exportId}", "revokeExport")

	// This shell enables only the implemented internal API subset. Known contract
	// routes without handlers remain fail-closed instead of being silently absent.
	fallback := internalAPIFallbackHandler(config.Logger, config.AuditSink)
	implemented := map[string]http.Handler{
		"createRepo":                       createRepoHandler,
		"createSavePoint":                  createSavePointHandler,
		"archiveRepo":                      archiveRepoHandler,
		"restoreArchivedRepo":              restoreArchivedRepoHandler,
		"deleteRepo":                       deleteRepoHandler,
		"disableNamespace":                 disableNamespaceHandler,
		"ensureVolume":                     volumeHandler,
		"getNamespaceVolumeBinding":        getBindingHandler,
		"getRepo":                          getRepoHandler,
		"getWorkloadMountBinding":          getWorkloadMountHandler,
		"heartbeatWorkloadMountBinding":    heartbeatWorkloadMountHandler,
		"listRepos":                        listReposHandler,
		"purgeRepo":                        purgeRepoHandler,
		"restorePreview":                   restorePreviewHandler,
		"restorePreviewDiscard":            restorePreviewDiscardHandler,
		"restoreRun":                       restoreRunHandler,
		"createRepoTemplate":               createRepoTemplateHandler,
		"cloneRepoTemplate":                cloneRepoTemplateHandler,
		"restoreTombstonedRepo":            restoreTombstonedRepoHandler,
		"releaseWorkloadMountBinding":      releaseWorkloadMountHandler,
		"revokeWorkloadMountBinding":       revokeWorkloadMountHandler,
		"getOperation":                     operationInspectionHandler,
		"listSavePoints":                   listSavePointsHandler,
		"putNamespaceVolumeBinding":        putBindingHandler,
		"upsertNamespace":                  upsertNamespaceHandler,
		"updateWorkloadMountBindingStatus": updateWorkloadMountStatusHandler,
	}
	if !config.WorkloadMountAdmissionDisabled || operationLookupStore != nil {
		implemented["createWorkloadMountBinding"] = createWorkloadMountHandler
	}
	if !config.WorkloadMountAdmissionDisabled {
		implemented["getOrchestratorMountPlan"] = getOrchestratorMountPlanHandler
	}
	if config.VolumeReader != nil {
		implemented["getVolumeHealth"] = volumeHealthHandler
	}
	if exportStore != nil && config.RepoReader != nil && config.NamespaceReader != nil && config.NamespaceBindingReader != nil && config.VolumeReader != nil && config.RepoFenceReader != nil {
		if !config.WebDAVExportAdmissionDisabled {
			implemented["createExport"] = createExportHandler
		}
		implemented["getExport"] = getExportHandler
		implemented["revokeExport"] = revokeExportHandler
	}
	mux.Handle("/", routeDispatchHandler(implemented, fallback))
	return mux
}

func deploymentPolicyOrStatic(policy AllowedCallerPolicy, callers []auth.AllowedCaller) AllowedCallerPolicy {
	if policy != nil {
		return policy
	}
	if callers != nil {
		static := NewStaticAllowedCallerPolicy(callers)
		return static
	}
	return nil
}

func routeDispatchHandler(implemented map[string]http.Handler, fallback http.Handler) http.Handler {
	if fallback == nil {
		fallback = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadata, ok := RouteMetadataForRequest(r)
		if !ok {
			fallback.ServeHTTP(w, r)
			return
		}
		handler := implemented[metadata.OperationID]
		if handler == nil {
			fallback.ServeHTTP(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func CapabilityDeniedHandler() http.Handler {
	return capabilityDeniedHandlerWithMessage("storage-backed API capabilities are disabled in neutral shell")
}

func internalAPICapabilityDeniedHandler() http.Handler {
	return capabilityDeniedHandlerWithMessage("requested internal API capability is not enabled")
}

func capabilityDeniedHandlerWithMessage(message string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope := NewErrorEnvelope(
			CodeCapabilityDenied,
			message,
			false,
			CorrelationIDFromRequest(r),
			nil,
			map[string]any{
				"disabled_capabilities": []string{
					CapabilityStorage,
					CapabilityJVS,
					CapabilityWebDAVExport,
					CapabilityWorkloadMount,
				},
			},
		)

		_ = WriteErrorEnvelope(w, http.StatusForbidden, envelope)
	})
}

func PathDeniedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope := NewErrorEnvelope(
			CodePathDenied,
			"route is not available",
			false,
			CorrelationIDFromRequest(r),
			nil,
			map[string]any{"route": "unmatched"},
		)

		_ = WriteErrorEnvelope(w, http.StatusNotFound, envelope)
	})
}

func neutralFallbackHandler(logger *slog.Logger, sink audit.Sink) http.Handler {
	return fallbackHandler(logger, sink, CapabilityDeniedHandler())
}

func internalAPIFallbackHandler(logger *slog.Logger, sink audit.Sink) http.Handler {
	return fallbackHandler(logger, sink, internalAPICapabilityDeniedHandler())
}

func fallbackHandler(logger *slog.Logger, sink audit.Sink, capabilityDenied http.Handler) http.Handler {
	pathDenied := PathDeniedHandler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if metadata, ok := RouteMetadataForRequest(r); ok {
			serveDeniedWithRequestLogAndAudit(
				w,
				r,
				capabilityDenied,
				logger,
				sink,
				slog.LevelWarn,
				"afscp.request.capability_denied",
				"capability denied",
				metadata.Path,
				metadata.OperationID,
				audit.EventTypeCapabilityDenied,
				CodeCapabilityDenied,
				map[string]any{
					"disabled_capabilities": []string{
						CapabilityStorage,
						CapabilityJVS,
						CapabilityWebDAVExport,
						CapabilityWorkloadMount,
					},
				},
			)
			return
		}

		serveDeniedWithRequestLogAndAudit(
			w,
			r,
			pathDenied,
			logger,
			sink,
			slog.LevelWarn,
			"afscp.request.path_denied",
			"path denied",
			"unmatched",
			"",
			audit.EventTypePathDenied,
			CodePathDenied,
			nil,
		)
	})
}

func requestLogHandler(next http.Handler, logger *slog.Logger, level slog.Level, event string, message string, route string, operationID string) http.Handler {
	if logger == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveWithRequestLog(w, r, next, logger, level, event, message, route, operationID)
	})
}

func serveWithRequestLog(w http.ResponseWriter, r *http.Request, next http.Handler, logger *slog.Logger, level slog.Level, event string, message string, route string, operationID string) {
	if logger == nil {
		next.ServeHTTP(w, r)
		return
	}

	recorder := &responseStatusRecorder{ResponseWriter: w}
	next.ServeHTTP(recorder, r)

	fields := requestLogFields(r, route, operationID, recorder.statusCode())
	observability.LogEvent(r.Context(), logger, level, event, message, fields)
}

func serveDeniedWithRequestLogAndAudit(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	logger *slog.Logger,
	sink audit.Sink,
	level slog.Level,
	logEvent string,
	message string,
	route string,
	operationID string,
	auditType audit.EventType,
	code ErrorCode,
	auditDetails map[string]any,
) {
	metadata := RouteMetadata{Path: route, OperationID: operationID}
	recorder := &responseStatusRecorder{ResponseWriter: w}
	next.ServeHTTP(recorder, r)

	status := recorder.statusCode()
	if logger != nil {
		fields := requestLogFields(r, route, operationID, status)
		observability.LogEvent(r.Context(), logger, level, logEvent, message, fields)
	}

	emitDeniedAuditEvent(r.Context(), sink, r, deniedAuditEvent{
		Type:    auditType,
		Route:   metadata,
		Status:  status,
		Code:    code,
		Reason:  message,
		Details: auditDetails,
	})
}

func requestLogFields(r *http.Request, route string, operationID string, status int) map[string]any {
	fields := map[string]any{
		"correlation_id": CorrelationIDFromRequest(r),
		"method":         "",
		"path":           "",
		"route":          route,
		"status":         status,
	}

	if r != nil {
		fields["method"] = r.Method
		if r.URL != nil {
			fields["path"] = r.URL.Path
		}
	}
	if operationID != "" {
		fields["operation_id"] = operationID
	}
	return fields
}

type responseStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *responseStatusRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseStatusRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(body)
}

func (recorder *responseStatusRecorder) statusCode() int {
	if recorder.status == 0 {
		return http.StatusOK
	}
	return recorder.status
}
