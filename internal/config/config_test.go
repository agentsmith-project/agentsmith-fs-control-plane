package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsFailClosed(t *testing.T) {
	cfg, err := Load(MapSource{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got, want := cfg.ServiceName, "afscp"; got != want {
		t.Fatalf("ServiceName = %q, want %q", got, want)
	}
	if got, want := cfg.ListenAddr, "127.0.0.1:8080"; got != want {
		t.Fatalf("ListenAddr = %q, want %q", got, want)
	}
	if got, want := cfg.Environment, "development"; got != want {
		t.Fatalf("Environment = %q, want %q", got, want)
	}

	assertCapability(t, "storage", cfg.Capabilities.Storage, false, false)
	assertCapability(t, "jvs", cfg.Capabilities.JVS, false, false)
	assertCapability(t, "webdav", cfg.Capabilities.WebDAV, false, false)
	assertCapability(t, "mount", cfg.Capabilities.Mount, false, false)

	if cfg.Worker.OperationRecovery.Enabled {
		t.Fatal("worker operation recovery enabled by default, want disabled")
	}
	if cfg.Worker.OperationRecovery.Limit != 10 {
		t.Fatalf("operation recovery limit = %d, want 10", cfg.Worker.OperationRecovery.Limit)
	}
	if cfg.Worker.OperationRecovery.LeaseDuration != 5*time.Minute {
		t.Fatalf("operation recovery lease duration = %v, want 5m", cfg.Worker.OperationRecovery.LeaseDuration)
	}
	if cfg.Worker.RunOnceTimeout != 30*time.Second {
		t.Fatalf("worker run-once timeout = %v, want 30s", cfg.Worker.RunOnceTimeout)
	}
	if cfg.Worker.OperationRecovery.RepoCreate.Enabled {
		t.Fatal("repo_create recovery enabled by default, want disabled")
	}
	if cfg.Worker.OperationRecovery.RepoLifecycle.Enabled {
		t.Fatal("repo lifecycle recovery enabled by default, want disabled")
	}
	if cfg.Worker.OperationRecovery.RestorePreview.Enabled {
		t.Fatal("restore preview recovery enabled by default, want disabled")
	}
	if cfg.Worker.OperationRecovery.RestorePreviewDiscard.Enabled {
		t.Fatal("restore preview discard recovery enabled by default, want disabled")
	}
	if cfg.Worker.OperationRecovery.RestoreRun.Enabled {
		t.Fatal("restore run recovery enabled by default, want disabled")
	}
	if cfg.Worker.AuditDelivery.Enabled {
		t.Fatal("audit delivery enabled by default, want disabled")
	}
	if cfg.Worker.AuditDelivery.Limit != 10 {
		t.Fatalf("audit delivery limit = %d, want 10", cfg.Worker.AuditDelivery.Limit)
	}
	if cfg.Worker.AuditDelivery.MaxAttempts != 5 {
		t.Fatalf("audit delivery max attempts = %d, want 5", cfg.Worker.AuditDelivery.MaxAttempts)
	}
	if cfg.Worker.AuditDelivery.RetryBackoff != time.Minute {
		t.Fatalf("audit delivery retry backoff = %v, want 1m", cfg.Worker.AuditDelivery.RetryBackoff)
	}
	if cfg.Worker.AuditDelivery.StaleThreshold != 5*time.Minute {
		t.Fatalf("audit delivery stale threshold = %v, want 5m", cfg.Worker.AuditDelivery.StaleThreshold)
	}
	if cfg.Worker.AuditDelivery.Timeout != 10*time.Second {
		t.Fatalf("audit delivery timeout = %v, want 10s", cfg.Worker.AuditDelivery.Timeout)
	}
	if cfg.API.Mode != "neutral" {
		t.Fatalf("api mode = %q, want neutral", cfg.API.Mode)
	}
	if cfg.API.PostgresDSN != "" {
		t.Fatalf("api postgres dsn = %q, want empty", cfg.API.PostgresDSN)
	}
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

func TestLoadAPIInternalRuntimeConfig(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_API_MODE":                                 " internal ",
		"AFSCP_API_POSTGRES_DSN":                         "postgres://api:secret@db/afscp",
		"AFSCP_API_SERVICE_TOKENS":                       "svc_api=token-a",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS":    "svc_ops:operator:operation_inspector|operator_admin",
		"AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS": "svc_api:product:namespace_admin",
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.API.Mode != "internal" {
		t.Fatalf("api mode = %q, want internal", cfg.API.Mode)
	}
	if cfg.API.PostgresDSN != "postgres://api:secret@db/afscp" {
		t.Fatalf("api postgres dsn = %q", cfg.API.PostgresDSN)
	}
	if cfg.API.ServiceTokens != "svc_api=token-a" {
		t.Fatalf("api service tokens = %q", cfg.API.ServiceTokens)
	}
	if cfg.API.DeploymentGlobalAllowedCallers != "svc_ops:operator:operation_inspector|operator_admin" {
		t.Fatalf("api global callers = %q", cfg.API.DeploymentGlobalAllowedCallers)
	}
	if cfg.API.DeploymentNamespaceAllowedCallers != "svc_api:product:namespace_admin" {
		t.Fatalf("api namespace callers = %q", cfg.API.DeploymentNamespaceAllowedCallers)
	}
}

func TestLoadAPIInternalRuntimeDSNFallsBackToSharedPostgres(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_API_MODE":     "internal",
		"AFSCP_POSTGRES_DSN": "postgres://shared:secret@db/afscp",
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.API.PostgresDSN != "postgres://shared:secret@db/afscp" {
		t.Fatalf("api postgres dsn = %q, want shared postgres fallback", cfg.API.PostgresDSN)
	}
}

func TestLoadRejectsUnknownAPIMode(t *testing.T) {
	_, err := Load(MapSource{"AFSCP_API_MODE": "storage"})
	if err == nil {
		t.Fatal("Load succeeded, want invalid API mode error")
	}
	if !strings.Contains(err.Error(), "AFSCP_API_MODE") {
		t.Fatalf("error = %q, want API mode context", err)
	}
}

func TestLoadRepoCreateRecoveryRequiresExplicitConfigWhenEnabled(t *testing.T) {
	base := MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_REPO_CREATE_RECOVERY_ENABLED":      "true",
	}
	tests := []struct {
		name     string
		override MapSource
		want     string
	}{
		{name: "missing binary", want: "AFSCP_JVS_BINARY_PATH"},
		{name: "missing checksum", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs"}, want: "AFSCP_JVS_BINARY_SHA256"},
		{name: "missing cwd", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64)}, want: "AFSCP_JVS_CWD"},
		{name: "missing roots", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "relative binary", override: MapSource{"AFSCP_JVS_BINARY_PATH": "jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol"}, want: "AFSCP_JVS_BINARY_PATH"},
		{name: "bad checksum", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": "not-sha", "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol"}, want: "AFSCP_JVS_BINARY_SHA256"},
		{name: "relative cwd", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol"}, want: "AFSCP_JVS_CWD"},
		{name: "bad root mapping", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=relative"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "bad volume id slash", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_bad/id=/srv/vol"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "bad volume id dot", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_bad.id=/srv/vol"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "duplicate volume id", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol-a,vol_123=/srv/vol-b"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "duplicate root", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol,vol_456=/srv/vol"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "overlapping roots", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol,vol_456=/srv/vol/child"}, want: "AFSCP_VOLUME_ROOTS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := MapSource{}
			for key, value := range base {
				source[key] = value
			}
			for key, value := range tt.override {
				source[key] = value
			}
			_, err := Load(source)
			if err == nil {
				t.Fatal("Load succeeded, want repo_create config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %s", err, tt.want)
			}
			for _, leaked := range []string{"/srv/vol", "/srv/vol-a", "/srv/vol-b", "/srv/vol/child"} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked raw root %q: %v", leaked, err)
				}
			}
		})
	}
}

