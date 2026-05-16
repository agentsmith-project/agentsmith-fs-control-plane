package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

const (
	defaultServiceName                    = "afscp"
	defaultListenAddr                     = "127.0.0.1:8080"
	defaultEnvironment                    = "development"
	defaultReadinessProfile               = ReadinessProfileRuntime
	defaultOperationRecoveryLimit         = 10
	defaultOperationRecoveryLeaseDuration = 5 * time.Minute
	defaultWorkerRunOnceTimeout           = 30 * time.Second
	defaultExportSessionReconcileLimit    = 10
	defaultRestoreReconciliationLimit     = 10
	defaultWorkloadMountStaleLeaseLimit   = 10
	defaultAuditDeliveryLimit             = 10
	defaultAuditDeliveryMaxAttempts       = 5
	defaultAuditDeliveryRetryBackoff      = time.Minute
	defaultAuditDeliveryStaleThreshold    = 5 * time.Minute
	defaultAuditDeliveryTimeout           = 10 * time.Second
)

const (
	JVSAcceptedReleaseVersion      = "pre-ga-local-afscp-direct-2026-05-16"
	JVSAcceptedLinuxAMD64AssetName = "afscp-jvs-direct-local-linux-amd64"
	JVSAcceptedLinuxAMD64SHA256    = "affa86a08dbb2195f594be0be01e9c3f128806f75d04826030afbe4ba283f2e2"
	JVSAcceptedSourceRef           = "jvs@main:eb026cc48efb57ef64c9f3e482f0011b9232701b"
)

const (
	ReadinessProfileRuntime = "runtime"
	ReadinessProfileGA      = "ga"
)

// Source supplies configuration values without tying tests to process env.
type Source interface {
	Lookup(key string) (string, bool)
}

// MapSource is a small in-memory Source for tests and local composition.
type MapSource map[string]string

func (m MapSource) Lookup(key string) (string, bool) {
	if m == nil {
		return "", false
	}
	value, ok := m[key]
	return value, ok
}

// EnvSource loads values from the current process environment.
type EnvSource struct{}

func (EnvSource) Lookup(key string) (string, bool) {
	return os.LookupEnv(key)
}

type Config struct {
	ServiceName      string
	ListenAddr       string
	Environment      string
	ReadinessProfile string
	Capabilities     Capabilities
	Worker           WorkerConfig
	API              APIConfig
	ExportGateway    ExportGatewayConfig
}

type Capabilities struct {
	Storage      Capability
	JVS          Capability
	WebDAV       Capability
	Mount        Capability
	RepoTemplate Capability
	RepoPurge    Capability
}

type Capability struct {
	Enabled bool
	Ready   bool
}

type APIConfig struct {
	Mode                              string
	PostgresDSN                       string
	ServiceTokens                     string
	WebDAVExportPublicBaseURL         string
	DeploymentGlobalAllowedCallers    string
	DeploymentNamespaceAllowedCallers string
	VolumeRoots                       map[string]string
	WorkloadMountRuntimeSecretRefs    map[string]SecretRef
	SavePointHistory                  WorkerRepoCreateRecoveryConfig
}

type SecretRef struct {
	Namespace string
	Name      string
}

type ExportGatewayConfig struct {
	ListenAddr  string
	PostgresDSN string
	VolumeRoots map[string]string
	Prefix      string
}

type WorkerConfig struct {
	RunOnceTimeout         time.Duration
	OperationRecovery      WorkerOperationRecoveryConfig
	ExportSessionReconcile WorkerExportSessionReconcileConfig
	RestoreReconciliation  WorkerRestoreReconciliationConfig
	WorkloadMountStale     WorkerWorkloadMountStaleLeaseConfig
	AuditDelivery          WorkerAuditDeliveryConfig
}

type WorkerOperationRecoveryConfig struct {
	Enabled        bool
	PostgresDSN    string
	Owner          string
	Limit          int
	LeaseDuration  time.Duration
	RepoCreate     WorkerRepoCreateRecoveryConfig
	RepoLifecycle  WorkerRepoCreateRecoveryConfig
	RepoPurge      WorkerRepoCreateRecoveryConfig
	SavePoint      WorkerRepoCreateRecoveryConfig
	TemplateCreate WorkerRepoCreateRecoveryConfig
	TemplateClone  WorkerRepoCreateRecoveryConfig
	Restore        WorkerRepoCreateRecoveryConfig
}

type WorkerRepoCreateRecoveryConfig struct {
	Enabled                   bool
	JVSBinaryPath             string
	JVSBinarySHA256           string
	JVSCWD                    string
	JVSDirectRestoreRequired  bool
	JVSDirectRestoreSourceRef string
	VolumeRoots               map[string]string
}

type WorkerExportSessionReconcileConfig struct {
	Enabled     bool
	PostgresDSN string
	Owner       string
	Limit       int
}

type WorkerRestoreReconciliationConfig struct {
	Enabled     bool
	PostgresDSN string
	Owner       string
	Limit       int
}

type WorkerWorkloadMountStaleLeaseConfig struct {
	Enabled     bool
	PostgresDSN string
	Limit       int
}

type WorkerAuditDeliveryConfig struct {
	Enabled        bool
	PostgresDSN    string
	Owner          string
	Limit          int
	MaxAttempts    int
	RetryBackoff   time.Duration
	StaleThreshold time.Duration
	SinkKind       string
	Endpoint       string
	BearerToken    string
	Timeout        time.Duration
}

