package config

import (
	"strings"
	"testing"
	"time"
)

const (
	acceptedJVSBinarySHA256            = "f6028582acdf9257f83636bcb70dc63a809887689bb3bc52c47336360f6b3d1c"
	directRestoreLocalJVSBinaryPath    = "/tmp/afscp-jvs-direct-local"
	directRestoreLocalJVSBinarySHA256  = "f6028582acdf9257f83636bcb70dc63a809887689bb3bc52c47336360f6b3d1c"
	directRestoreLocalJVSSourceRef     = "jvs@main:edd317474db5fd6f9e3e98015438a47d02ad73c6"
	customDirectRestoreJVSBinarySHA256 = "c88553bb18bdd70e1399bf562fcb853bd200798498bd24bc25458196fb568902"
	customDirectRestoreJVSSourceRef    = "jvs@agentsmith-direct-restore-operation:c65b418f58d6e39e91199c1d55783e2ec91be9a1"
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
	if got, want := cfg.ReadinessProfile, "runtime"; got != want {
		t.Fatalf("ReadinessProfile = %q, want %q", got, want)
	}

	assertCapability(t, "storage", cfg.Capabilities.Storage, false, false)
	assertCapability(t, "jvs", cfg.Capabilities.JVS, false, false)
	assertCapability(t, "webdav", cfg.Capabilities.WebDAV, false, false)
	assertCapability(t, "mount", cfg.Capabilities.Mount, false, false)
	assertCapability(t, "repo_template", cfg.Capabilities.RepoTemplate, false, false)
	assertCapability(t, "repo_purge", cfg.Capabilities.RepoPurge, false, false)

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
	if cfg.Worker.OperationRecovery.Restore.Enabled {
		t.Fatal("restore recovery enabled by default, want disabled")
	}
	if cfg.Worker.AuditDelivery.Enabled {
		t.Fatal("audit delivery enabled by default, want disabled")
	}
	if cfg.Worker.ExportSessionReconcile.Enabled {
		t.Fatal("export session reconcile enabled by default, want disabled")
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
	if cfg.API.WebDAVExportPublicBaseURL != "" {
		t.Fatalf("api webdav export public base url = %q, want empty", cfg.API.WebDAVExportPublicBaseURL)
	}
	if cfg.ExportGateway.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("export gateway listen addr = %q, want default", cfg.ExportGateway.ListenAddr)
	}
	if cfg.ExportGateway.PostgresDSN != "" {
		t.Fatalf("export gateway postgres dsn = %q, want empty", cfg.ExportGateway.PostgresDSN)
	}
	if cfg.ExportGateway.Prefix != "/e/" {
		t.Fatalf("export gateway prefix = %q, want /e/", cfg.ExportGateway.Prefix)
	}
	if len(cfg.ExportGateway.VolumeRoots) != 0 {
		t.Fatalf("export gateway volume roots = %#v, want empty", cfg.ExportGateway.VolumeRoots)
	}
}

func TestLoadNormalizesFieldsAndCapabilities(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_SERVICE_NAME":          " afscp-api ",
		"AFSCP_LISTEN_ADDR":           " 127.0.0.1:8090 ",
		"AFSCP_ENVIRONMENT":           " Prod ",
		"AFSCP_STORAGE_ENABLED":       " true ",
		"AFSCP_STORAGE_READY":         " 1 ",
		"AFSCP_JVS_ENABLED":           " TRUE ",
		"AFSCP_JVS_READY":             " false ",
		"AFSCP_WEBDAV_ENABLED":        " yes ",
		"AFSCP_WEBDAV_READY":          " yes ",
		"AFSCP_MOUNT_READY":           " true ",
		"AFSCP_MOUNT_ENABLED":         " false ",
		"AFSCP_REPO_TEMPLATE_ENABLED": " true ",
		"AFSCP_REPO_TEMPLATE_READY":   " true ",
		"AFSCP_REPO_PURGE_ENABLED":    " true ",
		"AFSCP_REPO_PURGE_READY":      " false ",
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
	assertCapability(t, "repo_template", cfg.Capabilities.RepoTemplate, true, true)
	assertCapability(t, "repo_purge", cfg.Capabilities.RepoPurge, true, false)
}