func TestLoadRepoCreateRecoveryParsesValidConfig(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_REPO_CREATE_RECOVERY_ENABLED":      "true",
		"AFSCP_JVS_BINARY_PATH":                   "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                 strings.Repeat("a", 64),
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123, vol_other=/srv/afscp/volumes/vol_other",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	repo := cfg.Worker.OperationRecovery.RepoCreate
	if !repo.Enabled || repo.JVSBinaryPath != "/opt/afscp/bin/jvs" || repo.JVSCWD != "/var/lib/afscp/jvs-cwd" || repo.JVSBinarySHA256 != strings.Repeat("a", 64) {
		t.Fatalf("repo_create config = %#v", repo)
	}
	if repo.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" || repo.VolumeRoots["vol_other"] != "/srv/afscp/volumes/vol_other" {
		t.Fatalf("volume roots = %#v", repo.VolumeRoots)
	}
}

func TestLoadRepoLifecycleRecoveryRequiresExplicitConfigWhenEnabled(t *testing.T) {
	_, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED":   "true",
	})
	if err == nil {
		t.Fatal("Load succeeded, want repo lifecycle config error")
	}
	if !strings.Contains(err.Error(), "AFSCP_JVS_BINARY_PATH") {
		t.Fatalf("error = %q, want binary path config error", err)
	}
}

