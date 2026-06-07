package apiapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/capability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

func TestNewRuntimeFailsClosedWithoutDSN(t *testing.T) {
	_, err := NewRuntime(Options{
		Source: config.MapSource{
			"AFSCP_API_MODE":                              "internal",
			"AFSCP_API_SERVICE_TOKENS":                    "svc_api=token-api",
			"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_api:product:operation_inspector",
			"AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL":     "https://files.example.test",
		},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			t.Fatal("store factory should not be called without DSN")
			return StoreHandle{}, nil
		},
	})
	if err == nil {
		t.Fatal("NewRuntime succeeded, want missing DSN error")
	}
	if !strings.Contains(err.Error(), "AFSCP_API_POSTGRES_DSN") {
		t.Fatalf("error = %q, want DSN context", err)
	}
}

func TestNewRuntimeFailsClosedWithoutValidTokenMapping(t *testing.T) {
	tests := []struct {
		name   string
		tokens string
		want   string
	}{
		{name: "missing", tokens: "", want: "AFSCP_API_SERVICE_TOKENS"},
		{name: "malformed", tokens: "svc_api", want: "AFSCP_API_SERVICE_TOKENS"},
		{name: "missing caller", tokens: "=super-secret-token", want: "AFSCP_API_SERVICE_TOKENS"},
		{name: "missing token", tokens: "svc_api=", want: "AFSCP_API_SERVICE_TOKENS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRuntime(Options{
				Source: config.MapSource{
					"AFSCP_API_MODE":                              "internal",
					"AFSCP_API_POSTGRES_DSN":                      "postgres://api:secret@db/afscp",
					"AFSCP_API_SERVICE_TOKENS":                    tt.tokens,
					"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_api:product:operation_inspector",
					"AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL":     "https://files.example.test",
				},
				StoreFactory: func(context.Context, string) (StoreHandle, error) {
					t.Fatal("store factory should not be called with invalid token config")
					return StoreHandle{}, nil
				},
			})
			if err == nil {
				t.Fatal("NewRuntime succeeded, want token config error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "super-secret-token") {
				t.Fatalf("error leaked token: %v", err)
			}
		})
	}
}

func TestInternalRuntimeRejectsMissingWebDAVPublicBaseURLWhenWebDAVAvailable(t *testing.T) {
	_, err := NewRuntime(Options{
		Source: config.MapSource{
			"AFSCP_API_MODE":                                 "internal",
			"AFSCP_API_POSTGRES_DSN":                         "postgres://api:secret@db/afscp",
			"AFSCP_API_SERVICE_TOKENS":                       "svc_api=token-api",
			"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS":    "svc_api:product:operation_inspector",
			"AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS": "svc_api:product:namespace_admin",
			"AFSCP_WEBDAV_ENABLED":                           "true",
			"AFSCP_WEBDAV_READY":                             "true",
		},
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			t.Fatal("store factory should not be called without WebDAV export public base URL")
			return StoreHandle{}, nil
		},
	})
	if err == nil {
		t.Fatal("NewRuntime succeeded, want missing WebDAV export public base URL error")
	}
	if !strings.Contains(err.Error(), "AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL") {
		t.Fatalf("error = %q, want public base URL context", err)
	}
}

func TestInternalRuntimeRejectsMissingWorkloadMountRuntimeSecretRefsWhenMountAvailable(t *testing.T) {
	source := readyTestRuntimeSource()
	delete(source, "AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS")
	_, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			t.Fatal("StoreFactory must not be called before workload mount secret ref config is validated")
			return StoreHandle{}, nil
		},
	})
	if err == nil {
		t.Fatal("NewRuntime succeeded, want workload mount secret ref config error")
	}
	if !strings.Contains(err.Error(), "AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS") {
		t.Fatalf("error = %q, want workload mount secret ref config key", err)
	}
}

func TestInternalRuntimeRejectsInvalidWorkloadMountRuntimeSecretRefsBeforeOpeningStore(t *testing.T) {
	source := readyTestRuntimeSource()
	source["AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS"] = "vol_main=Secret-Ns/runtime-secret-volume"
	_, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			t.Fatal("StoreFactory must not be called before workload mount secret ref config is validated")
			return StoreHandle{}, nil
		},
	})
	if err == nil {
		t.Fatal("NewRuntime succeeded, want workload mount secret ref config error")
	}
	if !strings.Contains(err.Error(), "AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS") {
		t.Fatalf("error = %q, want workload mount secret ref config key", err)
	}
	if strings.Contains(err.Error(), "Secret-Ns") {
		t.Fatalf("error leaked raw secret ref: %v", err)
	}
}

func TestInternalRuntimeAllowsMissingWorkloadMountRuntimeSecretRefsWhenMountUnavailable(t *testing.T) {
	source := readyTestRuntimeSource()
	source["AFSCP_MOUNT_READY"] = "false"
	delete(source, "AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS")
	store := &fakeRuntimeStore{binding: testBinding()}
	runtime, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
		SavePointHistoryRunnerFactory: testSavePointHistoryRunnerFactory(),
	})
	if err != nil {
		t.Fatalf("NewRuntime returned error with workload mount unavailable: %v", err)
	}
	defer closeRuntime(t, runtime)
}

func TestNewRuntimeFromConfigSavePointHistoryVerifiesJVSBinaryAgainstAcceptedPin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jvs")
	content := []byte("not the accepted jvs release binary")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("write fake jvs binary: %v", err)
	}
	sum := sha256.Sum256(content)

	runtime, err := NewRuntimeFromConfig(config.Config{
		API: config.APIConfig{
			Mode:                              "internal",
			PostgresDSN:                       "postgres://api:secret@db/afscp",
			ServiceTokens:                     "svc_api=token-api",
			WebDAVExportPublicBaseURL:         "https://files.example.test",
			DeploymentGlobalAllowedCallers:    "svc_api:product:operation_inspector",
			DeploymentNamespaceAllowedCallers: "svc_api:product:namespace_admin",
			SavePointHistory: config.WorkerRepoCreateRecoveryConfig{
				Enabled:         true,
				JVSBinaryPath:   path,
				JVSBinarySHA256: hex.EncodeToString(sum[:]),
				JVSCWD:          "/var/lib/afscp/jvs-cwd",
				VolumeRoots:     map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
			},
		},
	}, Options{
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			store := &fakeRuntimeStore{binding: testBinding()}
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
	})
	if err == nil {
		defer closeRuntime(t, runtime)
		t.Fatal("NewRuntimeFromConfig succeeded with non-pinned binary hash, want checksum error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %q, want checksum mismatch", err)
	}
}

func TestNewSavePointHistoryJVSRunnerFromConfigAllowsDeclaredDirectRestoreArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jvs-direct-restore")
	content := []byte("direct-capable jvs artifact")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("write fake jvs binary: %v", err)
	}
	sum := sha256.Sum256(content)

	_, err := NewSavePointHistoryJVSRunnerFromConfig(config.WorkerRepoCreateRecoveryConfig{
		Enabled:                   true,
		JVSBinaryPath:             path,
		JVSBinarySHA256:           hex.EncodeToString(sum[:]),
		JVSCWD:                    t.TempDir(),
		JVSDirectRestoreRequired:  true,
		JVSDirectRestoreSourceRef: "jvs@test-direct-restore",
		VolumeRoots:               map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	if err != nil {
		t.Fatalf("NewSavePointHistoryJVSRunnerFromConfig returned error for declared direct artifact: %v", err)
	}
}

func TestNewRuntimeFailsBeforeStoreWhenJVSReadyWithoutSavePointHistoryConfig(t *testing.T) {
	source := readyTestRuntimeSource()
	delete(source, "AFSCP_JVS_BINARY_PATH")

	_, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(context.Context, string) (StoreHandle, error) {
			t.Fatal("store factory should not be called before API JVS history config is validated")
			return StoreHandle{}, nil
		},
	})
	if err == nil {
		t.Fatal("NewRuntime succeeded, want API JVS history config error")
	}
	if !strings.Contains(err.Error(), "AFSCP_JVS_BINARY_PATH") {
		t.Fatalf("error = %q, want JVS binary config key", err)
	}
}