func (c Capability) Available() bool {
	return c.Enabled && c.Ready
}

func LoadFromEnv() (Config, error) {
	return Load(EnvSource{})
}

func Load(source Source) (Config, error) {
	cfg := Config{
		ServiceName:      defaultServiceName,
		ListenAddr:       defaultListenAddr,
		Environment:      defaultEnvironment,
		ReadinessProfile: defaultReadinessProfile,
		API: APIConfig{
			Mode:                           "neutral",
			VolumeRoots:                    map[string]string{},
			WorkloadMountRuntimeSecretRefs: map[string]SecretRef{},
		},
		ExportGateway: ExportGatewayConfig{
			Prefix:      "/e/",
			VolumeRoots: map[string]string{},
		},
		Worker: WorkerConfig{
			RunOnceTimeout: defaultWorkerRunOnceTimeout,
			OperationRecovery: WorkerOperationRecoveryConfig{
				Limit:         defaultOperationRecoveryLimit,
				LeaseDuration: defaultOperationRecoveryLeaseDuration,
			},
			ExportSessionReconcile: WorkerExportSessionReconcileConfig{
				Limit: defaultExportSessionReconcileLimit,
			},
			RestoreReconciliation: WorkerRestoreReconciliationConfig{
				Limit: defaultRestoreReconciliationLimit,
			},
			WorkloadMountStale: WorkerWorkloadMountStaleLeaseConfig{
				Limit: defaultWorkloadMountStaleLeaseLimit,
			},
			AuditDelivery: WorkerAuditDeliveryConfig{
				Limit:          defaultAuditDeliveryLimit,
				MaxAttempts:    defaultAuditDeliveryMaxAttempts,
				RetryBackoff:   defaultAuditDeliveryRetryBackoff,
				StaleThreshold: defaultAuditDeliveryStaleThreshold,
				Timeout:        defaultAuditDeliveryTimeout,
			},
		},
	}

	cfg.ServiceName = strings.ToLower(valueOrDefault(source, "AFSCP_SERVICE_NAME", cfg.ServiceName))
	cfg.ListenAddr = valueOrDefault(source, "AFSCP_LISTEN_ADDR", cfg.ListenAddr)
	cfg.Environment = strings.ToLower(valueOrDefault(source, "AFSCP_ENVIRONMENT", cfg.Environment))
	cfg.ReadinessProfile = strings.ToLower(valueOrDefault(source, "AFSCP_READINESS_PROFILE", cfg.ReadinessProfile))
	if err := validateReadinessProfile(cfg.ReadinessProfile); err != nil {
		return Config{}, err
	}

	var err error
	if cfg.Capabilities.Storage, err = loadCapability(source, "AFSCP_STORAGE"); err != nil {
		return Config{}, err
	}
	if cfg.Capabilities.JVS, err = loadCapability(source, "AFSCP_JVS"); err != nil {
		return Config{}, err
	}
	if cfg.Capabilities.WebDAV, err = loadCapability(source, "AFSCP_WEBDAV"); err != nil {
		return Config{}, err
	}
	if cfg.Capabilities.Mount, err = loadCapability(source, "AFSCP_MOUNT"); err != nil {
		return Config{}, err
	}
	if cfg.Capabilities.RepoTemplate, err = loadCapability(source, "AFSCP_REPO_TEMPLATE"); err != nil {
		return Config{}, err
	}
	if cfg.Capabilities.RepoPurge, err = loadCapability(source, "AFSCP_REPO_PURGE"); err != nil {
		return Config{}, err
	}
	if cfg.Worker, err = loadWorkerConfig(source, cfg.Worker); err != nil {
		return Config{}, err
	}
	if cfg.API, err = loadAPIConfig(source, cfg.API, cfg.Capabilities); err != nil {
		return Config{}, err
	}
	if cfg.ExportGateway, err = loadExportGatewayConfig(source, cfg.ExportGateway, cfg.ListenAddr); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func valueOrDefault(source Source, key string, fallback string) string {
	if source == nil {
		return fallback
	}
	value, ok := source.Lookup(key)
	if !ok {
		return fallback
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func loadCapability(source Source, prefix string) (Capability, error) {
	enabled, err := boolValue(source, prefix+"_ENABLED")
	if err != nil {
		return Capability{}, err
	}
	ready, err := boolValue(source, prefix+"_READY")
	if err != nil {
		return Capability{}, err
	}
	if !enabled {
		ready = false
	}
	return Capability{Enabled: enabled, Ready: ready}, nil
}

func validateReadinessProfile(profile string) error {
	switch profile {
	case ReadinessProfileRuntime, ReadinessProfileGA:
		return nil
	default:
		return fmt.Errorf("AFSCP_READINESS_PROFILE must be runtime or ga")
	}
}

func loadWorkerConfig(source Source, defaults WorkerConfig) (WorkerConfig, error) {
	worker := defaults
	enabled, err := boolValue(source, "AFSCP_WORKER_OPERATION_RECOVERY_ENABLED")
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.Enabled = enabled
	worker.OperationRecovery.PostgresDSN = valueOrDefault(source, "AFSCP_POSTGRES_DSN", "")
	if worker.OperationRecovery.PostgresDSN == "" {
		worker.OperationRecovery.PostgresDSN = valueOrDefault(source, "AFSCP_DATABASE_URL", "")
	}
	worker.OperationRecovery.Owner = valueOrDefault(source, "AFSCP_WORKER_OWNER", "")
	repoCreate, err := loadRepoCreateRecoveryConfig(source)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.RepoCreate = repoCreate
	repoLifecycle, err := loadRepoLifecycleRecoveryConfig(source)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.RepoLifecycle = repoLifecycle
	repoPurge, err := loadRepoPurgeRecoveryConfig(source)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.RepoPurge = repoPurge
	savePoint, err := loadSavePointRecoveryConfig(source)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.SavePoint = savePoint
	templateCreate, err := loadTemplateCreateRecoveryConfig(source)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.TemplateCreate = templateCreate
	templateClone, err := loadTemplateCloneRecoveryConfig(source)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.TemplateClone = templateClone
	restore, err := loadRestoreRecoveryConfig(source)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.OperationRecovery.Restore = restore

	limit, err := intValue(source, "AFSCP_OPERATION_RECOVERY_LIMIT", worker.OperationRecovery.Limit)
	if err != nil {
		return WorkerConfig{}, err
	}
	if limit <= 0 {
		return WorkerConfig{}, fmt.Errorf("AFSCP_OPERATION_RECOVERY_LIMIT must be positive")
	}
	worker.OperationRecovery.Limit = limit

	leaseDuration, err := durationValue(source, "AFSCP_OPERATION_RECOVERY_LEASE_DURATION", worker.OperationRecovery.LeaseDuration)
	if err != nil {
		return WorkerConfig{}, err
	}
	if leaseDuration <= 0 {
		return WorkerConfig{}, fmt.Errorf("AFSCP_OPERATION_RECOVERY_LEASE_DURATION must be positive")
	}
	worker.OperationRecovery.LeaseDuration = leaseDuration

	runOnceTimeout, err := durationValue(source, "AFSCP_WORKER_RUN_ONCE_TIMEOUT", worker.RunOnceTimeout)
	if err != nil {
		return WorkerConfig{}, err
	}
	if runOnceTimeout <= 0 {
		return WorkerConfig{}, fmt.Errorf("AFSCP_WORKER_RUN_ONCE_TIMEOUT must be positive")
	}
	worker.RunOnceTimeout = runOnceTimeout

	if worker.OperationRecovery.Enabled {
		if worker.OperationRecovery.PostgresDSN == "" {
			return WorkerConfig{}, fmt.Errorf("AFSCP_POSTGRES_DSN is required when AFSCP_WORKER_OPERATION_RECOVERY_ENABLED is true")
		}
		if worker.OperationRecovery.Owner == "" {
			return WorkerConfig{}, fmt.Errorf("AFSCP_WORKER_OWNER is required when AFSCP_WORKER_OPERATION_RECOVERY_ENABLED is true")
		}
	}
	exportReconcile, err := loadWorkerExportSessionReconcileConfig(source, worker.ExportSessionReconcile)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.ExportSessionReconcile = exportReconcile
	restoreReconciliation, err := loadWorkerRestoreReconciliationConfig(source, worker.RestoreReconciliation)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.RestoreReconciliation = restoreReconciliation
	workloadMountStale, err := loadWorkerWorkloadMountStaleLeaseConfig(source, worker.WorkloadMountStale)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.WorkloadMountStale = workloadMountStale
	auditDelivery, err := loadWorkerAuditDeliveryConfig(source, worker.AuditDelivery)
	if err != nil {
		return WorkerConfig{}, err
	}
	worker.AuditDelivery = auditDelivery

	return worker, nil
}

func loadWorkerExportSessionReconcileConfig(source Source, defaults WorkerExportSessionReconcileConfig) (WorkerExportSessionReconcileConfig, error) {
	cfg := defaults
	enabled, err := boolValue(source, "AFSCP_EXPORT_SESSION_RECONCILE_ENABLED")
	if err != nil {
		return WorkerExportSessionReconcileConfig{}, err
	}
	cfg.Enabled = enabled
	cfg.PostgresDSN = valueOrDefault(source, "AFSCP_EXPORT_SESSION_RECONCILE_POSTGRES_DSN", "")
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_POSTGRES_DSN", "")
	}
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_DATABASE_URL", "")
	}
	cfg.Owner = strings.TrimSpace(valueOrDefault(source, "AFSCP_EXPORT_SESSION_RECONCILE_OWNER", cfg.Owner))
	if cfg.Limit, err = intValue(source, "AFSCP_EXPORT_SESSION_RECONCILE_LIMIT", cfg.Limit); err != nil {
		return WorkerExportSessionReconcileConfig{}, err
	}
	if cfg.Limit <= 0 {
		return WorkerExportSessionReconcileConfig{}, fmt.Errorf("AFSCP_EXPORT_SESSION_RECONCILE_LIMIT must be positive")
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	if cfg.PostgresDSN == "" {
		return WorkerExportSessionReconcileConfig{}, fmt.Errorf("AFSCP_POSTGRES_DSN is required when AFSCP_EXPORT_SESSION_RECONCILE_ENABLED is true")
	}
	if cfg.Owner == "" {
		return WorkerExportSessionReconcileConfig{}, fmt.Errorf("AFSCP_EXPORT_SESSION_RECONCILE_OWNER is required when AFSCP_EXPORT_SESSION_RECONCILE_ENABLED is true")
	}
	return cfg, nil
}

func loadWorkerRestoreReconciliationConfig(source Source, defaults WorkerRestoreReconciliationConfig) (WorkerRestoreReconciliationConfig, error) {
	cfg := defaults
	enabled, err := boolValue(source, "AFSCP_RESTORE_RECONCILIATION_ENABLED")
	if err != nil {
		return WorkerRestoreReconciliationConfig{}, err
	}
	cfg.Enabled = enabled
	cfg.PostgresDSN = valueOrDefault(source, "AFSCP_POSTGRES_DSN", cfg.PostgresDSN)
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_DATABASE_URL", "")
	}
	cfg.Owner = strings.TrimSpace(valueOrDefault(source, "AFSCP_RESTORE_RECONCILIATION_OWNER", cfg.Owner))
	limit, err := intValue(source, "AFSCP_RESTORE_RECONCILIATION_LIMIT", cfg.Limit)
	if err != nil {
		return WorkerRestoreReconciliationConfig{}, err
	}
	if limit <= 0 {
		return WorkerRestoreReconciliationConfig{}, fmt.Errorf("AFSCP_RESTORE_RECONCILIATION_LIMIT must be positive")
	}
	cfg.Limit = limit
	if cfg.Enabled {
		if cfg.PostgresDSN == "" {
			return WorkerRestoreReconciliationConfig{}, fmt.Errorf("AFSCP_POSTGRES_DSN is required when AFSCP_RESTORE_RECONCILIATION_ENABLED is true")
		}
		if cfg.Owner == "" {
			return WorkerRestoreReconciliationConfig{}, fmt.Errorf("AFSCP_RESTORE_RECONCILIATION_OWNER is required when AFSCP_RESTORE_RECONCILIATION_ENABLED is true")
		}
	}
	return cfg, nil
}

func loadWorkerWorkloadMountStaleLeaseConfig(source Source, defaults WorkerWorkloadMountStaleLeaseConfig) (WorkerWorkloadMountStaleLeaseConfig, error) {
	cfg := defaults
	enabled, err := boolValue(source, "AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_ENABLED")
	if err != nil {
		return WorkerWorkloadMountStaleLeaseConfig{}, err
	}
	cfg.Enabled = enabled
	cfg.PostgresDSN = valueOrDefault(source, "AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_POSTGRES_DSN", "")
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_POSTGRES_DSN", "")
	}
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_DATABASE_URL", "")
	}
	if cfg.Limit, err = intValue(source, "AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_LIMIT", cfg.Limit); err != nil {
		return WorkerWorkloadMountStaleLeaseConfig{}, err
	}
	if cfg.Limit <= 0 {
		return WorkerWorkloadMountStaleLeaseConfig{}, fmt.Errorf("AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_LIMIT must be positive")
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	if cfg.PostgresDSN == "" {
		return WorkerWorkloadMountStaleLeaseConfig{}, fmt.Errorf("AFSCP_POSTGRES_DSN is required when AFSCP_WORKLOAD_MOUNT_STALE_LEASE_RECONCILE_ENABLED is true")
	}
	return cfg, nil
}