func TestLoadRepoLifecycleRecoveryParsesValidConfig(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED":   "true",
		"AFSCP_JVS_BINARY_PATH":                   "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                 strings.Repeat("b", 64),
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lifecycle := cfg.Worker.OperationRecovery.RepoLifecycle
	if !lifecycle.Enabled || lifecycle.JVSBinaryPath != "/opt/afscp/bin/jvs" || lifecycle.JVSBinarySHA256 != strings.Repeat("b", 64) || lifecycle.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("repo lifecycle config = %#v", lifecycle)
	}
}

func TestLoadRepoPurgeRecoveryParsesIndependentExplicitGate(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_REPO_LIFECYCLE_RECOVERY_ENABLED":   "true",
		"AFSCP_REPO_PURGE_RECOVERY_ENABLED":       "true",
		"AFSCP_JVS_BINARY_PATH":                   "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                 strings.Repeat("c", 64),
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Worker.OperationRecovery.RepoLifecycle.Enabled || !cfg.Worker.OperationRecovery.RepoPurge.Enabled {
		t.Fatalf("repo lifecycle/purge gates = %#v/%#v", cfg.Worker.OperationRecovery.RepoLifecycle, cfg.Worker.OperationRecovery.RepoPurge)
	}
}

func TestLoadRestorePreviewRecoveryParsesIndependentExplicitGate(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_RESTORE_PREVIEW_RECOVERY_ENABLED":  "true",
		"AFSCP_JVS_BINARY_PATH":                   "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                 strings.Repeat("d", 64),
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	restorePreview := cfg.Worker.OperationRecovery.RestorePreview
	if !restorePreview.Enabled || restorePreview.JVSBinaryPath != "/opt/afscp/bin/jvs" || restorePreview.JVSBinarySHA256 != strings.Repeat("d", 64) || restorePreview.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("restore preview config = %#v", restorePreview)
	}
	if cfg.Worker.OperationRecovery.SavePoint.Enabled || cfg.Worker.OperationRecovery.RepoLifecycle.Enabled {
		t.Fatalf("restore preview gate should not enable other JVS workers: %#v", cfg.Worker.OperationRecovery)
	}
}

func TestLoadRestorePreviewDiscardRecoveryParsesIndependentExplicitGate(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED":        "true",
		"AFSCP_POSTGRES_DSN":                             "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                             "worker-a",
		"AFSCP_RESTORE_PREVIEW_DISCARD_RECOVERY_ENABLED": "true",
		"AFSCP_JVS_BINARY_PATH":                          "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                        strings.Repeat("e", 64),
		"AFSCP_JVS_CWD":                                  "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                             "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	discard := cfg.Worker.OperationRecovery.RestorePreviewDiscard
	if !discard.Enabled || discard.JVSBinaryPath != "/opt/afscp/bin/jvs" || discard.JVSBinarySHA256 != strings.Repeat("e", 64) || discard.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("restore preview discard config = %#v", discard)
	}
	if cfg.Worker.OperationRecovery.RestorePreview.Enabled || cfg.Worker.OperationRecovery.SavePoint.Enabled || cfg.Worker.OperationRecovery.RepoLifecycle.Enabled {
		t.Fatalf("restore preview discard gate should not enable other JVS workers: %#v", cfg.Worker.OperationRecovery)
	}
}