func TestInternalRuntimeJVSReadyWiresSavePointHistoryReaderWithoutHiddenAPIGate(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	binding := testBinding()
	binding.AllowedCallers = []resources.AllowedCaller{{
		CallerService: "svc_api",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
	}}
	store := &fakeRuntimeStore{
		binding: binding,
		namespace: resources.Namespace{
			ID:        "ns_alpha",
			Status:    resources.NamespaceStatusActive,
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now,
		},
		repo: resources.Repo{
			ID:                  "repo_alpha",
			NamespaceID:         "ns_alpha",
			VolumeID:            "vol_main",
			JVSRepoID:           "jvs_repo_alpha",
			Kind:                resources.RepoKindRepo,
			Status:              resources.RepoStatusActive,
			ControlVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/control",
			PayloadVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/payload",
			Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
			CreatedAt:           now.Add(-time.Hour),
			UpdatedAt:           now,
		},
		volume: activeRuntimeVolume(now),
	}
	historyRunner := &runtimeFakeJVSHistoryRunner{summary: jvsrunner.HistorySummary{
		Workspace:         "main",
		NewestSavePointID: "sp_001",
		SavePoints:        []jvsrunner.SavePointSummary{{SavePointID: "sp_001", Message: "first", CreatedAt: "2026-05-05T12:00:00Z"}},
	}}
	var runnerConfig config.WorkerRepoCreateRecoveryConfig
	source := readyTestRuntimeSource()
	delete(source, "AFSCP_API_SAVE_POINT_HISTORY_ENABLED")

	runtime, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
		SavePointHistoryRunnerFactory: func(cfg config.WorkerRepoCreateRecoveryConfig) (api.JVSHistoryRunner, error) {
			runnerConfig = cfg
			return historyRunner, nil
		},
		OperationID: func() string { return "op_savepoint_runtime" },
		Clock:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, internalGET("/internal/v1/repos/repo_alpha/save-points", "svc_api", "token-api"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !runnerConfig.Enabled || runnerConfig.VolumeRoots["vol_main"] != "/srv/afscp/volumes/vol_main" {
		t.Fatalf("runner config = %#v, want API history config from JVS readiness", runnerConfig)
	}
	if historyRunner.calls != 1 ||
		!strings.HasSuffix(historyRunner.directTarget.ControlRoot, "/afscp/namespaces/ns_alpha/repos/repo_alpha/control") ||
		!strings.HasSuffix(historyRunner.directTarget.Home, "/afscp/namespaces/ns_alpha/repos/repo_alpha/payload") {
		t.Fatalf("history runner calls/target = %d/%#v", historyRunner.calls, historyRunner.directTarget)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"save_point_id":"sp_001"`) || strings.Contains(body, string(api.CodeInternalError)) {
		t.Fatalf("body = %s, want save point history without internal error", body)
	}
}

func TestInternalRuntimeServesStorageBackedImplementedSubsetAndFailsClosedForMissingHandlers(t *testing.T) {
	runtime := newTestRuntime(t)
	defer closeRuntime(t, runtime)

	req := internalGET("/internal/v1/namespaces/ns_alpha/volume-binding", "svc_api", "token-api")
	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("binding status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "neutral shell") || strings.Contains(rec.Body.String(), "neutral_api_shell") {
		t.Fatalf("implemented route returned neutral shell text: %s", rec.Body.String())
	}
	var binding api.NamespaceVolumeBindingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatalf("binding response did not decode: %v: %s", err, rec.Body.String())
	}
	if binding.NamespaceID != "ns_alpha" || binding.DefaultVolumeID != "vol_main" {
		t.Fatalf("binding response = %#v", binding)
	}

	exportReq := internalRequest(http.MethodPost, "/internal/v1/repo-templates/tmpl_alpha:clone", "svc_api", "token-api")
	rec = httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, exportReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("template route status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "caller role is not allowed") {
		t.Fatalf("template route body = %s, want implemented route role denial", body)
	}
	if strings.Contains(body, "neutral shell") {
		t.Fatalf("unimplemented route returned neutral shell text: %s", body)
	}
}

func TestInternalRuntimeCreateExportUsesConfiguredWebDAVPublicBaseURL(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	binding := testBinding()
	binding.AllowedCallers = []resources.AllowedCaller{{
		CallerService: "svc_api",
		Roles:         []resources.CallerRole{resources.CallerRoleExportAdmin},
	}}
	store := &fakeRuntimeStore{
		binding: binding,
		namespace: resources.Namespace{
			ID:        "ns_alpha",
			Status:    resources.NamespaceStatusActive,
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now,
		},
		repo: resources.Repo{
			ID:                  "repo_alpha",
			NamespaceID:         "ns_alpha",
			VolumeID:            "vol_main",
			JVSRepoID:           "jvs_repo_alpha",
			Kind:                resources.RepoKindRepo,
			Status:              resources.RepoStatusActive,
			ControlVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/control",
			PayloadVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/payload",
			Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
			CreatedAt:           now.Add(-time.Hour),
			UpdatedAt:           now,
		},
		volume: activeRuntimeVolume(now),
	}
	source := readyTestRuntimeSource()
	volumeRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(volumeRoot, "afscp", "namespaces", "ns_alpha", "repos", "repo_alpha", "payload"), 0o755); err != nil {
		t.Fatalf("mkdir payload root: %v", err)
	}
	source["AFSCP_API_VOLUME_ROOTS"] = "vol_main=" + volumeRoot
	source["AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL"] = "https://files.example.test/public"
	runtime, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
		SavePointHistoryRunnerFactory: testSavePointHistoryRunnerFactory(),
		OperationID:                   func() string { return "op_export_runtime" },
		Clock:                         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer closeRuntime(t, runtime)

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos/repo_alpha/exports", strings.NewReader(`{"mode":"read_only","ttl_seconds":120}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.HeaderAuthorization, "Bearer token-api")
	req.Header.Set(auth.HeaderCorrelationID, "corr_test")
	req.Header.Set(auth.HeaderCallerService, "svc_api")
	req.Header.Set(auth.HeaderNamespaceID, "ns_alpha")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_export")
	req.Header.Set(auth.HeaderActorType, "service")
	req.Header.Set(auth.HeaderActorID, "svc_api")
	rec := httptest.NewRecorder()

	runtime.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	var env api.OperationEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode operation envelope: %v: %s", err, rec.Body.String())
	}
	access, ok := env.Result["access"].(map[string]any)
	if !ok {
		t.Fatalf("result.access = %#v, want object", env.Result["access"])
	}
	got, ok := access["url"].(string)
	if !ok {
		t.Fatalf("access.url = %#v, want string", access["url"])
	}
	parsed, err := url.Parse(got)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		t.Fatalf("access.url = %q, want absolute URI: %v", got, err)
	}
	if !strings.HasPrefix(got, "https://files.example.test/public/e/") || !strings.HasSuffix(got, "/") {
		t.Fatalf("access.url = %q, want configured public base URL", got)
	}
	if store.exportCreateCalls != 1 {
		t.Fatalf("export create calls = %d, want 1", store.exportCreateCalls)
	}
}