func TestLoadReadinessProfileAllowsRuntimeAndGA(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "runtime", raw: " runtime ", want: "runtime"},
		{name: "ga", raw: " GA ", want: "ga"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(MapSource{"AFSCP_READINESS_PROFILE": tt.raw})
			if err != nil {
				t.Fatalf("Load returned error: %v", err)
			}
			if cfg.ReadinessProfile != tt.want {
				t.Fatalf("ReadinessProfile = %q, want %q", cfg.ReadinessProfile, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidReadinessProfile(t *testing.T) {
	_, err := Load(MapSource{"AFSCP_READINESS_PROFILE": "production"})
	if err == nil {
		t.Fatal("Load succeeded, want readiness profile error")
	}
	if !strings.Contains(err.Error(), "AFSCP_READINESS_PROFILE") {
		t.Fatalf("error = %q, want readiness profile context", err)
	}
}

func TestLoadAPIInternalRuntimeConfig(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_API_MODE":                                 " internal ",
		"AFSCP_API_POSTGRES_DSN":                         "postgres://api:secret@db/afscp",
		"AFSCP_API_SERVICE_TOKENS":                       "svc_api=token-a",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS":    "svc_ops:operator:operation_inspector|operator_admin",
		"AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS": "svc_api:product:namespace_admin",
		"AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL":        " https://files.example.com/public ",
		"AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS":           " vol_payload01=runtime-secret-namespace/runtime-secret-volume ",
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
	if cfg.API.WebDAVExportPublicBaseURL != "https://files.example.com/public" {
		t.Fatalf("api webdav export public base url = %q", cfg.API.WebDAVExportPublicBaseURL)
	}
	if got := cfg.API.WorkloadMountRuntimeSecretRefs["vol_payload01"]; got.Namespace != "runtime-secret-namespace" || got.Name != "runtime-secret-volume" {
		t.Fatalf("workload mount secret ref = %#v", got)
	}
}

func TestLoadAPIWebDAVExportPublicBaseURLRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "non http scheme", raw: "ftp://files.example.com"},
		{name: "missing host", raw: "https:///exports"},
		{name: "relative reference", raw: "/exports"},
		{name: "userinfo", raw: "https://user:secret@files.example.com"},
		{name: "query", raw: "https://files.example.com?token=secret"},
		{name: "fragment", raw: "https://files.example.com/#secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(MapSource{
				"AFSCP_API_MODE": "internal",
				"AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL": tt.raw,
			})
			if err == nil {
				t.Fatal("Load succeeded, want public base URL config error")
			}
			if !strings.Contains(err.Error(), "AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL") {
				t.Fatalf("error = %q, want public base URL context", err)
			}
			for _, leaked := range []string{"user:secret", "token=secret", "#secret"} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked raw URL component %q: %v", leaked, err)
				}
			}
		})
	}
}

func TestNormalizeWebDAVExportPublicBaseURLMissingDescribesCapabilityScope(t *testing.T) {
	_, err := NormalizeWebDAVExportPublicBaseURL("")
	if err == nil {
		t.Fatal("NormalizeWebDAVExportPublicBaseURL succeeded, want missing public base URL error")
	}
	if !strings.Contains(err.Error(), "when WebDAV export capability is available") {
		t.Fatalf("error = %q, want WebDAV capability scope", err)
	}
	if strings.Contains(err.Error(), "AFSCP_API_MODE is internal") {
		t.Fatalf("error = %q, should not imply all internal API runtimes require WebDAV export public base URL", err)
	}
}