func TestLoadRestoreRunRecoveryParsesIndependentExplicitGate(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_RESTORE_RUN_RECOVERY_ENABLED":      "true",
		"AFSCP_JVS_BINARY_PATH":                   "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                 strings.Repeat("f", 64),
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	restoreRun := cfg.Worker.OperationRecovery.RestoreRun
	if !restoreRun.Enabled || restoreRun.JVSBinaryPath != "/opt/afscp/bin/jvs" || restoreRun.JVSBinarySHA256 != strings.Repeat("f", 64) || restoreRun.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("restore run config = %#v", restoreRun)
	}
	if cfg.Worker.OperationRecovery.RestorePreview.Enabled || cfg.Worker.OperationRecovery.RestorePreviewDiscard.Enabled || cfg.Worker.OperationRecovery.SavePoint.Enabled || cfg.Worker.OperationRecovery.RepoLifecycle.Enabled {
		t.Fatalf("restore run gate should not enable other JVS workers: %#v", cfg.Worker.OperationRecovery)
	}
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

func TestLoadWorkerOperationRecoveryRequiresDSNAndOwnerWhenEnabled(t *testing.T) {
	tests := []struct {
		name   string
		source MapSource
		want   string
	}{
		{
			name:   "missing dsn",
			source: MapSource{"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true", "AFSCP_WORKER_OWNER": "worker-a"},
			want:   "AFSCP_POSTGRES_DSN",
		},
		{
			name:   "missing owner",
			source: MapSource{"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true", "AFSCP_POSTGRES_DSN": "postgres://user:password@db/afscp"},
			want:   "AFSCP_WORKER_OWNER",
		},
		{
			name:   "blank owner",
			source: MapSource{"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true", "AFSCP_POSTGRES_DSN": "postgres://user:password@db/afscp", "AFSCP_WORKER_OWNER": " \t"},
			want:   "AFSCP_WORKER_OWNER",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.source)
			if err == nil {
				t.Fatal("Load succeeded, want config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %s", err, tt.want)
			}
		})
	}
}

func TestLoadWorkerOperationRecoveryRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		value  string
		source MapSource
	}{
		{name: "invalid enabled", key: "AFSCP_WORKER_OPERATION_RECOVERY_ENABLED", value: "sometimes"},
		{name: "invalid limit", key: "AFSCP_OPERATION_RECOVERY_LIMIT", value: "ten"},
		{name: "zero limit", key: "AFSCP_OPERATION_RECOVERY_LIMIT", value: "0"},
		{name: "invalid lease duration", key: "AFSCP_OPERATION_RECOVERY_LEASE_DURATION", value: "soon"},
		{name: "zero lease duration", key: "AFSCP_OPERATION_RECOVERY_LEASE_DURATION", value: "0s"},
		{name: "invalid timeout", key: "AFSCP_WORKER_RUN_ONCE_TIMEOUT", value: "later"},
		{name: "zero timeout", key: "AFSCP_WORKER_RUN_ONCE_TIMEOUT", value: "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := MapSource{
				"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
				"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
				"AFSCP_WORKER_OWNER":                      "worker-a",
			}
			for key, value := range tt.source {
				source[key] = value
			}
			source[tt.key] = tt.value

			_, err := Load(source)
			if err == nil {
				t.Fatal("Load succeeded, want config error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("error = %q, want key %s", err, tt.key)
			}
		})
	}
}