func TestInternalRuntimeCreateExportCapabilityDeniedWhenWebDAVUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		enable string
		ready  string
	}{
		{name: "disabled", enable: "false", ready: "false"},
		{name: "not ready", enable: "true", ready: "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(100, 0).UTC()
			binding := testBinding()
			binding.AllowedCallers = []resources.AllowedCaller{{
				CallerService: "svc_api",
				Roles:         []resources.CallerRole{resources.CallerRoleExportAdmin},
			}}
			store := &fakeRuntimeStore{
				binding: binding,
				namespace: resources.Namespace{
					ID:        "ns_alpha",
					Status:    resources.NamespaceStatusActive,
					CreatedAt: now.Add(-time.Hour),
					UpdatedAt: now,
				},
				repo: resources.Repo{
					ID:                  "repo_alpha",
					NamespaceID:         "ns_alpha",
					VolumeID:            "vol_main",
					JVSRepoID:           "jvs_repo_alpha",
					Kind:                resources.RepoKindRepo,
					Status:              resources.RepoStatusActive,
					ControlVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/control",
					PayloadVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/payload",
					Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
					CreatedAt:           now.Add(-time.Hour),
					UpdatedAt:           now,
				},
				volume: activeRuntimeVolume(now),
			}
			source := readyTestRuntimeSource()
			source["AFSCP_WEBDAV_ENABLED"] = tt.enable
			source["AFSCP_WEBDAV_READY"] = tt.ready
			delete(source, "AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL")
			runtime, err := NewRuntime(Options{
				Source: source,
				StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
					if dsn != "postgres://api:secret@db/afscp" {
						t.Fatalf("dsn = %q", dsn)
					}
					return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
				},
				SavePointHistoryRunnerFactory: testSavePointHistoryRunnerFactory(),
				OperationID:                   func() string { return "op_export_runtime" },
				Clock:                         func() time.Time { return now },
			})
			if err != nil {
				t.Fatalf("NewRuntime: %v", err)
			}
			defer closeRuntime(t, runtime)

			if tt.enable == "false" {
				rec := httptest.NewRecorder()
				runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
				}
				var body api.ReadinessResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
				}
				if !body.Ready {
					t.Fatalf("readiness = not ready, want ready with disabled WebDAV")
				}
			}

			req := httptest.NewRequest(http.MethodPost, "/internal/v1/repos/repo_alpha/exports", strings.NewReader(`{"mode":"read_only","ttl_seconds":120}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(auth.HeaderAuthorization, "Bearer token-api")
			req.Header.Set(auth.HeaderCorrelationID, "corr_test")
			req.Header.Set(auth.HeaderCallerService, "svc_api")
			req.Header.Set(auth.HeaderNamespaceID, "ns_alpha")
			req.Header.Set(auth.HeaderIdempotencyKey, "idem_export")
			req.Header.Set(auth.HeaderActorType, "service")
			req.Header.Set(auth.HeaderActorID, "svc_api")
			rec := httptest.NewRecorder()

			runtime.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
			}
			var env api.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode error envelope: %v: %s", err, rec.Body.String())
			}
			if env.Error.Code != api.CodeCapabilityDenied {
				t.Fatalf("error code = %s, want %s", env.Error.Code, api.CodeCapabilityDenied)
			}
			if store.exportCreateCalls != 0 {
				t.Fatalf("export create calls = %d, want denied before durable create", store.exportCreateCalls)
			}
		})
	}
}

func TestServiceTokenPrincipalResolverAuthenticatesBearerAndRejectsCallerMismatch(t *testing.T) {
	resolver, err := NewServiceTokenPrincipalResolver("svc_api=token-api")
	if err != nil {
		t.Fatalf("NewServiceTokenPrincipalResolver: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/namespaces/ns_alpha/volume-binding", nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer token-api")
	principal, err := resolver.ResolvePrincipal(req)
	if err != nil {
		t.Fatalf("ResolvePrincipal: %v", err)
	}
	if principal.CanonicalCallerService != "svc_api" {
		t.Fatalf("canonical caller = %q, want svc_api", principal.CanonicalCallerService)
	}

	runtime := newTestRuntime(t)
	defer closeRuntime(t, runtime)

	mismatch := internalGET("/internal/v1/namespaces/ns_alpha/volume-binding", "svc_other", "token-api")
	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, mismatch)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mismatch status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "caller_service_mismatch") {
		t.Fatalf("mismatch body = %s, want caller_service_mismatch", rec.Body.String())
	}

	badToken := internalGET("/internal/v1/namespaces/ns_alpha/volume-binding", "svc_api", "wrong-secret-token")
	rec = httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, badToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want %d: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "wrong-secret-token") {
		t.Fatalf("bad token response leaked token: %s", rec.Body.String())
	}
}

func TestInternalRuntimeReadinessIsNotNeutralAndDoesNotAdvertiseUnimplementedHandlersReady(t *testing.T) {
	runtime := newTestRuntime(t)
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "neutral_api_shell") {
		t.Fatalf("internal readiness used neutral reason: %s", rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	if !body.Ready {
		t.Fatalf("readiness = not ready, want ready for implemented internal runtime subset")
	}
	if storage := body.Capabilities[api.CapabilityStorage]; !storage.Enabled || !storage.Ready || storage.Gated {
		t.Fatalf("storage gate = %#v, want ready storage", storage)
	}
	webdav := body.Capabilities[api.CapabilityWebDAVExport]
	if !webdav.Enabled || !webdav.Ready || webdav.Gated || webdav.Reason != "" {
		t.Fatalf("webdav gate = %#v, want enabled and ready", webdav)
	}
	if _, ok := body.Capabilities[api.CapabilityWorkloadMount]; ok {
		t.Fatalf("readiness advertised legacy coarse workload capability %q", api.CapabilityWorkloadMount)
	}
	for _, capability := range []string{api.CapabilityWorkloadMountBinding, api.CapabilityWorkloadMountDiscovery, api.CapabilityWorkloadTeardownPlan} {
		gate := body.Capabilities[capability]
		if !gate.Enabled || !gate.Ready || gate.Gated || gate.Reason != "" {
			t.Fatalf("%s gate = %#v, want enabled and ready", capability, gate)
		}
	}
}

func TestInternalRuntimeReadinessGatesUnconfiguredWebDAVButDoesNotRequireIt(t *testing.T) {
	readiness := internalReadiness(config.Config{})
	gate := readiness.Capabilities[api.CapabilityWebDAVExport]
	if gate.Enabled || gate.Ready || !gate.Gated || gate.Reason != "webdav_not_configured" {
		t.Fatalf("webdav gate = %#v, want unconfigured gate", gate)
	}
	for _, required := range readiness.RequiredCapabilities {
		if required == api.CapabilityWebDAVExport {
			t.Fatalf("required capabilities = %#v, want webdav omitted when disabled", readiness.RequiredCapabilities)
		}
	}
}

func TestInternalRuntimeReadinessDefaultProfileDoesNotRequireDisabledWebDAV(t *testing.T) {
	runtime := newTestRuntimeWithSourceOverrides(t, config.MapSource{
		"AFSCP_WEBDAV_ENABLED": "false",
		"AFSCP_WEBDAV_READY":   "false",
	}, func(context.Context) error { return nil })
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	if !body.Ready {
		t.Fatalf("readiness = not ready, want default runtime profile to omit disabled WebDAV")
	}
}

func TestInternalRuntimeReadinessDefaultProfileRequiresStaticStorageCapability(t *testing.T) {
	tests := []struct {
		name        string
		overrides   config.MapSource
		wantEnabled bool
		wantReason  string
	}{
		{
			name:        "storage config missing",
			overrides:   config.MapSource{},
			wantEnabled: false,
			wantReason:  "storage_not_configured",
		},
		{
			name: "storage disabled",
			overrides: config.MapSource{
				"AFSCP_STORAGE_ENABLED": "false",
				"AFSCP_STORAGE_READY":   "false",
			},
			wantEnabled: false,
			wantReason:  "storage_not_configured",
		},
		{
			name: "storage enabled but unready",
			overrides: config.MapSource{
				"AFSCP_STORAGE_ENABLED": "true",
				"AFSCP_STORAGE_READY":   "false",
			},
			wantEnabled: true,
			wantReason:  "storage_not_ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := baseTestRuntimeSource()
			for key, value := range tt.overrides {
				source[key] = value
			}
			pingCalls := 0
			runtime := newTestRuntimeWithSource(t, source, func(context.Context) error {
				pingCalls++
				return nil
			})
			defer closeRuntime(t, runtime)

			rec := httptest.NewRecorder()
			runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}
			if pingCalls != 0 {
				t.Fatalf("storage ping calls = %d, want 0 when static storage is unavailable", pingCalls)
			}

			var body api.ReadinessResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
			}
			if body.Ready {
				t.Fatalf("readiness = ready, want not ready when static storage is unavailable")
			}
			storage := body.Capabilities[api.CapabilityStorage]
			if storage.Enabled != tt.wantEnabled || storage.Ready || !storage.Gated || storage.Reason != tt.wantReason {
				t.Fatalf("storage gate = %#v, want enabled=%t ready=false gated=true reason=%q", storage, tt.wantEnabled, tt.wantReason)
			}
		})
	}
}

func TestInternalRuntimeReadinessGAProfileRequiresDefaultGACapabilitySet(t *testing.T) {
	tests := []struct {
		name      string
		overrides config.MapSource
		wantGate  string
		wantCode  int
	}{
		{
			name: "jvs missing",
			overrides: config.MapSource{
				"AFSCP_JVS_ENABLED": "false",
				"AFSCP_JVS_READY":   "false",
			},
			wantGate: api.CapabilityJVS,
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name: "storage disabled",
			overrides: config.MapSource{
				"AFSCP_STORAGE_ENABLED": "false",
				"AFSCP_STORAGE_READY":   "false",
			},
			wantGate: api.CapabilityStorage,
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name: "storage unready",
			overrides: config.MapSource{
				"AFSCP_STORAGE_ENABLED": "true",
				"AFSCP_STORAGE_READY":   "false",
			},
			wantGate: api.CapabilityStorage,
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name: "webdav disabled",
			overrides: config.MapSource{
				"AFSCP_WEBDAV_ENABLED": "false",
				"AFSCP_WEBDAV_READY":   "false",
			},
			wantGate: api.CapabilityWebDAVExport,
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:      "all ready",
			overrides: config.MapSource{},
			wantCode:  http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overrides := config.MapSource{"AFSCP_READINESS_PROFILE": "ga"}
			for key, value := range tt.overrides {
				overrides[key] = value
			}
			runtime := newTestRuntimeWithSourceOverrides(t, overrides, func(context.Context) error { return nil })
			defer closeRuntime(t, runtime)

			rec := httptest.NewRecorder()
			runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("readiness status = %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}

			var body api.ReadinessResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
			}
			if tt.wantCode == http.StatusOK {
				if !body.Ready {
					t.Fatalf("readiness = not ready, want ready")
				}
				return
			}
			if body.Ready {
				t.Fatalf("readiness = ready, want GA profile not ready")
			}
			gate := body.Capabilities[tt.wantGate]
			if gate.Enabled && gate.Ready && !gate.Gated {
				t.Fatalf("%s gate = %#v, want gated or unavailable", tt.wantGate, gate)
			}
		})
	}
}

func TestInternalRuntimeReadinessGatesJVSHistoryCapabilitiesWhenReaderConfigMissing(t *testing.T) {
	cfg := config.Config{
		ReadinessProfile: config.ReadinessProfileGA,
		API: config.APIConfig{
			Mode:                              "internal",
			ServiceTokens:                     "svc_api=token-api",
			DeploymentGlobalAllowedCallers:    "svc_api:admin:volume_admin|operation_inspector",
			DeploymentNamespaceAllowedCallers: "svc_api:product:namespace_admin",
		},
		Capabilities: config.Capabilities{
			Storage: config.Capability{Enabled: true, Ready: true},
			JVS:     config.Capability{Enabled: true, Ready: true},
			WebDAV:  config.Capability{Enabled: true, Ready: true},
		},
	}
	readiness := internalReadiness(cfg)
	rec := httptest.NewRecorder()
	api.ReadinessHandler(readiness).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	for _, capabilityID := range []string{api.CapabilityJVSSaveRestore, api.CapabilityJVSProjection} {
		gate, ok := body.Capabilities[capabilityID]
		if !ok {
			t.Fatalf("missing readiness capability %q", capabilityID)
		}
		if !gate.RequiredForServiceReady || !gate.RequiredForDefaultGA || gate.OptionalGated {
			t.Fatalf("%s gate = %#v, want default GA required service gate", capabilityID, gate)
		}
		if !gate.Enabled || gate.Ready || !gate.Gated || gate.Reason != "save_point_history_not_configured" {
			t.Fatalf("%s gate = %#v, want save point history config gate", capabilityID, gate)
		}
	}
	if body.Ready {
		t.Fatalf("readiness = ready, want not ready while JVS history reader config is missing")
	}
}

func TestInternalRuntimeReadinessIncludesAdminBootstrapFacets(t *testing.T) {
	runtime := newTestRuntime(t)
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	for _, capability := range []string{
		api.CapabilityNamespaceBinding,
		api.CapabilityVolumePreflight,
		api.CapabilityCallerPolicyReadiness,
		api.CapabilityPathRedaction,
		api.CapabilityAdminBootstrap,
	} {
		gate, ok := body.Capabilities[capability]
		if !ok {
			t.Fatalf("missing admin bootstrap facet %q", capability)
		}
		if !gate.Enabled || !gate.Ready || gate.Gated || gate.Reason != "" {
			t.Fatalf("%s gate = %#v, want ready admin bootstrap facet", capability, gate)
		}
	}
}

func TestInternalRuntimeReadinessGAProfileRequiresAdminBootstrap(t *testing.T) {
	runtime := newTestRuntimeWithSourceOverrides(t, config.MapSource{
		"AFSCP_READINESS_PROFILE": "ga",
	}, func(context.Context) error {
		return errors.New("dial tcp postgres://api:secret@db/afscp")
	})
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	for _, capability := range []string{
		api.CapabilityNamespaceBinding,
		api.CapabilityVolumePreflight,
		api.CapabilityCallerPolicyReadiness,
		api.CapabilityPathRedaction,
		api.CapabilityAdminBootstrap,
	} {
		gate, ok := body.Capabilities[capability]
		if !ok {
			t.Fatalf("missing GA admin bootstrap facet %q", capability)
		}
		if !gate.RequiredForServiceReady || !gate.RequiredForDefaultGA || gate.OptionalGated {
			t.Fatalf("%s gate = %#v, want required for GA service readiness", capability, gate)
		}
	}
	admin := body.Capabilities[api.CapabilityAdminBootstrap]
	if !admin.Enabled || admin.Ready || !admin.Gated || admin.Reason != "admin_bootstrap_dependency_not_ready" {
		t.Fatalf("admin bootstrap gate = %#v, want fixed dependency failure", admin)
	}
}

func TestInternalRuntimeReadinessAdminBootstrapGatesOnStoragePingWithoutLeakingErrors(t *testing.T) {
	runtime := newTestRuntimeWithStorePing(t, func(context.Context) error {
		return errors.New("postgres://api:secret@db/afscp root=/srv/afscp/volumes/vol_main SecretRef=ns/runtime credential=.jvs")
	})
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	volume := body.Capabilities[api.CapabilityVolumePreflight]
	if !volume.Enabled || volume.Ready || !volume.Gated || volume.Reason != "volume_preflight_storage_not_ready" {
		t.Fatalf("volume preflight gate = %#v, want redacted storage ping failure", volume)
	}
	admin := body.Capabilities[api.CapabilityAdminBootstrap]
	if !admin.Enabled || admin.Ready || !admin.Gated || admin.Reason != "admin_bootstrap_dependency_not_ready" {
		t.Fatalf("admin bootstrap gate = %#v, want fixed aggregate failure", admin)
	}
	rendered := rec.Body.String()
	for _, leaked := range []string{"postgres://api", "secret", "/srv/afscp", "SecretRef", "credential", ".jvs"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("readiness response leaked %q in %s", leaked, rendered)
		}
	}
}

func TestInternalRuntimeReadinessFailsClosedWithoutAPIVolumeRootsWhenStorageAvailable(t *testing.T) {
	source := readyTestRuntimeSource()
	delete(source, "AFSCP_API_VOLUME_ROOTS")
	source["AFSCP_JVS_ENABLED"] = "false"
	source["AFSCP_JVS_READY"] = "false"
	runtime := newTestRuntimeWithSource(t, source, func(context.Context) error { return nil })
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	if body.Ready {
		t.Fatalf("readiness = ready, want not ready without configured API volume roots")
	}
	volume := body.Capabilities[api.CapabilityVolumePreflight]
	if !volume.Enabled || volume.Ready || !volume.Gated || volume.Reason != "volume_preflight_volume_roots_missing" {
		t.Fatalf("volume preflight gate = %#v, want missing API volume roots", volume)
	}
	admin := body.Capabilities[api.CapabilityAdminBootstrap]
	if !admin.Enabled || admin.Ready || !admin.Gated || admin.Reason != "admin_bootstrap_dependency_not_ready" {
		t.Fatalf("admin bootstrap gate = %#v, want fixed aggregate failure", admin)
	}
}

func TestInternalRuntimeReadinessAdminBootstrapGatesOnMissingConfiguredVolumeRecord(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	source := readyTestRuntimeSource()
	runtime, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			store := &fakeRuntimeStore{binding: testBinding()}
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
		SavePointHistoryRunnerFactory: testSavePointHistoryRunnerFactory(),
		Clock:                         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	volume := body.Capabilities[api.CapabilityVolumePreflight]
	if !volume.Enabled || volume.Ready || !volume.Gated || volume.Reason != "volume_preflight_configured_volume_missing" {
		t.Fatalf("volume preflight gate = %#v, want configured volume missing", volume)
	}
	admin := body.Capabilities[api.CapabilityAdminBootstrap]
	if !admin.Enabled || admin.Ready || !admin.Gated || admin.Reason != "admin_bootstrap_dependency_not_ready" {
		t.Fatalf("admin bootstrap gate = %#v, want fixed aggregate failure", admin)
	}
	rendered := rec.Body.String()
	for _, leaked := range []string{"postgres://api", "secret", "/srv/afscp", "vol_main"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("readiness response leaked %q in %s", leaked, rendered)
		}
	}
}

func TestInternalRuntimeReadinessAdminBootstrapRequiresUsableCallerPolicyRoles(t *testing.T) {
	runtime := newTestRuntimeWithSourceOverrides(t, config.MapSource{
		"AFSCP_API_SERVICE_TOKENS":                    "svc_api=secret-token",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_api:product:operation_inspector",
	}, func(context.Context) error { return nil })
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	callerPolicy := body.Capabilities[api.CapabilityCallerPolicyReadiness]
	if callerPolicy.Enabled || callerPolicy.Ready || !callerPolicy.Gated || callerPolicy.Reason != "caller_policy_missing_bootstrap_role" {
		t.Fatalf("caller policy gate = %#v, want missing bootstrap role", callerPolicy)
	}
	admin := body.Capabilities[api.CapabilityAdminBootstrap]
	if !admin.Enabled || admin.Ready || !admin.Gated || admin.Reason != "admin_bootstrap_dependency_not_ready" {
		t.Fatalf("admin bootstrap gate = %#v, want aggregate dependency failure", admin)
	}
	rendered := rec.Body.String()
	for _, leaked := range []string{"secret-token", "secret"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("readiness response leaked %q in %s", leaked, rendered)
		}
	}
}

func TestInternalRuntimeReadinessAdminBootstrapRequiresPolicyCallersToBeAuthenticatable(t *testing.T) {
	runtime := newTestRuntimeWithSourceOverrides(t, config.MapSource{
		"AFSCP_API_SERVICE_TOKENS":                    "svc_api=token-api",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_volume:admin:volume_admin,svc_api:operator:operator_admin",
	}, func(context.Context) error { return nil })
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	callerPolicy := body.Capabilities[api.CapabilityCallerPolicyReadiness]
	if callerPolicy.Enabled || callerPolicy.Ready || !callerPolicy.Gated || callerPolicy.Reason != "caller_policy_not_configured" {
		t.Fatalf("caller policy gate = %#v, want unauthenticatable policy caller to fail readiness", callerPolicy)
	}
	admin := body.Capabilities[api.CapabilityAdminBootstrap]
	if !admin.Enabled || admin.Ready || !admin.Gated || admin.Reason != "admin_bootstrap_dependency_not_ready" {
		t.Fatalf("admin bootstrap gate = %#v, want aggregate dependency failure", admin)
	}
}

func TestInternalRuntimeReadinessAdminBootstrapDoesNotRequireDefaultUserLoop(t *testing.T) {
	runtime := newTestRuntimeWithSourceOverrides(t, config.MapSource{
		"AFSCP_JVS_ENABLED":    "false",
		"AFSCP_JVS_READY":      "false",
		"AFSCP_WEBDAV_ENABLED": "false",
		"AFSCP_WEBDAV_READY":   "false",
	}, func(context.Context) error { return nil })
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	admin := body.Capabilities[api.CapabilityAdminBootstrap]
	if !admin.Enabled || !admin.Ready || admin.Gated || admin.Reason != "" {
		t.Fatalf("admin bootstrap gate = %#v, want ready without JVS/WebDAV default user loop", admin)
	}
}

func TestInternalRuntimeReadinessGAProfileTreatsWorkloadMountAsOptionalGated(t *testing.T) {
	tests := []struct {
		name       string
		overrides  config.MapSource
		wantReason string
	}{
		{
			name: "disabled",
			overrides: config.MapSource{
				"AFSCP_MOUNT_ENABLED": "false",
				"AFSCP_MOUNT_READY":   "false",
			},
			wantReason: "mount_not_configured",
		},
		{
			name: "unready",
			overrides: config.MapSource{
				"AFSCP_MOUNT_ENABLED": "true",
				"AFSCP_MOUNT_READY":   "false",
			},
			wantReason: "mount_not_ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overrides := config.MapSource{"AFSCP_READINESS_PROFILE": "ga"}
			for key, value := range tt.overrides {
				overrides[key] = value
			}
			runtime := newTestRuntimeWithSourceOverrides(t, overrides, func(context.Context) error { return nil })
			defer closeRuntime(t, runtime)

			rec := httptest.NewRecorder()
			runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			var body struct {
				Ready        bool                      `json:"ready"`
				Capabilities map[string]map[string]any `json:"capabilities"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
			}
			if !body.Ready {
				t.Fatalf("readiness = not ready, want ready when only workload mount is gated")
			}
			if _, ok := body.Capabilities[api.CapabilityWorkloadMount]; ok {
				t.Fatalf("readiness advertised legacy coarse workload capability %q", api.CapabilityWorkloadMount)
			}
			for _, capability := range []string{api.CapabilityStorage, api.CapabilityJVS, api.CapabilityWebDAVExport} {
				gate := body.Capabilities[capability]
				if !runtimeReadinessBoolField(t, gate, "required_for_service_ready") {
					t.Fatalf("%s required_for_service_ready = false, want true", capability)
				}
				if !runtimeReadinessBoolField(t, gate, "required_for_default_ga") {
					t.Fatalf("%s required_for_default_ga = false, want true", capability)
				}
				if runtimeReadinessBoolField(t, gate, "optional_gated") {
					t.Fatalf("%s optional_gated = true, want false", capability)
				}
			}

			for capability, wantReason := range map[string]string{
				api.CapabilityWorkloadMountBinding:   tt.wantReason,
				api.CapabilityWorkloadMountDiscovery: tt.wantReason,
				api.CapabilityWorkloadTeardownPlan:   tt.wantReason,
				api.CapabilityRepoTemplate:           "repo_template_not_configured",
				api.CapabilityRepoPurge:              "repo_purge_not_configured",
			} {
				gate := body.Capabilities[capability]
				if runtimeReadinessBoolField(t, gate, "required_for_service_ready") {
					t.Fatalf("%s required_for_service_ready = true, want false", capability)
				}
				if runtimeReadinessBoolField(t, gate, "required_for_default_ga") {
					t.Fatalf("%s required_for_default_ga = true, want false", capability)
				}
				if !runtimeReadinessBoolField(t, gate, "optional_gated") {
					t.Fatalf("%s optional_gated = false, want true", capability)
				}
				if got, _ := gate["reason"].(string); got != wantReason {
					t.Fatalf("%s reason = %q, want %q", capability, got, wantReason)
				}
			}
		})
	}
}

