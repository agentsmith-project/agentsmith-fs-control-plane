package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultServiceName = "afscp"
	defaultListenAddr  = ":8080"
	defaultEnvironment = "development"
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