func TestLoadWorkerOperationRecoveryParsesDefaultsOverridesAndFallbackDSN(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_DATABASE_URL":                      "postgres://fallback:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      " worker-a ",
	})
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if !cfg.Worker.OperationRecovery.Enabled {
		t.Fatal("operation recovery disabled, want enabled")
	}
	if cfg.Worker.OperationRecovery.PostgresDSN != "postgres://fallback:password@db/afscp" {
		t.Fatalf("postgres dsn = %q, want fallback database url", cfg.Worker.OperationRecovery.PostgresDSN)
	}
	if cfg.Worker.OperationRecovery.Owner != "worker-a" {
		t.Fatalf("owner = %q, want trimmed worker-a", cfg.Worker.OperationRecovery.Owner)
	}
	if cfg.Worker.OperationRecovery.Limit != 10 || cfg.Worker.OperationRecovery.LeaseDuration != 5*time.Minute || cfg.Worker.RunOnceTimeout != 30*time.Second {
		t.Fatalf("defaults = limit %d lease %v timeout %v", cfg.Worker.OperationRecovery.Limit, cfg.Worker.OperationRecovery.LeaseDuration, cfg.Worker.RunOnceTimeout)
	}

	cfg, err = Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_DATABASE_URL":                      "postgres://fallback:password@db/afscp",
		"AFSCP_POSTGRES_DSN":                      "postgres://primary:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-b",
		"AFSCP_OPERATION_RECOVERY_LIMIT":          "25",
		"AFSCP_OPERATION_RECOVERY_LEASE_DURATION": "2m",
		"AFSCP_WORKER_RUN_ONCE_TIMEOUT":           "45s",
	})
	if err != nil {
		t.Fatalf("Load overrides: %v", err)
	}
	if cfg.Worker.OperationRecovery.PostgresDSN != "postgres://primary:password@db/afscp" {
		t.Fatalf("postgres dsn = %q, want primary dsn", cfg.Worker.OperationRecovery.PostgresDSN)
	}
	if cfg.Worker.OperationRecovery.Owner != "worker-b" || cfg.Worker.OperationRecovery.Limit != 25 || cfg.Worker.OperationRecovery.LeaseDuration != 2*time.Minute || cfg.Worker.RunOnceTimeout != 45*time.Second {
		t.Fatalf("overrides = %#v", cfg.Worker)
	}
}