func TestDiscoverySurfacesReadyzDoesNotPromoteOptionalRuntimeDefaultReady(t *testing.T) {
	runtime := newTestRuntimeWithSourceOverrides(t, config.MapSource{
		"AFSCP_READINESS_PROFILE": "ga",
		"AFSCP_MOUNT_ENABLED":     "false",
		"AFSCP_MOUNT_READY":       "false",
	}, func(context.Context) error { return nil })
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Ready        bool                      `json:"ready"`
		Capabilities map[string]map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	if !body.Ready {
		t.Fatalf("readiness = not ready, want ready when only optional workload runtime is disabled")
	}
	for _, capability := range []string{api.CapabilityWorkloadMountBinding, api.CapabilityWorkloadMountDiscovery, api.CapabilityWorkloadTeardownPlan} {
		gate := body.Capabilities[capability]
		if runtimeReadinessBoolField(t, gate, "required_for_default_ga") || runtimeReadinessBoolField(t, gate, "required_for_service_ready") {
			t.Fatalf("%s gate = %#v, want optional discovery/runtime surface outside default/service readiness", capability, gate)
		}
		if !runtimeReadinessBoolField(t, gate, "optional_gated") {
			t.Fatalf("%s optional_gated = false, want true", capability)
		}
	}
	if strings.Contains(rec.Body.String(), "orchestrator_mount") || strings.Contains(rec.Body.String(), "operator_admin") {
		t.Fatalf("readyz leaked authorization roles instead of readiness state: %s", rec.Body.String())
	}
}