func loadAPIConfig(source Source, defaults APIConfig, capabilities Capabilities) (APIConfig, error) {
	cfg := defaults
	cfg.Mode = strings.ToLower(valueOrDefault(source, "AFSCP_API_MODE", cfg.Mode))
	switch cfg.Mode {
	case "neutral", "internal":
	default:
		return APIConfig{}, fmt.Errorf("AFSCP_API_MODE must be neutral or internal")
	}

	cfg.PostgresDSN = valueOrDefault(source, "AFSCP_API_POSTGRES_DSN", "")
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_POSTGRES_DSN", "")
	}
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_DATABASE_URL", "")
	}
	cfg.ServiceTokens = valueOrDefault(source, "AFSCP_API_SERVICE_TOKENS", "")
	cfg.WebDAVExportPublicBaseURL = valueOrDefault(source, "AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL", cfg.WebDAVExportPublicBaseURL)
	if strings.TrimSpace(cfg.WebDAVExportPublicBaseURL) != "" {
		normalized, err := NormalizeWebDAVExportPublicBaseURL(cfg.WebDAVExportPublicBaseURL)
		if err != nil {
			return APIConfig{}, err
		}
		cfg.WebDAVExportPublicBaseURL = normalized
	}
	cfg.DeploymentGlobalAllowedCallers = valueOrDefault(source, "AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS", "")
	cfg.DeploymentNamespaceAllowedCallers = valueOrDefault(source, "AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS", "")
	if cfg.Mode != "internal" {
		cfg.VolumeRoots = map[string]string{}
		cfg.WorkloadMountRuntimeSecretRefs = map[string]SecretRef{}
	} else {
		rootsRaw := valueOrDefault(source, "AFSCP_API_VOLUME_ROOTS", "")
		rootsKey := "AFSCP_API_VOLUME_ROOTS"
		if rootsRaw == "" {
			rootsRaw = valueOrDefault(source, "AFSCP_VOLUME_ROOTS", "")
			rootsKey = "AFSCP_VOLUME_ROOTS"
		}
		if rootsRaw == "" {
			cfg.VolumeRoots = map[string]string{}
		} else {
			roots, err := parseOptionalVolumeRoots(rootsRaw, rootsKey)
			if err != nil {
				return APIConfig{}, err
			}
			cfg.VolumeRoots = roots
		}
		secretRefsRaw := valueOrDefault(source, "AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS", "")
		if secretRefsRaw == "" {
			cfg.WorkloadMountRuntimeSecretRefs = map[string]SecretRef{}
		} else {
			refs, err := parseWorkloadMountRuntimeSecretRefs(secretRefsRaw)
			if err != nil {
				return APIConfig{}, err
			}
			cfg.WorkloadMountRuntimeSecretRefs = refs
		}
	}
	savePointHistory, err := loadAPIJVSHistoryConfig(source, cfg, capabilities)
	if err != nil {
		return APIConfig{}, err
	}
	cfg.SavePointHistory = savePointHistory
	return cfg, nil
}

func NormalizeWebDAVExportPublicBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL is required when WebDAV export capability is available for export issuance")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL must be an http or https URL without userinfo, query, or fragment")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL must be an http or https URL without userinfo, query, or fragment")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL must be an http or https URL without userinfo, query, or fragment")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL must be an http or https URL without userinfo, query, or fragment")
	}
	parsed.Scheme = scheme
	return strings.TrimRight(parsed.String(), "/"), nil
}

func loadAPIJVSHistoryConfig(source Source, api APIConfig, capabilities Capabilities) (WorkerRepoCreateRecoveryConfig, error) {
	if api.Mode != "internal" || !capabilities.JVS.Available() {
		return WorkerRepoCreateRecoveryConfig{VolumeRoots: map[string]string{}}, nil
	}
	cfg := WorkerRepoCreateRecoveryConfig{Enabled: true, VolumeRoots: map[string]string{}}
	cfg.JVSBinaryPath = valueOrDefault(source, "AFSCP_JVS_BINARY_PATH", "")
	if err := validateCleanAbsoluteConfigPath("AFSCP_JVS_BINARY_PATH", cfg.JVSBinaryPath, "AFSCP_JVS_READY"); err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	cfg.JVSBinarySHA256 = strings.ToLower(valueOrDefault(source, "AFSCP_JVS_BINARY_SHA256", ""))
	directRestoreCapable, sourceRef, err := validatePinnedOrDirectRestoreJVSBinarySHA256(source, cfg.JVSBinarySHA256)
	if err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	if directRestoreCapable {
		cfg.JVSDirectRestoreRequired = true
		cfg.JVSDirectRestoreSourceRef = sourceRef
	}
	cfg.JVSCWD = valueOrDefault(source, "AFSCP_JVS_CWD", "")
	if err := validateCleanAbsoluteConfigPath("AFSCP_JVS_CWD", cfg.JVSCWD, "AFSCP_JVS_READY"); err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	if len(api.VolumeRoots) == 0 {
		return WorkerRepoCreateRecoveryConfig{}, fmt.Errorf("AFSCP_API_VOLUME_ROOTS or AFSCP_VOLUME_ROOTS is required when AFSCP_JVS_READY is true")
	}
	roots := make(map[string]string, len(api.VolumeRoots))
	for volumeID, root := range api.VolumeRoots {
		roots[volumeID] = root
	}
	if err := validateVolumeRoots(roots, "AFSCP_API_VOLUME_ROOTS or AFSCP_VOLUME_ROOTS"); err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	cfg.VolumeRoots = roots
	return cfg, nil
}