func TestLoadWorkerAuditDeliveryRequiresExplicitConfigWhenEnabled(t *testing.T) {
	base := MapSource{"AFSCP_WORKER_AUDIT_DELIVERY_ENABLED": "true"}
	tests := []struct {
		name     string
		override MapSource
		want     string
	}{
		{name: "missing dsn", want: "AFSCP_POSTGRES_DSN"},
		{name: "missing owner", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp"}, want: "AFSCP_WORKER_AUDIT_DELIVERY_OWNER"},
		{name: "missing sink", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker"}, want: "AFSCP_AUDIT_DELIVERY_SINK_KIND"},
		{name: "missing endpoint", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker", "AFSCP_AUDIT_DELIVERY_SINK_KIND": "http_json"}, want: "AFSCP_AUDIT_DELIVERY_ENDPOINT"},
		{name: "unsupported sink", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker", "AFSCP_AUDIT_DELIVERY_SINK_KIND": "stdout", "AFSCP_AUDIT_DELIVERY_ENDPOINT": "https://audit.example/sink"}, want: "AFSCP_AUDIT_DELIVERY_SINK_KIND"},
		{name: "bad endpoint", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker", "AFSCP_AUDIT_DELIVERY_SINK_KIND": "http_json", "AFSCP_AUDIT_DELIVERY_ENDPOINT": "ftp://secret.example/sink"}, want: "AFSCP_AUDIT_DELIVERY_ENDPOINT"},
		{name: "http non-loopback endpoint", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker", "AFSCP_AUDIT_DELIVERY_SINK_KIND": "http_json", "AFSCP_AUDIT_DELIVERY_ENDPOINT": "http://audit.example/sink"}, want: "AFSCP_AUDIT_DELIVERY_ENDPOINT"},
		{name: "endpoint userinfo", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker", "AFSCP_AUDIT_DELIVERY_SINK_KIND": "http_json", "AFSCP_AUDIT_DELIVERY_ENDPOINT": "https://user:secret@audit.example/sink"}, want: "AFSCP_AUDIT_DELIVERY_ENDPOINT"},
		{name: "invalid timeout", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker", "AFSCP_AUDIT_DELIVERY_SINK_KIND": "http_json", "AFSCP_AUDIT_DELIVERY_ENDPOINT": "https://audit.example/sink", "AFSCP_AUDIT_DELIVERY_TIMEOUT": "soon"}, want: "AFSCP_AUDIT_DELIVERY_TIMEOUT"},
		{name: "zero timeout", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://audit:password@db/afscp", "AFSCP_WORKER_AUDIT_DELIVERY_OWNER": "audit-worker", "AFSCP_AUDIT_DELIVERY_SINK_KIND": "http_json", "AFSCP_AUDIT_DELIVERY_ENDPOINT": "https://audit.example/sink", "AFSCP_AUDIT_DELIVERY_TIMEOUT": "0s"}, want: "AFSCP_AUDIT_DELIVERY_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := MapSource{}
			for key, value := range base {
				source[key] = value
			}
			for key, value := range tt.override {
				source[key] = value
			}
			_, err := Load(source)
			if err == nil {
				t.Fatal("Load succeeded, want audit delivery config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %s", err, tt.want)
			}
			for _, leaked := range []string{"secret.example", "password@db", "user:secret"} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked sensitive config %q: %v", leaked, err)
				}
			}
		})
	}
}

func TestLoadWorkerAuditDeliveryParsesDefaultsOverridesAndFallbackDSN(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_AUDIT_DELIVERY_ENABLED": "true",
		"AFSCP_DATABASE_URL":                  "postgres://fallback:password@db/afscp",
		"AFSCP_WORKER_AUDIT_DELIVERY_OWNER":   " audit-worker ",
		"AFSCP_AUDIT_DELIVERY_SINK_KIND":      " http_json ",
		"AFSCP_AUDIT_DELIVERY_ENDPOINT":       " https://audit.example/sink ",
	})
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	auditDelivery := cfg.Worker.AuditDelivery
	if !auditDelivery.Enabled {
		t.Fatal("audit delivery disabled, want enabled")
	}
	if auditDelivery.PostgresDSN != "postgres://fallback:password@db/afscp" || auditDelivery.Owner != "audit-worker" || auditDelivery.SinkKind != "http_json" || auditDelivery.Endpoint != "https://audit.example/sink" {
		t.Fatalf("audit delivery config = %#v", auditDelivery)
	}
	if auditDelivery.Limit != 10 || auditDelivery.MaxAttempts != 5 || auditDelivery.RetryBackoff != time.Minute || auditDelivery.StaleThreshold != 5*time.Minute || auditDelivery.Timeout != 10*time.Second {
		t.Fatalf("audit delivery defaults = %#v", auditDelivery)
	}

	cfg, err = Load(MapSource{
		"AFSCP_WORKER_AUDIT_DELIVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                  "postgres://primary:password@db/afscp",
		"AFSCP_WORKER_AUDIT_DELIVERY_OWNER":   "audit-worker-b",
		"AFSCP_AUDIT_DELIVERY_LIMIT":          "25",
		"AFSCP_AUDIT_DELIVERY_MAX_ATTEMPTS":   "8",
		"AFSCP_AUDIT_DELIVERY_RETRY_BACKOFF":  "3m",
		"AFSCP_AUDIT_DELIVERY_STALE_AFTER":    "9m",
		"AFSCP_AUDIT_DELIVERY_SINK_KIND":      "http_json",
		"AFSCP_AUDIT_DELIVERY_ENDPOINT":       "http://127.0.0.1:8080/sink",
		"AFSCP_AUDIT_DELIVERY_BEARER_TOKEN":   "audit-secret-token",
		"AFSCP_AUDIT_DELIVERY_TIMEOUT":        "2s",
	})
	if err != nil {
		t.Fatalf("Load overrides: %v", err)
	}
	auditDelivery = cfg.Worker.AuditDelivery
	if auditDelivery.PostgresDSN != "postgres://primary:password@db/afscp" || auditDelivery.Owner != "audit-worker-b" || auditDelivery.Limit != 25 || auditDelivery.MaxAttempts != 8 || auditDelivery.RetryBackoff != 3*time.Minute || auditDelivery.StaleThreshold != 9*time.Minute || auditDelivery.Endpoint != "http://127.0.0.1:8080/sink" || auditDelivery.BearerToken != "audit-secret-token" || auditDelivery.Timeout != 2*time.Second {
		t.Fatalf("audit delivery overrides = %#v", auditDelivery)
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