func TestInternalRuntimeReadinessRuntimeProfileRequiresOptedInWorkloadMountFacets(t *testing.T) {
	runtime := newTestRuntimeWithSourceOverrides(t, config.MapSource{
		"AFSCP_MOUNT_ENABLED": "true",
		"AFSCP_MOUNT_READY":   "false",
	}, func(context.Context) error { return nil })
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	if body.Ready {
		t.Fatalf("readiness = ready, want not ready when workload mount is explicitly enabled but unready")
	}
	if _, ok := body.Capabilities[api.CapabilityWorkloadMount]; ok {
		t.Fatalf("readiness advertised legacy coarse workload capability %q", api.CapabilityWorkloadMount)
	}
	for _, capability := range []string{api.CapabilityWorkloadMountBinding, api.CapabilityWorkloadMountDiscovery, api.CapabilityWorkloadTeardownPlan} {
		gate, ok := body.Capabilities[capability]
		if !ok {
			t.Fatalf("missing split workload capability %q", capability)
		}
		if !gate.RequiredForServiceReady || gate.OptionalGated {
			t.Fatalf("%s gate = %#v, want runtime opt-in required", capability, gate)
		}
		if !gate.Enabled || gate.Ready || !gate.Gated || gate.Reason != "mount_not_ready" {
			t.Fatalf("%s gate = %#v, want enabled unready gated reason mount_not_ready", capability, gate)
		}
	}
}