func loadExportGatewayConfig(source Source, defaults ExportGatewayConfig, listenFallback string) (ExportGatewayConfig, error) {
	cfg := defaults
	cfg.ListenAddr = valueOrDefault(source, "AFSCP_EXPORT_GATEWAY_LISTEN_ADDR", "")
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = listenFallback
	}
	cfg.PostgresDSN = valueOrDefault(source, "AFSCP_EXPORT_GATEWAY_POSTGRES_DSN", "")
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_POSTGRES_DSN", "")
	}
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_DATABASE_URL", "")
	}
	cfg.Prefix = valueOrDefault(source, "AFSCP_EXPORT_GATEWAY_PREFIX", cfg.Prefix)
	if cfg.Prefix == "" {
		cfg.Prefix = "/e/"
	}
	rootsRaw := valueOrDefault(source, "AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS", "")
	if rootsRaw != "" {
		roots, err := parseOptionalVolumeRoots(rootsRaw, "AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS")
		if err != nil {
			return ExportGatewayConfig{}, err
		}
		cfg.VolumeRoots = roots
		return cfg, nil
	}

	rootsRaw = valueOrDefault(source, "AFSCP_VOLUME_ROOTS", "")
	if rootsRaw == "" {
		cfg.VolumeRoots = map[string]string{}
		return cfg, nil
	}
	roots, err := parseOptionalVolumeRoots(rootsRaw, "AFSCP_VOLUME_ROOTS")
	if err != nil {
		if exportGatewayExplicitlyConfigured(source) {
			return ExportGatewayConfig{}, err
		}
		cfg.VolumeRoots = map[string]string{}
		return cfg, nil
	}
	cfg.VolumeRoots = roots
	return cfg, nil
}

func exportGatewayExplicitlyConfigured(source Source) bool {
	if source == nil {
		return false
	}
	for _, key := range []string{"AFSCP_EXPORT_GATEWAY_LISTEN_ADDR", "AFSCP_EXPORT_GATEWAY_POSTGRES_DSN", "AFSCP_EXPORT_GATEWAY_PREFIX"} {
		if valueOrDefault(source, key, "") != "" {
			return true
		}
	}
	return false
}

func ValidateExportGatewayConfig(cfg ExportGatewayConfig) error {
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return fmt.Errorf("AFSCP_EXPORT_GATEWAY_LISTEN_ADDR is required")
	}
	if strings.TrimSpace(cfg.PostgresDSN) == "" {
		return fmt.Errorf("AFSCP_EXPORT_GATEWAY_POSTGRES_DSN is required")
	}
	if !validGatewayPrefix(cfg.Prefix) {
		return fmt.Errorf("AFSCP_EXPORT_GATEWAY_PREFIX must start and end with / and contain no escapes")
	}
	if len(cfg.VolumeRoots) == 0 {
		return fmt.Errorf("AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS is required")
	}
	if err := validateVolumeRoots(cfg.VolumeRoots, "AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS"); err != nil {
		return err
	}
	return nil
}