func TestLoadNeutralAPIDoesNotRequireWebDAVExportPublicBaseURL(t *testing.T) {
	cfg, err := Load(MapSource{"AFSCP_API_MODE": "neutral"})
	if err != nil {
		t.Fatalf("Load returned error in neutral mode without public base URL: %v", err)
	}
	if cfg.API.WebDAVExportPublicBaseURL != "" {
		t.Fatalf("api webdav export public base url = %q, want empty in neutral mode", cfg.API.WebDAVExportPublicBaseURL)
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

func TestLoadAPIVolumeRootsFromExplicitConfigAndFallback(t *testing.T) {
	tests := []struct {
		name   string
		source MapSource
		want   map[string]string
	}{
		{
			name: "explicit api roots win",
			source: MapSource{
				"AFSCP_API_MODE":         "internal",
				"AFSCP_API_VOLUME_ROOTS": "vol_api=/srv/afscp/api-volumes/vol_api",
				"AFSCP_VOLUME_ROOTS":     "vol_shared=/srv/afscp/shared-volumes/vol_shared",
			},
			want: map[string]string{"vol_api": "/srv/afscp/api-volumes/vol_api"},
		},
		{
			name: "fallback shared roots",
			source: MapSource{
				"AFSCP_API_MODE":     "internal",
				"AFSCP_VOLUME_ROOTS": "vol_shared=/srv/afscp/shared-volumes/vol_shared",
			},
			want: map[string]string{"vol_shared": "/srv/afscp/shared-volumes/vol_shared"},
		},
		{
			name:   "empty stays unconfigured",
			source: MapSource{"AFSCP_API_MODE": "internal"},
			want:   map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(tt.source)
			if err != nil {
				t.Fatalf("Load returned error: %v", err)
			}
			if len(cfg.API.VolumeRoots) != len(tt.want) {
				t.Fatalf("api volume roots = %#v, want %#v", cfg.API.VolumeRoots, tt.want)
			}
			for volumeID, root := range tt.want {
				if got := cfg.API.VolumeRoots[volumeID]; got != root {
					t.Fatalf("api volume root %s = %q, want %q in %#v", volumeID, got, root, cfg.API.VolumeRoots)
				}
			}
		})
	}
}

func TestLoadNeutralAPIIgnoresMalformedVolumeRoots(t *testing.T) {
	tests := []struct {
		name   string
		source MapSource
	}{
		{
			name:   "default neutral mode",
			source: MapSource{"AFSCP_VOLUME_ROOTS": "vol_api=/srv/afscp/secret-root,vol_other=/srv/afscp/secret-root/child"},
		},
		{
			name:   "explicit neutral mode",
			source: MapSource{"AFSCP_API_MODE": "neutral", "AFSCP_VOLUME_ROOTS": "vol_api=/srv/afscp/secret-root,vol_other=/srv/afscp/secret-root/child"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(tt.source)
			if err != nil {
				t.Fatalf("Load returned error in neutral mode, want malformed shared roots ignored: %v", err)
			}
			if len(cfg.API.VolumeRoots) != 0 {
				t.Fatalf("api volume roots = %#v, want empty in neutral mode", cfg.API.VolumeRoots)
			}
			if len(cfg.ExportGateway.VolumeRoots) != 0 {
				t.Fatalf("export gateway volume roots = %#v, want empty when shared roots are malformed and gateway is not explicit", cfg.ExportGateway.VolumeRoots)
			}
		})
	}
}

func TestLoadNeutralAPIDoesNotDisableExportGatewaySharedRootFallback(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_API_MODE":     "neutral",
		"AFSCP_VOLUME_ROOTS": "vol_shared=/srv/afscp/shared-volumes/vol_shared",
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.API.VolumeRoots) != 0 {
		t.Fatalf("api volume roots = %#v, want empty in neutral mode", cfg.API.VolumeRoots)
	}
	if got := cfg.ExportGateway.VolumeRoots["vol_shared"]; got != "/srv/afscp/shared-volumes/vol_shared" {
		t.Fatalf("export gateway shared fallback root = %q in %#v", got, cfg.ExportGateway.VolumeRoots)
	}
}

