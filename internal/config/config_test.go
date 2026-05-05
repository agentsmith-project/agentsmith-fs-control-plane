package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsFailClosed(t *testing.T) {
	cfg, err := Load(MapSource{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got, want := cfg.ServiceName, "afscp"; got != want {
		t.Fatalf("ServiceName = %q, want %q", got, want)
	}
	if got, want := cfg.ListenAddr, ":8080"; got != want {
		t.Fatalf("ListenAddr = %q, want %q", got, want)
	}
	if got, want := cfg.Environment, "development"; got != want {
		t.Fatalf("Environment = %q, want %q", got, want)
	}

	assertCapability(t, "storage", cfg.Capabilities.Storage, false, false)
	assertCapability(t, "jvs", cfg.Capabilities.JVS, false, false)
	assertCapability(t, "webdav", cfg.Capabilities.WebDAV, false, false)
	assertCapability(t, "mount", cfg.Capabilities.Mount, false, false)
}

func TestLoadNormalizesFieldsAndCapabilities(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_SERVICE_NAME":    " afscp-api ",
		"AFSCP_LISTEN_ADDR":     " 127.0.0.1:8090 ",
		"AFSCP_ENVIRONMENT":     " Prod ",
		"AFSCP_STORAGE_ENABLED": " true ",
		"AFSCP_STORAGE_READY":   " 1 ",
		"AFSCP_JVS_ENABLED":     " TRUE ",
		"AFSCP_JVS_READY":       " false ",
		"AFSCP_WEBDAV_ENABLED":  " yes ",
		"AFSCP_WEBDAV_READY":    " yes ",
		"AFSCP_MOUNT_READY":     " true ",
		"AFSCP_MOUNT_ENABLED":   " false ",
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got, want := cfg.ServiceName, "afscp-api"; got != want {
		t.Fatalf("ServiceName = %q, want %q", got, want)
	}
	if got, want := cfg.ListenAddr, "127.0.0.1:8090"; got != want {
		t.Fatalf("ListenAddr = %q, want %q", got, want)
	}
	if got, want := cfg.Environment, "prod"; got != want {
		t.Fatalf("Environment = %q, want %q", got, want)
	}

	assertCapability(t, "storage", cfg.Capabilities.Storage, true, true)
	assertCapability(t, "jvs", cfg.Capabilities.JVS, true, false)
	assertCapability(t, "webdav", cfg.Capabilities.WebDAV, true, true)
	assertCapability(t, "mount", cfg.Capabilities.Mount, false, false)
}

func TestLoadRejectsInvalidCapabilityBool(t *testing.T) {
	_, err := Load(MapSource{"AFSCP_STORAGE_ENABLED": "maybe"})
	if err == nil {
		t.Fatal("Load returned nil error, want invalid bool error")
	}
	if !strings.Contains(err.Error(), "AFSCP_STORAGE_ENABLED") {
		t.Fatalf("error = %q, want key name", err)
	}
}

func assertCapability(t *testing.T, name string, got Capability, wantEnabled bool, wantReady bool) {
	t.Helper()

	if got.Enabled != wantEnabled {
		t.Fatalf("%s Enabled = %v, want %v", name, got.Enabled, wantEnabled)
	}
	if got.Ready != wantReady {
		t.Fatalf("%s Ready = %v, want %v", name, got.Ready, wantReady)
	}
	if got.Available() != (wantEnabled && wantReady) {
		t.Fatalf("%s Available() = %v, want %v", name, got.Available(), wantEnabled && wantReady)
	}
}