func validGatewayPrefix(prefix string) bool {
	return strings.HasPrefix(prefix, "/") && strings.HasSuffix(prefix, "/") &&
		!strings.Contains(prefix, "%") && !strings.Contains(prefix, "\\") &&
		!strings.Contains(prefix, "//") && prefix != "/"
}

func ParseExportGatewayVolumeRoots(raw string) (map[string]string, error) {
	return parseVolumeRoots(raw, "AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS")
}

func parseWorkloadMountRuntimeSecretRefs(raw string) (map[string]SecretRef, error) {
	const key = "AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS"
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]SecretRef{}, nil
	}
	refs := map[string]SecretRef{}
	for _, part := range strings.Split(raw, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" || strings.TrimSpace(pair[1]) == "" {
			return nil, fmt.Errorf("%s must be vol_id=namespace/name pairs", key)
		}
		volumeID := strings.TrimSpace(pair[0])
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, fmt.Errorf("%s must contain valid volume ids and secret refs", key)
		}
		refParts := strings.Split(strings.TrimSpace(pair[1]), "/")
		if len(refParts) != 2 {
			return nil, fmt.Errorf("%s must contain valid volume ids and secret refs", key)
		}
		ref := SecretRef{Namespace: strings.TrimSpace(refParts[0]), Name: strings.TrimSpace(refParts[1])}
		if !validRuntimeSecretRefDNSLabel(ref.Namespace, 63) || !validRuntimeSecretRefDNSSubdomain(ref.Name, 253) {
			return nil, fmt.Errorf("%s must contain valid volume ids and secret refs", key)
		}
		if _, exists := refs[volumeID]; exists {
			return nil, fmt.Errorf("%s must contain unique volume ids", key)
		}
		refs[volumeID] = ref
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("%s is required when configured", key)
	}
	return refs, nil
}

func validRuntimeSecretRefDNSSubdomain(value string, maxLen int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLen {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if !validRuntimeSecretRefDNSLabel(part, 63) {
			return false
		}
	}
	return true
}

func validRuntimeSecretRefDNSLabel(value string, maxLen int) bool {
	if value == "" || len(value) > maxLen {
		return false
	}
	for idx, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && idx > 0 && idx < len(value)-1:
		default:
			return false
		}
	}
	return true
}

func loadWorkerAuditDeliveryConfig(source Source, defaults WorkerAuditDeliveryConfig) (WorkerAuditDeliveryConfig, error) {
	cfg := defaults
	enabled, err := boolValue(source, "AFSCP_WORKER_AUDIT_DELIVERY_ENABLED")
	if err != nil {
		return WorkerAuditDeliveryConfig{}, err
	}
	cfg.Enabled = enabled
	cfg.PostgresDSN = valueOrDefault(source, "AFSCP_AUDIT_DELIVERY_POSTGRES_DSN", "")
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_POSTGRES_DSN", "")
	}
	if cfg.PostgresDSN == "" {
		cfg.PostgresDSN = valueOrDefault(source, "AFSCP_DATABASE_URL", "")
	}
	cfg.Owner = valueOrDefault(source, "AFSCP_WORKER_AUDIT_DELIVERY_OWNER", "")
	cfg.SinkKind = strings.ToLower(valueOrDefault(source, "AFSCP_AUDIT_DELIVERY_SINK_KIND", ""))
	cfg.Endpoint = valueOrDefault(source, "AFSCP_AUDIT_DELIVERY_ENDPOINT", "")
	cfg.BearerToken = valueOrDefault(source, "AFSCP_AUDIT_DELIVERY_BEARER_TOKEN", "")

	if cfg.Limit, err = intValue(source, "AFSCP_AUDIT_DELIVERY_LIMIT", cfg.Limit); err != nil {
		return WorkerAuditDeliveryConfig{}, err
	}
	if cfg.Limit <= 0 {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_AUDIT_DELIVERY_LIMIT must be positive")
	}
	if cfg.MaxAttempts, err = intValue(source, "AFSCP_AUDIT_DELIVERY_MAX_ATTEMPTS", cfg.MaxAttempts); err != nil {
		return WorkerAuditDeliveryConfig{}, err
	}
	if cfg.MaxAttempts <= 0 {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_AUDIT_DELIVERY_MAX_ATTEMPTS must be positive")
	}
	if cfg.RetryBackoff, err = durationValue(source, "AFSCP_AUDIT_DELIVERY_RETRY_BACKOFF", cfg.RetryBackoff); err != nil {
		return WorkerAuditDeliveryConfig{}, err
	}
	if cfg.RetryBackoff < 0 {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_AUDIT_DELIVERY_RETRY_BACKOFF cannot be negative")
	}
	if cfg.StaleThreshold, err = durationValue(source, "AFSCP_AUDIT_DELIVERY_STALE_AFTER", cfg.StaleThreshold); err != nil {
		return WorkerAuditDeliveryConfig{}, err
	}
	if cfg.StaleThreshold <= 0 {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_AUDIT_DELIVERY_STALE_AFTER must be positive")
	}
	if cfg.Timeout, err = durationValue(source, "AFSCP_AUDIT_DELIVERY_TIMEOUT", cfg.Timeout); err != nil {
		return WorkerAuditDeliveryConfig{}, err
	}
	if cfg.Timeout <= 0 {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_AUDIT_DELIVERY_TIMEOUT must be positive")
	}

	if !cfg.Enabled {
		return cfg, nil
	}
	if cfg.PostgresDSN == "" {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_POSTGRES_DSN is required when AFSCP_WORKER_AUDIT_DELIVERY_ENABLED is true")
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_WORKER_AUDIT_DELIVERY_OWNER is required when AFSCP_WORKER_AUDIT_DELIVERY_ENABLED is true")
	}
	cfg.Owner = strings.TrimSpace(cfg.Owner)
	if cfg.SinkKind == "" {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_AUDIT_DELIVERY_SINK_KIND is required when AFSCP_WORKER_AUDIT_DELIVERY_ENABLED is true")
	}
	if cfg.SinkKind != "http_json" {
		return WorkerAuditDeliveryConfig{}, fmt.Errorf("AFSCP_AUDIT_DELIVERY_SINK_KIND must be http_json")
	}
	if err := validateAuditDeliveryEndpoint(cfg.Endpoint); err != nil {
		return WorkerAuditDeliveryConfig{}, err
	}
	return cfg, nil
}