func TestLoadAPIVolumeRootsRejectsBadRootsWithoutLeakingValues(t *testing.T) {
	tests := []struct {
		name   string
		source MapSource
		want   string
	}{
		{
			name:   "explicit relative root",
			source: MapSource{"AFSCP_API_MODE": "internal", "AFSCP_API_VOLUME_ROOTS": "vol_api=secret-relative-root"},
			want:   "AFSCP_API_VOLUME_ROOTS",
		},
		{
			name:   "explicit overlapping roots",
			source: MapSource{"AFSCP_API_MODE": "internal", "AFSCP_API_VOLUME_ROOTS": "vol_api=/srv/afscp/secret-root,vol_other=/srv/afscp/secret-root/child"},
			want:   "AFSCP_API_VOLUME_ROOTS",
		},
		{
			name:   "fallback bad root",
			source: MapSource{"AFSCP_API_MODE": "internal", "AFSCP_VOLUME_ROOTS": "vol_api=/srv/afscp/secret-root,vol_other=/srv/afscp/secret-root/child"},
			want:   "AFSCP_VOLUME_ROOTS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.source)
			if err == nil {
				t.Fatal("Load succeeded, want API volume root config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %s", err, tt.want)
			}
			for _, leaked := range []string{"secret-relative-root", "/srv/afscp/secret-root", "/srv/afscp/secret-root/child"} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked raw root %q: %v", leaked, err)
				}
			}
		})
	}
}

func TestLoadAPIJVSReadyWiresSavePointHistoryWithoutExtraGate(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_API_MODE":                              "internal",
		"AFSCP_API_POSTGRES_DSN":                      "postgres://api:secret@db/afscp",
		"AFSCP_API_SERVICE_TOKENS":                    "svc_api=token-api",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_api:product:operation_inspector",
		"AFSCP_JVS_ENABLED":                           "true",
		"AFSCP_JVS_READY":                             "true",
		"AFSCP_JVS_BINARY_PATH":                       "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                     acceptedJVSBinarySHA256,
		"AFSCP_JVS_CWD":                               "/var/lib/afscp/jvs-cwd",
		"AFSCP_API_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
		"AFSCP_API_SAVE_POINT_HISTORY_ENABLED":        "false",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	history := cfg.API.SavePointHistory
	if !history.Enabled {
		t.Fatalf("API save point history disabled when JVS is ready: %#v", history)
	}
	if history.JVSBinaryPath != "/opt/afscp/bin/jvs" || history.JVSBinarySHA256 != acceptedJVSBinarySHA256 || history.JVSCWD != "/var/lib/afscp/jvs-cwd" {
		t.Fatalf("API save point history JVS config = %#v", history)
	}
	if history.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("API save point history roots = %#v", history.VolumeRoots)
	}
}

func TestLoadAPIJVSReadyAllowsDeclaredDirectRestoreArtifactForSavePointHistory(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_API_MODE":                              "internal",
		"AFSCP_API_POSTGRES_DSN":                      "postgres://api:secret@db/afscp",
		"AFSCP_API_SERVICE_TOKENS":                    "svc_api=token-api",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_api:product:operation_inspector",
		"AFSCP_JVS_ENABLED":                           "true",
		"AFSCP_JVS_READY":                             "true",
		"AFSCP_JVS_BINARY_PATH":                       directRestoreLocalJVSBinaryPath,
		"AFSCP_JVS_BINARY_SHA256":                     customDirectRestoreJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256":      customDirectRestoreJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF":         customDirectRestoreJVSSourceRef,
		"AFSCP_JVS_CWD":                               "/var/lib/afscp/jvs-cwd",
		"AFSCP_API_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	history := cfg.API.SavePointHistory
	if !history.Enabled || history.JVSBinarySHA256 != customDirectRestoreJVSBinarySHA256 || !history.JVSDirectRestoreRequired || history.JVSDirectRestoreSourceRef != customDirectRestoreJVSSourceRef {
		t.Fatalf("API save point history direct JVS config = %#v", history)
	}
}

func TestLoadAPIJVSReadyRequiresSavePointHistoryRuntimeConfig(t *testing.T) {
	base := MapSource{
		"AFSCP_API_MODE":                              "internal",
		"AFSCP_API_POSTGRES_DSN":                      "postgres://api:secret@db/afscp",
		"AFSCP_API_SERVICE_TOKENS":                    "svc_api=token-api",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_api:product:operation_inspector",
		"AFSCP_JVS_ENABLED":                           "true",
		"AFSCP_JVS_READY":                             "true",
	}
	tests := []struct {
		name     string
		override MapSource
		want     string
	}{
		{name: "missing binary", want: "AFSCP_JVS_BINARY_PATH"},
		{name: "missing cwd", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256}, want: "AFSCP_JVS_CWD"},
		{name: "missing roots", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd"}, want: "AFSCP_API_VOLUME_ROOTS or AFSCP_VOLUME_ROOTS"},
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
				t.Fatal("Load succeeded, want API JVS history config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %s", err, tt.want)
			}
		})
	}
}