func TestInternalRuntimeReadinessRuntimeProfileRequiresOptedInRepoOptionalCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		overrides  config.MapSource
		capability string
		wantReason string
	}{
		{
			name: "repo template unready",
			overrides: config.MapSource{
				"AFSCP_REPO_TEMPLATE_ENABLED": "true",
				"AFSCP_REPO_TEMPLATE_READY":   "false",
			},
			capability: api.CapabilityRepoTemplate,
			wantReason: "repo_template_not_ready",
		},
		{
			name: "repo purge unready",
			overrides: config.MapSource{
				"AFSCP_REPO_PURGE_ENABLED": "true",
				"AFSCP_REPO_PURGE_READY":   "false",
			},
			capability: api.CapabilityRepoPurge,
			wantReason: "repo_purge_not_ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := newTestRuntimeWithSourceOverrides(t, tt.overrides, func(context.Context) error { return nil })
			defer closeRuntime(t, runtime)

			rec := httptest.NewRecorder()
			runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}

			var body api.ReadinessResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
			}
			if body.Ready {
				t.Fatalf("readiness = ready, want not ready when %s is explicitly enabled but unready", tt.capability)
			}
			gate := body.Capabilities[tt.capability]
			if !gate.RequiredForServiceReady || gate.OptionalGated {
				t.Fatalf("%s gate = %#v, want runtime opt-in required", tt.capability, gate)
			}
			if !gate.Enabled || gate.Ready || !gate.Gated || gate.Reason != tt.wantReason {
				t.Fatalf("%s gate = %#v, want enabled unready gated reason %q", tt.capability, gate, tt.wantReason)
			}
		})
	}
}

func TestInternalRuntimeAdmissionDisabledFlagsMatchCapabilityMatrix(t *testing.T) {
	source := readyTestRuntimeSource()
	source["AFSCP_MOUNT_ENABLED"] = "false"
	source["AFSCP_MOUNT_READY"] = "false"
	source["AFSCP_REPO_TEMPLATE_ENABLED"] = "false"
	source["AFSCP_REPO_TEMPLATE_READY"] = "false"
	source["AFSCP_REPO_PURGE_ENABLED"] = "false"
	source["AFSCP_REPO_PURGE_READY"] = "false"
	cfg, err := config.Load(source)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	disabled := apiAdmissionDisabledCapabilities(cfg)
	for _, row := range capability.DecisionRowsForSurface(capability.SurfaceAPIAdmission) {
		got := disabled[row.CapabilityID]
		if row.OptionalGated && !got {
			t.Fatalf("%s/%s optional matrix admission capability %s disabled = false", row.OperationType, row.SurfaceType, row.CapabilityID)
		}
		if !row.OptionalGated && got {
			t.Fatalf("%s/%s default matrix admission capability %s disabled = true", row.OperationType, row.SurfaceType, row.CapabilityID)
		}
	}
}

func TestInternalRuntimeWiresRepoTemplateAndPurgeCapabilitiesToAdmission(t *testing.T) {
	source := readyTestRuntimeSource()
	source["AFSCP_REPO_TEMPLATE_ENABLED"] = "false"
	source["AFSCP_REPO_TEMPLATE_READY"] = "false"
	source["AFSCP_REPO_PURGE_ENABLED"] = "false"
	source["AFSCP_REPO_PURGE_READY"] = "false"

	binding := testBinding()
	binding.AllowedCallers = []resources.AllowedCaller{{
		CallerService: "svc_api",
		Roles: []resources.CallerRole{
			resources.CallerRoleTemplateAdmin,
			resources.CallerRoleRepoLifecycleAdmin,
		},
	}}
	store := &fakeRuntimeStore{binding: binding}
	runtime, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
		SavePointHistoryRunnerFactory: testSavePointHistoryRunnerFactory(),
		OperationID:                   func() string { return "op_test" },
		Clock:                         func() time.Time { return time.Unix(100, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer closeRuntime(t, runtime)

	tests := []struct {
		name        string
		request     *http.Request
		wantMessage string
	}{
		{
			name:        "repo template create",
			request:     internalPOST("/internal/v1/repo-templates", "svc_api", "token-api", `{"namespace_id":"ns_alpha","source_repo_id":"repo_alpha","target_template_id":"tmpl_alpha","clone_history_mode":"main"}`),
			wantMessage: "repo template admission is disabled",
		},
		{
			name:        "repo purge",
			request:     internalPOST("/internal/v1/repos/repo_alpha:purge", "svc_api", "token-api", `{"reason":"remove requested","product_confirmation_ref":"confirm-alpha"}`),
			wantMessage: "repo purge admission is disabled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeRepoReads := store.repoInNamespaceCalls
			beforeGenericIntake := store.operationCreateCalls
			beforeTemplateIntake := store.templateCreateCalls + store.templateCloneCalls
			rec := httptest.NewRecorder()

			runtime.Handler.ServeHTTP(rec, tt.request)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
			}
			env := decodeRuntimeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != api.CodeCapabilityDenied {
				t.Fatalf("error code = %s, want %s; body=%s", env.Error.Code, api.CodeCapabilityDenied, rec.Body.String())
			}
			if env.Error.Message != tt.wantMessage {
				t.Fatalf("error message = %q, want %q; body=%s", env.Error.Message, tt.wantMessage, rec.Body.String())
			}
			if store.repoInNamespaceCalls != beforeRepoReads || store.operationCreateCalls != beforeGenericIntake || store.templateCreateCalls+store.templateCloneCalls != beforeTemplateIntake {
				t.Fatalf("metadata/intake calls changed repo=%d->%d generic=%d->%d template=%d->%d", beforeRepoReads, store.repoInNamespaceCalls, beforeGenericIntake, store.operationCreateCalls, beforeTemplateIntake, store.templateCreateCalls+store.templateCloneCalls)
			}
		})
	}
}

func TestInternalRuntimeReadinessChecksStoreHealthWithoutLeakingErrors(t *testing.T) {
	runtime := newTestRuntimeWithStorePing(t, func(context.Context) error {
		return errors.New("postgres://api:secret@db/afscp token=store-token Authorization: Bearer bad")
	})
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	if body.Ready {
		t.Fatalf("readiness = ready, want not ready when storage ping fails")
	}
	storage := body.Capabilities[api.CapabilityStorage]
	if !storage.Enabled || storage.Ready || !storage.Gated || storage.Reason != "storage_not_ready" {
		t.Fatalf("storage gate = %#v, want storage_not_ready", storage)
	}
	rendered := rec.Body.String()
	for _, leaked := range []string{"postgres://api", "secret", "store-token", "Bearer bad"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("readiness response leaked %q in %s", leaked, rendered)
		}
	}
}

func TestInternalRuntimeReadinessFailsClosedWithoutStorePing(t *testing.T) {
	runtime := newTestRuntimeWithStorePing(t, nil)
	defer closeRuntime(t, runtime)

	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness did not decode: %v: %s", err, rec.Body.String())
	}
	if body.Ready {
		t.Fatalf("readiness = ready, want not ready when storage ping is missing")
	}
	storage := body.Capabilities[api.CapabilityStorage]
	if !storage.Enabled || storage.Ready || !storage.Gated || storage.Reason != "storage_health_check_missing" {
		t.Fatalf("storage gate = %#v, want storage_health_check_missing", storage)
	}
}