func validateAuditDeliveryEndpoint(endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("AFSCP_AUDIT_DELIVERY_ENDPOINT is required when AFSCP_WORKER_AUDIT_DELIVERY_ENABLED is true")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("AFSCP_AUDIT_DELIVERY_ENDPOINT must be an http or https URL without userinfo")
	}
	if parsed.Scheme == "http" && !auditDeliveryEndpointHostIsLoopback(parsed.Hostname()) {
		return fmt.Errorf("AFSCP_AUDIT_DELIVERY_ENDPOINT must use https except for loopback http development endpoints")
	}
	return nil
}

func auditDeliveryEndpointHostIsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loadRepoCreateRecoveryConfig(source Source) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfig(source, "AFSCP_REPO_CREATE_RECOVERY_ENABLED")
}

func loadRepoLifecycleRecoveryConfig(source Source) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfig(source, "AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED")
}

func loadRepoPurgeRecoveryConfig(source Source) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfig(source, "AFSCP_REPO_PURGE_RECOVERY_ENABLED")
}

func loadSavePointRecoveryConfig(source Source) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfig(source, "AFSCP_SAVE_POINT_RECOVERY_ENABLED")
}

func loadTemplateCreateRecoveryConfig(source Source) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfig(source, "AFSCP_TEMPLATE_CREATE_RECOVERY_ENABLED")
}

func loadTemplateCloneRecoveryConfig(source Source) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfig(source, "AFSCP_TEMPLATE_CLONE_RECOVERY_ENABLED")
}

func loadRestoreRecoveryConfig(source Source) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSDirectRestoreOperationRecoveryConfig(source, "AFSCP_RESTORE_RECOVERY_ENABLED")
}

func loadJVSOperationRecoveryConfig(source Source, gateKey string) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfigWithPin(source, gateKey, false)
}

func loadJVSDirectRestoreOperationRecoveryConfig(source Source, gateKey string) (WorkerRepoCreateRecoveryConfig, error) {
	return loadJVSOperationRecoveryConfigWithPin(source, gateKey, true)
}

func loadJVSOperationRecoveryConfigWithPin(source Source, gateKey string, directRestoreRequired bool) (WorkerRepoCreateRecoveryConfig, error) {
	enabled, err := boolValue(source, gateKey)
	if err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	cfg := WorkerRepoCreateRecoveryConfig{Enabled: enabled, VolumeRoots: map[string]string{}}
	if !enabled {
		return cfg, nil
	}
	cfg.JVSBinaryPath = valueOrDefault(source, "AFSCP_JVS_BINARY_PATH", "")
	if err := validateCleanAbsoluteConfigPath("AFSCP_JVS_BINARY_PATH", cfg.JVSBinaryPath, gateKey); err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	cfg.JVSBinarySHA256 = strings.ToLower(valueOrDefault(source, "AFSCP_JVS_BINARY_SHA256", ""))
	if directRestoreRequired {
		sourceRef, err := validateDirectRestoreJVSBinarySHA256(source, cfg.JVSBinarySHA256)
		if err != nil {
			return WorkerRepoCreateRecoveryConfig{}, err
		}
		cfg.JVSDirectRestoreRequired = true
		cfg.JVSDirectRestoreSourceRef = sourceRef
	} else {
		directRestoreCapable, sourceRef, err := validatePinnedOrDirectRestoreJVSBinarySHA256(source, cfg.JVSBinarySHA256)
		if err != nil {
			return WorkerRepoCreateRecoveryConfig{}, err
		}
		if directRestoreCapable {
			cfg.JVSDirectRestoreRequired = true
			cfg.JVSDirectRestoreSourceRef = sourceRef
		}
	}
	cfg.JVSCWD = valueOrDefault(source, "AFSCP_JVS_CWD", "")
	if err := validateCleanAbsoluteConfigPath("AFSCP_JVS_CWD", cfg.JVSCWD, gateKey); err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	rootsRaw := valueOrDefault(source, "AFSCP_VOLUME_ROOTS", "")
	roots, err := parseVolumeRoots(rootsRaw, gateKey)
	if err != nil {
		return WorkerRepoCreateRecoveryConfig{}, err
	}
	cfg.VolumeRoots = roots
	return cfg, nil
}