func TestLoadAPIWorkloadMountRuntimeSecretRefs(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_API_MODE":                       "internal",
		"AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS": "vol_payload01=runtime-secret-namespace/runtime-secret-volume,vol_other=runtime-secret-namespace/runtime-secret-other",
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.API.WorkloadMountRuntimeSecretRefs["vol_payload01"]; got.Namespace != "runtime-secret-namespace" || got.Name != "runtime-secret-volume" {
		t.Fatalf("vol_payload01 secret ref = %#v", got)
	}
	if got := cfg.API.WorkloadMountRuntimeSecretRefs["vol_other"]; got.Namespace != "runtime-secret-namespace" || got.Name != "runtime-secret-other" {
		t.Fatalf("vol_other secret ref = %#v", got)
	}
}

func TestLoadAPIWorkloadMountRuntimeSecretRefsRejectsInvalidConfigWithoutLeakingValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing separator", raw: "vol_payload01"},
		{name: "bad volume id", raw: "payload01=secret-ns/secret-name"},
		{name: "missing namespace", raw: "vol_payload01=/secret-name"},
		{name: "missing name", raw: "vol_payload01=secret-ns/"},
		{name: "extra slash", raw: "vol_payload01=secret-ns/secret-name/extra"},
		{name: "uppercase secret", raw: "vol_payload01=Secret-Ns/secret-name"},
		{name: "duplicate volume", raw: "vol_payload01=secret-ns/secret-name,vol_payload01=secret-ns/secret-other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(MapSource{
				"AFSCP_API_MODE":                       "internal",
				"AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS": tt.raw,
			})
			if err == nil {
				t.Fatal("Load succeeded, want workload mount secret ref config error")
			}
			if !strings.Contains(err.Error(), "AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS") {
				t.Fatalf("error = %q, want secret ref config key", err)
			}
			for _, leaked := range []string{"Secret-Ns", "secret-name/extra", "secret-other"} {
				if strings.Contains(err.Error(), leaked) {
					t.Fatalf("error leaked raw secret ref component %q: %v", leaked, err)
				}
			}
		})
	}
}

func TestLoadExportGatewayExplicitConfigRejectsMalformedSharedRoots(t *testing.T) {
	_, err := Load(MapSource{
		"AFSCP_EXPORT_GATEWAY_POSTGRES_DSN": "postgres://gateway:secret@db/afscp",
		"AFSCP_VOLUME_ROOTS":                "vol_api=/srv/afscp/secret-root,vol_other=/srv/afscp/secret-root/child",
	})
	if err == nil {
		t.Fatal("Load succeeded, want export gateway shared root fallback error")
	}
	if !strings.Contains(err.Error(), "AFSCP_VOLUME_ROOTS") {
		t.Fatalf("error = %q, want shared roots key", err)
	}
	for _, leaked := range []string{"/srv/afscp/secret-root", "/srv/afscp/secret-root/child", "secret"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error leaked raw root %q: %v", leaked, err)
		}
	}
}

func TestLoadExportGatewayConfig(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_EXPORT_GATEWAY_LISTEN_ADDR":  "127.0.0.1:9090",
		"AFSCP_EXPORT_GATEWAY_POSTGRES_DSN": "postgres://gateway:secret@db/afscp",
		"AFSCP_EXPORT_GATEWAY_PREFIX":       "/exports/",
		"AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS": "vol_123=/srv/afscp/volumes/vol_123, vol_other=/srv/afscp/volumes/vol_other",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ExportGateway.ListenAddr != "127.0.0.1:9090" {
		t.Fatalf("listen addr = %q", cfg.ExportGateway.ListenAddr)
	}
	if cfg.ExportGateway.PostgresDSN != "postgres://gateway:secret@db/afscp" {
		t.Fatalf("postgres dsn = %q", cfg.ExportGateway.PostgresDSN)
	}
	if cfg.ExportGateway.Prefix != "/exports/" {
		t.Fatalf("prefix = %q", cfg.ExportGateway.Prefix)
	}
	if cfg.ExportGateway.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" || cfg.ExportGateway.VolumeRoots["vol_other"] != "/srv/afscp/volumes/vol_other" {
		t.Fatalf("volume roots = %#v", cfg.ExportGateway.VolumeRoots)
	}
}