func TestInternalRuntimeVolumeHealthDefaultsMissingBackendProbeToNotHealthy(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	runtime, err := NewRuntime(Options{
		Source: config.MapSource{
			"AFSCP_API_MODE":                                 "internal",
			"AFSCP_API_POSTGRES_DSN":                         "postgres://api:secret@db/afscp",
			"AFSCP_API_SERVICE_TOKENS":                       "svc_api=token-api",
			"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS":    "svc_api:admin:volume_admin",
			"AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS": "svc_api:product:namespace_admin",
			"AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL":        "https://files.example.test",
		},
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			store := &fakeRuntimeStore{binding: testBinding(), volume: activeRuntimeVolume(now)}
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer closeRuntime(t, runtime)

	req := internalGET("/internal/v1/volumes/vol_main/health", "svc_api", "token-api")
	req.Header.Del(auth.HeaderNamespaceID)
	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("volume health status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body api.VolumeHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("volume health did not decode: %v: %s", err, rec.Body.String())
	}
	if body.Status == "healthy" {
		t.Fatalf("volume health = healthy, want missing backend probe finding: %#v", body)
	}
	if !runtimeVolumeHealthHasFinding(body, "BACKEND_PROBE_MISSING") {
		t.Fatalf("findings = %#v, want BACKEND_PROBE_MISSING", body.Findings)
	}
}

func TestInternalRuntimeVolumeHealthUsesConfiguredVolumeRootProbe(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	root := t.TempDir()
	runtime := newVolumeHealthRuntime(t, config.MapSource{
		"AFSCP_API_VOLUME_ROOTS": "vol_main=" + root,
	}, activeRuntimeVolume(now))
	defer closeRuntime(t, runtime)

	rec := serveRuntimeVolumeHealth(runtime)
	if rec.Code != http.StatusOK {
		t.Fatalf("volume health status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body api.VolumeHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("volume health did not decode: %v: %s", err, rec.Body.String())
	}
	if body.Status != "healthy" || len(body.Findings) != 0 {
		t.Fatalf("volume health = %#v, want healthy without findings", body)
	}
	if strings.Contains(rec.Body.String(), root) {
		t.Fatalf("volume health leaked configured root %q: %s", root, rec.Body.String())
	}
}

func TestInternalRuntimeVolumeHealthRootProbeFailuresAreSanitized(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	fileRoot := filepath.Join(t.TempDir(), "secret-volume-file")
	if err := os.WriteFile(fileRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write file root: %v", err)
	}
	missingRoot := filepath.Join(t.TempDir(), "secret-missing-volume-root")
	otherRoot := filepath.Join(t.TempDir(), "secret-other-volume-root")
	if err := os.Mkdir(otherRoot, 0o700); err != nil {
		t.Fatalf("mkdir other root: %v", err)
	}

	tests := []struct {
		name    string
		roots   string
		leakTag string
	}{
		{name: "root missing", roots: "vol_main=" + missingRoot, leakTag: missingRoot},
		{name: "root not directory", roots: "vol_main=" + fileRoot, leakTag: fileRoot},
		{name: "volume id missing", roots: "vol_other=" + otherRoot, leakTag: otherRoot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := newVolumeHealthRuntime(t, config.MapSource{
				"AFSCP_API_VOLUME_ROOTS": tt.roots,
			}, activeRuntimeVolume(now))
			defer closeRuntime(t, runtime)

			rec := serveRuntimeVolumeHealth(runtime)
			if rec.Code != http.StatusOK {
				t.Fatalf("volume health status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var body api.VolumeHealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("volume health did not decode: %v: %s", err, rec.Body.String())
			}
			if body.Status == "healthy" {
				t.Fatalf("volume health = healthy, want backend probe finding: %#v", body)
			}
			if !runtimeVolumeHealthHasFinding(body, "BACKEND_PROBE_FAILED") {
				t.Fatalf("findings = %#v, want BACKEND_PROBE_FAILED", body.Findings)
			}
			for _, leaked := range []string{tt.leakTag, "secret-volume"} {
				if strings.Contains(rec.Body.String(), leaked) {
					t.Fatalf("volume health leaked %q in %s", leaked, rec.Body.String())
				}
			}
		})
	}
}

func TestInternalRuntimeVolumeHealthRootProbeRejectsSymlinkAndMissingPermissions(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	targetRoot := filepath.Join(t.TempDir(), "secret-volume-target")
	if err := os.Mkdir(targetRoot, 0o700); err != nil {
		t.Fatalf("mkdir target root: %v", err)
	}
	symlinkRoot := filepath.Join(t.TempDir(), "secret-volume-symlink")
	if err := os.Symlink(targetRoot, symlinkRoot); err != nil {
		t.Fatalf("symlink root: %v", err)
	}
	noWriteRoot := filepath.Join(t.TempDir(), "secret-volume-no-write")
	if err := os.Mkdir(noWriteRoot, 0o500); err != nil {
		t.Fatalf("mkdir no-write root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(noWriteRoot, 0o700) })
	noSearchRoot := filepath.Join(t.TempDir(), "secret-volume-no-search")
	if err := os.Mkdir(noSearchRoot, 0o600); err != nil {
		t.Fatalf("mkdir no-search root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(noSearchRoot, 0o700) })

	tests := []struct {
		name string
		root string
	}{
		{name: "symlink", root: symlinkRoot},
		{name: "no write permission bits", root: noWriteRoot},
		{name: "no execute search permission bits", root: noSearchRoot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := newVolumeHealthRuntime(t, config.MapSource{
				"AFSCP_API_VOLUME_ROOTS": "vol_main=" + tt.root,
			}, activeRuntimeVolume(now))
			defer closeRuntime(t, runtime)

			rec := serveRuntimeVolumeHealth(runtime)
			if rec.Code != http.StatusOK {
				t.Fatalf("volume health status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var body api.VolumeHealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("volume health did not decode: %v: %s", err, rec.Body.String())
			}
			if body.Status == "healthy" {
				t.Fatalf("volume health = healthy, want backend probe finding: %#v", body)
			}
			if !runtimeVolumeHealthHasFinding(body, "BACKEND_PROBE_FAILED") {
				t.Fatalf("findings = %#v, want BACKEND_PROBE_FAILED", body.Findings)
			}
			for _, leaked := range []string{tt.root, targetRoot, "secret-volume"} {
				if strings.Contains(rec.Body.String(), leaked) {
					t.Fatalf("volume health leaked %q in %s", leaked, rec.Body.String())
				}
			}
		})
	}
}

func runtimeReadinessBoolField(t *testing.T, fields map[string]any, name string) bool {
	t.Helper()
	raw, ok := fields[name]
	if !ok {
		t.Fatalf("capability fields %#v missing %q", fields, name)
	}
	got, ok := raw.(bool)
	if !ok {
		t.Fatalf("capability field %q = %#v, want bool", name, raw)
	}
	return got
}

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	return newTestRuntimeWithStorePing(t, func(context.Context) error {
		return nil
	})
}

func newVolumeHealthRuntime(t *testing.T, overrides config.MapSource, volume resources.Volume) *Runtime {
	t.Helper()
	source := config.MapSource{
		"AFSCP_API_MODE":                                 "internal",
		"AFSCP_API_POSTGRES_DSN":                         "postgres://api:secret@db/afscp",
		"AFSCP_API_SERVICE_TOKENS":                       "svc_api=token-api",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS":    "svc_api:admin:volume_admin",
		"AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS": "svc_api:product:namespace_admin",
		"AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL":        "https://files.example.test",
	}
	for key, value := range overrides {
		source[key] = value
	}
	runtime, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			store := &fakeRuntimeStore{binding: testBinding(), volume: volume}
			return StoreHandle{Store: store, Close: store.Close, Ping: func(context.Context) error { return nil }}, nil
		},
		Clock: func() time.Time { return time.Unix(100, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtime
}

func serveRuntimeVolumeHealth(runtime *Runtime) *httptest.ResponseRecorder {
	req := internalGET("/internal/v1/volumes/vol_main/health", "svc_api", "token-api")
	req.Header.Del(auth.HeaderNamespaceID)
	rec := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, req)
	return rec
}

func newTestRuntimeWithStorePing(t *testing.T, ping func(context.Context) error) *Runtime {
	t.Helper()
	return newTestRuntimeWithSourceOverrides(t, nil, ping)
}

func newTestRuntimeWithSourceOverrides(t *testing.T, overrides config.MapSource, ping func(context.Context) error) *Runtime {
	t.Helper()
	source := readyTestRuntimeSource()
	for key, value := range overrides {
		source[key] = value
	}
	return newTestRuntimeWithSource(t, source, ping)
}

func baseTestRuntimeSource() config.MapSource {
	return config.MapSource{
		"AFSCP_API_MODE":                                 "internal",
		"AFSCP_API_POSTGRES_DSN":                         "postgres://api:secret@db/afscp",
		"AFSCP_API_SERVICE_TOKENS":                       "svc_api=token-api,svc_admin=token-admin",
		"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS":    "svc_admin:admin:volume_admin|operator_admin",
		"AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS": "svc_api:product:namespace_admin",
		"AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL":        "https://files.example.test",
		"AFSCP_JVS_ENABLED":                              "true",
		"AFSCP_JVS_READY":                                "true",
		"AFSCP_JVS_BINARY_PATH":                          "/opt/afscp/bin/jvs",
		"AFSCP_JVS_BINARY_SHA256":                        config.JVSAcceptedLinuxAMD64SHA256,
		"AFSCP_JVS_CWD":                                  "/var/lib/afscp/jvs-cwd",
		"AFSCP_API_VOLUME_ROOTS":                         "vol_main=/srv/afscp/volumes/vol_main",
		"AFSCP_WEBDAV_ENABLED":                           "true",
		"AFSCP_WEBDAV_READY":                             "true",
		"AFSCP_MOUNT_ENABLED":                            "true",
		"AFSCP_MOUNT_READY":                              "true",
		"AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS":           "vol_main=runtime-secret-namespace/runtime-secret-volume",
	}
}

func readyTestRuntimeSource() config.MapSource {
	source := baseTestRuntimeSource()
	source["AFSCP_STORAGE_ENABLED"] = "true"
	source["AFSCP_STORAGE_READY"] = "true"
	return source
}

func newTestRuntimeWithSource(t *testing.T, source config.MapSource, ping func(context.Context) error) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(Options{
		Source: source,
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			now := time.Unix(100, 0).UTC()
			store := &fakeRuntimeStore{binding: testBinding(), volume: activeRuntimeVolume(now)}
			return StoreHandle{Store: store, Close: store.Close, Ping: ping}, nil
		},
		SavePointHistoryRunnerFactory: testSavePointHistoryRunnerFactory(),
		OperationID:                   func() string { return "op_test" },
		Clock:                         func() time.Time { return time.Unix(100, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtime
}

func closeRuntime(t *testing.T, runtime *Runtime) {
	t.Helper()
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func internalGET(path, callerService, token string) *http.Request {
	return internalRequest(http.MethodGet, path, callerService, token)
}

func internalPOST(path, callerService, token string, body string) *http.Request {
	req := internalRequest(http.MethodPost, path, callerService, token)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = int64(len(body))
	return req
}

func internalRequest(method, path, callerService, token string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer "+token)
	req.Header.Set(auth.HeaderCorrelationID, "corr_test")
	req.Header.Set(auth.HeaderCallerService, callerService)
	req.Header.Set(auth.HeaderNamespaceID, "ns_alpha")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_test")
	req.Header.Set(auth.HeaderActorType, "service")
	req.Header.Set(auth.HeaderActorID, callerService)
	return req
}

func decodeRuntimeErrorEnvelope(t *testing.T, body []byte) api.ErrorEnvelope {
	t.Helper()
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("error envelope did not decode: %v: %s", err, string(body))
	}
	return env
}

func testBinding() resources.NamespaceVolumeBinding {
	now := time.Unix(100, 0).UTC()
	return resources.NamespaceVolumeBinding{
		NamespaceID:       "ns_alpha",
		DefaultVolumeID:   "vol_main",
		AllowedCallers:    []resources.AllowedCaller{{CallerService: "svc_api", Roles: []resources.CallerRole{resources.CallerRoleNamespaceAdmin}}},
		QuotaBytesDefault: 1 << 20,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(3600), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func activeRuntimeVolume(now time.Time) resources.Volume {
	return resources.Volume{
		ID:             "vol_main",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		CreatedAt:      now.Add(-time.Hour),
		UpdatedAt:      now,
	}
}

func runtimeVolumeHealthHasFinding(response api.VolumeHealthResponse, code string) bool {
	for _, finding := range response.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

type fakeRuntimeStore struct {
	binding              resources.NamespaceVolumeBinding
	namespace            resources.Namespace
	repo                 resources.Repo
	volume               resources.Volume
	exportCreate         exportaccess.CreateRequest
	exportCreateCalls    int
	repoInNamespaceCalls int
	operationCreateCalls int
	templateCreateCalls  int
	templateCloneCalls   int
	closed               bool
}

func (store *fakeRuntimeStore) Close() error {
	store.closed = true
	return nil
}

func (store *fakeRuntimeStore) GetNamespaceVolumeBinding(_ context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error) {
	if namespaceID != store.binding.NamespaceID {
		return resources.NamespaceVolumeBinding{}, sql.ErrNoRows
	}
	return store.binding, nil
}

func (store *fakeRuntimeStore) GetNamespace(_ context.Context, namespaceID string) (resources.Namespace, error) {
	if store.namespace.ID == namespaceID {
		return store.namespace, nil
	}
	return resources.Namespace{}, sql.ErrNoRows
}

func (store *fakeRuntimeStore) GetRepo(_ context.Context, repoID string) (resources.Repo, error) {
	if store.repo.ID == repoID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (store *fakeRuntimeStore) GetRepoInNamespace(_ context.Context, namespaceID, repoID string) (resources.Repo, error) {
	store.repoInNamespaceCalls++
	if store.repo.NamespaceID == namespaceID && store.repo.ID == repoID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) ListReposByNamespace(context.Context, string) ([]resources.Repo, error) {
	return nil, nil
}

func (store *fakeRuntimeStore) GetVolume(_ context.Context, volumeID string) (resources.Volume, error) {
	if store.volume.ID == volumeID {
		return store.volume, nil
	}
	return resources.Volume{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetWorkloadMountBinding(context.Context, string) (workloadmount.Binding, error) {
	return workloadmount.Binding{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetOrchestratorMountPlan(context.Context, string, string) (workloadmount.Plan, error) {
	return workloadmount.Plan{}, sql.ErrNoRows
}

func (store *fakeRuntimeStore) CreateOrReuseExport(_ context.Context, request exportaccess.CreateRequest) (exportaccess.CreateResult, error) {
	store.exportCreateCalls++
	store.exportCreate = request
	if store.repo.ID == "" {
		return exportaccess.CreateResult{}, errors.New("not implemented")
	}
	return exportaccess.CreateResult{Operation: request.Operation, Session: request.Session}, nil
}

func (*fakeRuntimeStore) GetExportSession(context.Context, string) (exportaccess.Session, error) {
	return exportaccess.Session{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) RevokeExport(context.Context, exportaccess.RevokeRequest) (exportaccess.RevokeResult, error) {
	return exportaccess.RevokeResult{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return nil, nil
}

func (*fakeRuntimeStore) RepoHasNonTerminalJVSMutation(context.Context, string) (bool, error) {
	return false, nil
}

func (*fakeRuntimeStore) GetRepoJVSMutationGateStatus(context.Context, string) (api.RepoJVSMutationGateStatus, error) {
	return api.RepoJVSMutationGateStatus{}, nil
}

func (*fakeRuntimeStore) GetOperation(context.Context, string) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetOperationByIdempotencyScope(context.Context, operations.IdempotencyScope) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, sql.ErrNoRows
}

func (store *fakeRuntimeStore) CreateOrReuseOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.operationCreateCalls++
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) CreateOrReuseRepoCreateOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (store *fakeRuntimeStore) CreateOrReuseTemplateCreateOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.templateCreateCalls++
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (store *fakeRuntimeStore) CreateOrReuseTemplateCloneOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.templateCloneCalls++
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) AppendAuditEvent(context.Context, audit.Event) error {
	return nil
}

type runtimeFakeJVSHistoryRunner struct {
	calls        int
	directTarget jvsrunner.DirectTarget
	summary      jvsrunner.HistorySummary
	err          error
}

func (runner *runtimeFakeJVSHistoryRunner) DirectList(_ context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectListSummary, error) {
	runner.calls++
	runner.directTarget = target
	if runner.err != nil {
		return jvsrunner.DirectListSummary{}, runner.err
	}
	savePoints := make([]jvsrunner.DirectSavePointSummary, 0, len(runner.summary.SavePoints))
	for _, savePoint := range runner.summary.SavePoints {
		savePoints = append(savePoints, jvsrunner.DirectSavePointSummary{SavePointID: savePoint.SavePointID, Message: savePoint.Message, CreatedAt: savePoint.CreatedAt, HistoryHead: savePoint.SavePointID == runner.summary.NewestSavePointID})
	}
	return jvsrunner.DirectListSummary{HistoryHeadID: runner.summary.NewestSavePointID, SavePoints: savePoints}, nil
}

func testSavePointHistoryRunnerFactory() SavePointHistoryRunnerFactory {
	return func(config.WorkerRepoCreateRecoveryConfig) (api.JVSHistoryRunner, error) {
		return &runtimeFakeJVSHistoryRunner{summary: jvsrunner.HistorySummary{Workspace: "main"}}, nil
	}
}
