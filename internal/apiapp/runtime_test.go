package apiapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

func TestNewRuntimeFailsClosedWithoutDSN(t *testing.T) {
	_, err := NewRuntime(Options{
		Source: config.MapSource{
			"AFSCP_API_MODE":                              "internal",
			"AFSCP_API_SERVICE_TOKENS":                    "svc_api=token-api",
			"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS": "svc_api:product:operation_inspector",
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

	exportReq := internalRequest(http.MethodPost, "/internal/v1/repo-templates/tpl_alpha:clone", "svc_api", "token-api")
	rec = httptest.NewRecorder()
	runtime.Handler.ServeHTTP(rec, exportReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unimplemented status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "requested internal API capability is not enabled") {
		t.Fatalf("unimplemented route body = %s, want internal capability denial", body)
	}
	if strings.Contains(body, "neutral shell") {
		t.Fatalf("unimplemented route returned neutral shell text: %s", body)
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
	mount := body.Capabilities[api.CapabilityWorkloadMount]
	if !mount.Enabled || !mount.Ready || mount.Gated || mount.Reason != "" {
		t.Fatalf("mount gate = %#v, want enabled and ready", mount)
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

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	return newTestRuntimeWithStorePing(t, func(context.Context) error {
		return nil
	})
}

func newTestRuntimeWithStorePing(t *testing.T, ping func(context.Context) error) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(Options{
		Source: config.MapSource{
			"AFSCP_API_MODE":                                 "internal",
			"AFSCP_API_POSTGRES_DSN":                         "postgres://api:secret@db/afscp",
			"AFSCP_API_SERVICE_TOKENS":                       "svc_api=token-api",
			"AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS":    "svc_api:product:operation_inspector",
			"AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS": "svc_api:product:namespace_admin",
			"AFSCP_JVS_ENABLED":                              "true",
			"AFSCP_JVS_READY":                                "true",
			"AFSCP_WEBDAV_ENABLED":                           "true",
			"AFSCP_WEBDAV_READY":                             "true",
			"AFSCP_MOUNT_ENABLED":                            "true",
			"AFSCP_MOUNT_READY":                              "true",
		},
		StoreFactory: func(_ context.Context, dsn string) (StoreHandle, error) {
			if dsn != "postgres://api:secret@db/afscp" {
				t.Fatalf("dsn = %q", dsn)
			}
			store := &fakeRuntimeStore{binding: testBinding()}
			return StoreHandle{Store: store, Close: store.Close, Ping: ping}, nil
		},
		OperationID: func() string { return "op_test" },
		Clock:       func() time.Time { return time.Unix(100, 0).UTC() },
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

func testBinding() resources.NamespaceVolumeBinding {
	now := time.Unix(100, 0).UTC()
	return resources.NamespaceVolumeBinding{
		NamespaceID:       "ns_alpha",
		DefaultVolumeID:   "vol_main",
		AllowedCallers:    []resources.AllowedCaller{{CallerService: "svc_api", Roles: []resources.CallerRole{resources.CallerRoleNamespaceAdmin}}},
		QuotaBytesDefault: 1 << 20,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(3600), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

type fakeRuntimeStore struct {
	binding resources.NamespaceVolumeBinding
	closed  bool
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

func (*fakeRuntimeStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return resources.Namespace{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetRepo(context.Context, string) (resources.Repo, error) {
	return resources.Repo{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetRepoInNamespace(context.Context, string, string) (resources.Repo, error) {
	return resources.Repo{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) ListReposByNamespace(context.Context, string) ([]resources.Repo, error) {
	return nil, nil
}

func (*fakeRuntimeStore) GetVolume(context.Context, string) (resources.Volume, error) {
	return resources.Volume{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetWorkloadMountBinding(context.Context, string) (workloadmount.Binding, error) {
	return workloadmount.Binding{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetOrchestratorMountPlan(context.Context, string, string) (workloadmount.Plan, error) {
	return workloadmount.Plan{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) CreateOrReuseExport(context.Context, exportaccess.CreateRequest) (exportaccess.CreateResult, error) {
	return exportaccess.CreateResult{}, errors.New("not implemented")
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

func (*fakeRuntimeStore) GetOperation(context.Context, string) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetOperationByIdempotencyScope(context.Context, operations.IdempotencyScope) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) CreateOrReuseOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) CreateOrReuseRepoCreateOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) CreateOrReuseRestorePreviewOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) CreateOrReuseRestorePreviewDiscardOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) CreateOrReuseRestoreRunOperation(context.Context, operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	return operations.IdempotencyResolution{}, errors.New("not implemented")
}

func (*fakeRuntimeStore) GetActiveRestorePlanByRepo(context.Context, string) (restoreplan.Plan, error) {
	return restoreplan.Plan{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) GetRestorePlanByPreviewOperation(context.Context, string) (restoreplan.Plan, error) {
	return restoreplan.Plan{}, sql.ErrNoRows
}

func (*fakeRuntimeStore) RestoreRunExistsForPreviewOperation(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (*fakeRuntimeStore) AppendAuditEvent(context.Context, audit.Event) error {
	return nil
}