func TestLoadExportGatewayConfigFallsBackToSharedDSNAndRoots(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_POSTGRES_DSN": "postgres://shared:secret@db/afscp",
		"AFSCP_VOLUME_ROOTS": "vol_123=/srv/afscp/volumes/vol_123",
		"AFSCP_LISTEN_ADDR":  "127.0.0.1:8181",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ExportGateway.PostgresDSN != "postgres://shared:secret@db/afscp" {
		t.Fatalf("postgres dsn = %q, want shared fallback", cfg.ExportGateway.PostgresDSN)
	}
	if cfg.ExportGateway.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("volume roots = %#v", cfg.ExportGateway.VolumeRoots)
	}
	if cfg.ExportGateway.ListenAddr != "127.0.0.1:8181" {
		t.Fatalf("listen addr = %q, want shared listen fallback", cfg.ExportGateway.ListenAddr)
	}
}

func TestValidateExportGatewayConfigFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		cfg  ExportGatewayConfig
		want string
	}{
		{name: "missing dsn", cfg: ExportGatewayConfig{ListenAddr: "127.0.0.1:9090", Prefix: "/e/", VolumeRoots: map[string]string{"vol_123": "/srv/vol"}}, want: "AFSCP_EXPORT_GATEWAY_POSTGRES_DSN"},
		{name: "missing roots", cfg: ExportGatewayConfig{ListenAddr: "127.0.0.1:9090", Prefix: "/e/", PostgresDSN: "postgres://gateway:secret@db/afscp"}, want: "AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS"},
		{name: "bad prefix", cfg: ExportGatewayConfig{ListenAddr: "127.0.0.1:9090", Prefix: "e", PostgresDSN: "postgres://gateway:secret@db/afscp", VolumeRoots: map[string]string{"vol_123": "/srv/vol"}}, want: "AFSCP_EXPORT_GATEWAY_PREFIX"},
		{name: "bad root", cfg: ExportGatewayConfig{ListenAddr: "127.0.0.1:9090", Prefix: "/e/", PostgresDSN: "postgres://gateway:secret@db/afscp", VolumeRoots: map[string]string{"vol_123": "relative"}}, want: "AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExportGatewayConfig(tt.cfg)
			if err == nil {
				t.Fatal("ValidateExportGatewayConfig succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %s", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/srv/vol") {
				t.Fatalf("error leaked sensitive config: %v", err)
			}
		})
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
		{name: "missing cwd", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256}, want: "AFSCP_JVS_CWD"},
		{name: "missing roots", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "relative binary", override: MapSource{"AFSCP_JVS_BINARY_PATH": "jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol"}, want: "AFSCP_JVS_BINARY_PATH"},
		{name: "bad checksum", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": "not-sha", "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol"}, want: "AFSCP_JVS_BINARY_SHA256"},
		{name: "unpinned checksum", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": strings.Repeat("a", 64), "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol"}, want: "pinned JVS"},
		{name: "relative cwd", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol"}, want: "AFSCP_JVS_CWD"},
		{name: "bad root mapping", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=relative"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "bad volume id slash", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_bad/id=/srv/vol"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "bad volume id dot", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_bad.id=/srv/vol"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "duplicate volume id", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol-a,vol_123=/srv/vol-b"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "duplicate root", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol,vol_456=/srv/vol"}, want: "AFSCP_VOLUME_ROOTS"},
		{name: "overlapping roots", override: MapSource{"AFSCP_JVS_BINARY_PATH": "/opt/afscp/bin/jvs", "AFSCP_JVS_BINARY_SHA256": acceptedJVSBinarySHA256, "AFSCP_JVS_CWD": "/var/lib/afscp/jvs-cwd", "AFSCP_VOLUME_ROOTS": "vol_123=/srv/vol,vol_456=/srv/vol/child"}, want: "AFSCP_VOLUME_ROOTS"},
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
		"AFSCP_JVS_BINARY_SHA256":                 acceptedJVSBinarySHA256,
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123, vol_other=/srv/afscp/volumes/vol_other",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	repo := cfg.Worker.OperationRecovery.RepoCreate
	if !repo.Enabled || repo.JVSBinaryPath != "/opt/afscp/bin/jvs" || repo.JVSCWD != "/var/lib/afscp/jvs-cwd" || repo.JVSBinarySHA256 != acceptedJVSBinarySHA256 {
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
		"AFSCP_JVS_BINARY_SHA256":                 acceptedJVSBinarySHA256,
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lifecycle := cfg.Worker.OperationRecovery.RepoLifecycle
	if !lifecycle.Enabled || lifecycle.JVSBinaryPath != "/opt/afscp/bin/jvs" || lifecycle.JVSBinarySHA256 != acceptedJVSBinarySHA256 || lifecycle.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
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
		"AFSCP_JVS_BINARY_SHA256":                 acceptedJVSBinarySHA256,
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

func TestLoadRestoreRecoveryParsesIndependentExplicitGate(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_RESTORE_RECOVERY_ENABLED":          "true",
		"AFSCP_JVS_BINARY_PATH":                   directRestoreLocalJVSBinaryPath,
		"AFSCP_JVS_BINARY_SHA256":                 customDirectRestoreJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256":  customDirectRestoreJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF":     customDirectRestoreJVSSourceRef,
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	restore := cfg.Worker.OperationRecovery.Restore
	if !restore.Enabled || restore.JVSBinaryPath != directRestoreLocalJVSBinaryPath || restore.JVSBinarySHA256 != customDirectRestoreJVSBinarySHA256 || !restore.JVSDirectRestoreRequired || restore.JVSDirectRestoreSourceRef != customDirectRestoreJVSSourceRef || restore.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("restore config = %#v", restore)
	}
	if cfg.Worker.OperationRecovery.SavePoint.Enabled || cfg.Worker.OperationRecovery.RepoLifecycle.Enabled {
		t.Fatalf("restore gate should not enable other JVS workers: %#v", cfg.Worker.OperationRecovery)
	}
}

func TestLoadSharedJVSRecoveryGatesAllowDeclaredDirectRestoreArtifact(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_SAVE_POINT_RECOVERY_ENABLED":       "true",
		"AFSCP_TEMPLATE_CREATE_RECOVERY_ENABLED":  "true",
		"AFSCP_RESTORE_RECOVERY_ENABLED":          "true",
		"AFSCP_JVS_BINARY_PATH":                   directRestoreLocalJVSBinaryPath,
		"AFSCP_JVS_BINARY_SHA256":                 customDirectRestoreJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256":  customDirectRestoreJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF":     customDirectRestoreJVSSourceRef,
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for name, worker := range map[string]WorkerRepoCreateRecoveryConfig{
		"save point":      cfg.Worker.OperationRecovery.SavePoint,
		"template create": cfg.Worker.OperationRecovery.TemplateCreate,
		"restore":         cfg.Worker.OperationRecovery.Restore,
	} {
		if !worker.Enabled || worker.JVSBinarySHA256 != customDirectRestoreJVSBinarySHA256 || !worker.JVSDirectRestoreRequired || worker.JVSDirectRestoreSourceRef != customDirectRestoreJVSSourceRef {
			t.Fatalf("%s worker direct JVS config = %#v", name, worker)
		}
	}
}

func TestLoadRestoreRecoveryAcceptsCurrentDirectLocalJVSPin(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_RESTORE_RECOVERY_ENABLED":          "true",
		"AFSCP_JVS_BINARY_PATH":                   "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                 acceptedJVSBinarySHA256,
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	})
	if err != nil {
		t.Fatalf("Load returned error for current direct local JVS pin: %v", err)
	}
	restore := cfg.Worker.OperationRecovery.Restore
	if !restore.Enabled || !restore.JVSDirectRestoreRequired || restore.JVSDirectRestoreSourceRef != directRestoreLocalJVSSourceRef {
		t.Fatalf("restore config = %#v, want current direct local pin accepted", restore)
	}
}

func TestLoadRestoreRecoveryRequiresDirectRestoreArtifactDeclaration(t *testing.T) {
	base := MapSource{
		"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
		"AFSCP_POSTGRES_DSN":                      "postgres://user:password@db/afscp",
		"AFSCP_WORKER_OWNER":                      "worker-a",
		"AFSCP_RESTORE_RECOVERY_ENABLED":          "true",
		"AFSCP_JVS_BINARY_PATH":                   "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                 customDirectRestoreJVSBinarySHA256,
		"AFSCP_JVS_CWD":                           "/var/lib/afscp/jvs-cwd",
		"AFSCP_VOLUME_ROOTS":                      "vol_123=/srv/afscp/volumes/vol_123",
	}
	tests := []struct {
		name     string
		override MapSource
		want     string
	}{
		{name: "missing direct sha", want: "AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256"},
		{name: "mismatched direct sha", override: MapSource{"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256": strings.Repeat("2", 64), "AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF": customDirectRestoreJVSSourceRef}, want: "must match AFSCP_JVS_BINARY_SHA256"},
		{name: "missing source ref", override: MapSource{"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256": customDirectRestoreJVSBinarySHA256}, want: "AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF"},
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
				t.Fatal("Load succeeded, want direct restore artifact declaration error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
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

func TestLoadWorkerExportSessionReconcileRequiresDSNAndOwnerWhenEnabled(t *testing.T) {
	base := MapSource{"AFSCP_EXPORT_SESSION_RECONCILE_ENABLED": "true"}
	tests := []struct {
		name     string
		override MapSource
		want     string
	}{
		{name: "missing dsn", want: "AFSCP_POSTGRES_DSN"},
		{name: "missing owner", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://worker:password@db/afscp"}, want: "AFSCP_EXPORT_SESSION_RECONCILE_OWNER"},
		{name: "invalid limit", override: MapSource{"AFSCP_POSTGRES_DSN": "postgres://worker:password@db/afscp", "AFSCP_EXPORT_SESSION_RECONCILE_OWNER": "export-worker", "AFSCP_EXPORT_SESSION_RECONCILE_LIMIT": "0"}, want: "AFSCP_EXPORT_SESSION_RECONCILE_LIMIT"},
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
				t.Fatal("Load succeeded, want config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want key %s", err, tt.want)
			}
		})
	}
}

func TestLoadWorkerExportSessionReconcileParsesDefaultsOverridesAndFallbackDSN(t *testing.T) {
	cfg, err := Load(MapSource{
		"AFSCP_EXPORT_SESSION_RECONCILE_ENABLED": "true",
		"AFSCP_DATABASE_URL":                     "postgres://fallback:password@db/afscp",
		"AFSCP_EXPORT_SESSION_RECONCILE_OWNER":   " export-worker ",
	})
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	reconcile := cfg.Worker.ExportSessionReconcile
	if !reconcile.Enabled {
		t.Fatal("export session reconcile disabled, want enabled")
	}
	if reconcile.PostgresDSN != "postgres://fallback:password@db/afscp" || reconcile.Owner != "export-worker" || reconcile.Limit != 10 {
		t.Fatalf("export reconcile config = %#v", reconcile)
	}

	cfg, err = Load(MapSource{
		"AFSCP_EXPORT_SESSION_RECONCILE_ENABLED": "true",
		"AFSCP_DATABASE_URL":                     "postgres://fallback:password@db/afscp",
		"AFSCP_POSTGRES_DSN":                     "postgres://primary:password@db/afscp",
		"AFSCP_EXPORT_SESSION_RECONCILE_OWNER":   "export-worker-b",
		"AFSCP_EXPORT_SESSION_RECONCILE_LIMIT":   "25",
		"AFSCP_WORKER_RUN_ONCE_TIMEOUT":          "45s",
	})
	if err != nil {
		t.Fatalf("Load overrides: %v", err)
	}
	reconcile = cfg.Worker.ExportSessionReconcile
	if reconcile.PostgresDSN != "postgres://primary:password@db/afscp" || reconcile.Owner != "export-worker-b" || reconcile.Limit != 25 || cfg.Worker.RunOnceTimeout != 45*time.Second {
		t.Fatalf("export reconcile overrides = %#v", cfg.Worker)
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
