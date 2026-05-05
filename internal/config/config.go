package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultServiceName                    = "afscp"
	defaultListenAddr                     = "127.0.0.1:8080"
	defaultEnvironment                    = "development"
	defaultOperationRecoveryLimit         = 10
	defaultOperationRecoveryLeaseDuration = 5 * time.Minute
	defaultWorkerRunOnceTimeout           = 30 * time.Second
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
	ServiceName  string
	ListenAddr   string
	Environment  string
	Capabilities Capabilities
	Worker       WorkerConfig
}

type Capabilities struct {
	Storage Capability
	JVS     Capability
	WebDAV  Capability
	Mount   Capability
}

type Capability struct {
	Enabled bool
	Ready   bool
}

type WorkerConfig struct {
	RunOnceTimeout    time.Duration
	OperationRecovery WorkerOperationRecoveryConfig
}

type WorkerOperationRecoveryConfig struct {
	Enabled       bool
	PostgresDSN   string
	Owner         string
	Limit         int
	LeaseDuration time.Duration
}

func (c Capability) Available() bool {
	return c.Enabled && c.Ready
}

func LoadFromEnv() (Config, error) {
	return Load(EnvSource{})
}

func Load(source Source) (Config, error) {
	cfg := Config{
		ServiceName: defaultServiceName,
		ListenAddr:  defaultListenAddr,
		Environment: defaultEnvironment,
		Worker: WorkerConfig{
			RunOnceTimeout: defaultWorkerRunOnceTimeout,
			OperationRecovery: WorkerOperationRecoveryConfig{
				Limit:         defaultOperationRecoveryLimit,
				LeaseDuration: defaultOperationRecoveryLeaseDuration,
			},
		},
	}

	cfg.ServiceName = strings.ToLower(valueOrDefault(source, "AFSCP_SERVICE_NAME", cfg.ServiceName))
	cfg.ListenAddr = valueOrDefault(source, "AFSCP_LISTEN_ADDR", cfg.ListenAddr)
	cfg.Environment = strings.ToLower(valueOrDefault(source, "AFSCP_ENVIRONMENT", cfg.Environment))

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
	if cfg.Worker, err = loadWorkerConfig(source, cfg.Worker); err != nil {
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

	return worker, nil
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