func validateCleanAbsoluteConfigPath(key, path, gateKey string) error {
	if path == "" {
		return fmt.Errorf("%s is required when %s is true", key, gateKey)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return fmt.Errorf("%s must be an absolute clean path", key)
	}
	return nil
}

func validatePinnedJVSBinarySHA256(value string) error {
	if !validSHA256Hex(value) {
		return fmt.Errorf("AFSCP_JVS_BINARY_SHA256 must be a sha256 hex digest")
	}
	if value != JVSAcceptedLinuxAMD64SHA256 {
		return fmt.Errorf("AFSCP_JVS_BINARY_SHA256 must match pinned JVS %s %s SHA-256", JVSAcceptedReleaseVersion, JVSAcceptedLinuxAMD64AssetName)
	}
	return nil
}

func validatePinnedOrDirectRestoreJVSBinarySHA256(source Source, value string) (bool, string, error) {
	if !validSHA256Hex(value) {
		return false, "", fmt.Errorf("AFSCP_JVS_BINARY_SHA256 must be a sha256 hex digest")
	}
	if value == JVSAcceptedLinuxAMD64SHA256 {
		return true, JVSAcceptedSourceRef, nil
	}
	sourceRef, err := validateDirectRestoreJVSBinarySHA256(source, value)
	if err != nil {
		return false, "", fmt.Errorf("AFSCP_JVS_BINARY_SHA256 must match pinned JVS %s %s SHA-256 or a declared direct restore artifact: %w", JVSAcceptedReleaseVersion, JVSAcceptedLinuxAMD64AssetName, err)
	}
	return true, sourceRef, nil
}

func validateDirectRestoreJVSBinarySHA256(source Source, value string) (string, error) {
	if !validSHA256Hex(value) {
		return "", fmt.Errorf("AFSCP_JVS_BINARY_SHA256 must be a sha256 hex digest")
	}
	if value == JVSAcceptedLinuxAMD64SHA256 {
		return JVSAcceptedSourceRef, nil
	}
	directSHA := strings.ToLower(valueOrDefault(source, "AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256", ""))
	if !validSHA256Hex(directSHA) {
		return "", fmt.Errorf("AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256 must be a sha256 hex digest when AFSCP_RESTORE_RECOVERY_ENABLED is true")
	}
	if directSHA != value {
		return "", fmt.Errorf("AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256 must match AFSCP_JVS_BINARY_SHA256 when AFSCP_RESTORE_RECOVERY_ENABLED is true")
	}
	sourceRef := valueOrDefault(source, "AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF", "")
	if !validDirectRestoreSourceRef(sourceRef) {
		return "", fmt.Errorf("AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF is required when AFSCP_RESTORE_RECOVERY_ENABLED is true")
	}
	return sourceRef, nil
}

func validDirectRestoreSourceRef(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func parseVolumeRoots(raw, gateKey string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("AFSCP_VOLUME_ROOTS is required when %s is true", gateKey)
	}
	return parseVolumeRootsForKey(raw, "AFSCP_VOLUME_ROOTS")
}

func parseOptionalVolumeRoots(raw, key string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	return parseVolumeRootsForKey(raw, key)
}

func parseVolumeRootsForKey(raw, key string) (map[string]string, error) {
	roots := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" || strings.TrimSpace(pair[1]) == "" {
			return nil, fmt.Errorf("%s must be vol_id=/abs/root pairs", key)
		}
		volumeID := strings.TrimSpace(pair[0])
		root := strings.TrimSpace(pair[1])
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, fmt.Errorf("%s must contain valid volume ids and absolute clean roots", key)
		}
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
			return nil, fmt.Errorf("%s must contain valid volume ids and absolute clean roots", key)
		}
		if _, exists := roots[volumeID]; exists {
			return nil, fmt.Errorf("%s must contain unique volume ids and non-overlapping roots", key)
		}
		roots[volumeID] = root
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("%s is required", key)
	}
	if err := validateVolumeRoots(roots, key); err != nil {
		return nil, err
	}
	return roots, nil
}

func validateVolumeRoots(roots map[string]string, key string) error {
	seenRoots := map[string]string{}
	for volumeID, root := range roots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return fmt.Errorf("%s must contain valid volume ids and absolute clean roots", key)
		}
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
			return fmt.Errorf("%s must contain valid volume ids and absolute clean roots", key)
		}
		for _, existingRoot := range seenRoots {
			if root == existingRoot || configPathContains(root, existingRoot) || configPathContains(existingRoot, root) {
				return fmt.Errorf("%s must contain unique volume ids and non-overlapping roots", key)
			}
		}
		seenRoots[volumeID] = root
	}
	return nil
}

func configPathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func boolValue(source Source, key string) (bool, error) {
	if source == nil {
		return false, nil
	}
	value, ok := source.Lookup(key)
	if !ok {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "f", "false", "n", "no", "off", "disabled":
		return false, nil
	case "1", "t", "true", "y", "yes", "on", "enabled":
		return true, nil
	default:
		return false, fmt.Errorf("invalid bool for %s: %q", key, value)
	}
}

func intValue(source Source, key string, fallback int) (int, error) {
	if source == nil {
		return fallback, nil
	}
	value, ok := source.Lookup(key)
	if !ok {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid int for %s: %q", key, value)
	}
	return parsed, nil
}

func durationValue(source Source, key string, fallback time.Duration) (time.Duration, error) {
	if source == nil {
		return fallback, nil
	}
	value, ok := source.Lookup(key)
	if !ok {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for %s: %q", key, value)
	}
	return parsed, nil
}
