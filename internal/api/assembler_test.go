package api

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestInternalAPIShellServesNamespaceVolumeBindingThroughInjectedHandler(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	reader := &fakeNamespaceVolumeBindingReader{binding: resources.NamespaceVolumeBinding{
		NamespaceID:     "ns_123",
		DefaultVolumeID: "vol_123",
		AllowedCallers: []resources.AllowedCaller{{
			CallerService: "agentsmith-api",
			Roles:         []resources.CallerRole{resources.CallerRoleNamespaceAdmin},
		}},
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now.Add(time.Minute),
	}}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: reader,
	})
	req := namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if reader.calls != 2 || reader.namespaceID != "ns_123" || reader.ctx == nil {
		t.Fatalf("reader calls/ns/ctx = %d/%q/%v, want policy and handler reads for ns_123 with request context", reader.calls, reader.namespaceID, reader.ctx != nil)
	}
	bound, ok := RequestContextFromRequest(req.WithContext(reader.ctx))
	if !ok {
		t.Fatal("reader context did not include AuthGate request context")
	}
	if bound.CallerService != "agentsmith-api" {
		t.Fatalf("bound CallerService = %q, want canonical agentsmith-api", bound.CallerService)
	}
	body := rec.Body.String()
	for _, want := range []string{`"namespace_id":"ns_123"`, `"default_volume_id":"vol_123"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response %s missing %s", body, want)
		}
	}
}

func TestInternalAPIShellLogsImplementedNamespaceVolumeBindingRoute(t *testing.T) {
	var logs bytes.Buffer
	reader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "agentsmith-api",
		Roles:         []resources.CallerRole{resources.CallerRoleNamespaceAdmin},
	})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		Logger:                 observability.NewJSONLogger(&logs, nil),
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: reader,
	})
	req := namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding?token=query-secret", "ns_123")
	req.Header.Set(auth.HeaderAuthorization, "Bearer auth-secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	entry := decodeSingleStructuredLogEntry(t, logs.Bytes())
	if got, want := entry["event"], "afscp.request"; got != want {
		t.Fatalf("event = %#v, want %#v in %#v", got, want, entry)
	}
	if got, want := entry["level"], slog.LevelInfo.String(); got != want {
		t.Fatalf("level = %#v, want %#v", got, want)
	}
	if got, want := entry["correlation_id"], "corr_binding"; got != want {
		t.Fatalf("correlation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["method"], http.MethodGet; got != want {
		t.Fatalf("method = %#v, want %#v", got, want)
	}
	if got, want := entry["path"], "/internal/v1/namespaces/ns_123/volume-binding"; got != want {
		t.Fatalf("path = %#v, want %#v", got, want)
	}
	if got, want := entry["route"], "/internal/v1/namespaces/{namespaceId}/volume-binding"; got != want {
		t.Fatalf("route = %#v, want %#v", got, want)
	}
	if got, want := entry["operation_id"], "getNamespaceVolumeBinding"; got != want {
		t.Fatalf("operation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["status"], float64(http.StatusOK); got != want {
		t.Fatalf("status = %#v, want %#v", got, want)
	}
	rendered := logs.String()
	for _, leaked := range []string{"auth-secret", "query-secret"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("implemented route log leaked %q: %s", leaked, rendered)
		}
	}
}

func TestInternalAPIShellKeepsUnimplementedKnownRoutesCapabilityDenied(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "repo archive", method: http.MethodPost, path: "/internal/v1/repos/repo_123:archive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleNamespaceAdmin}})}
			handler := NewInternalAPIShell(InternalAPIShellConfig{
				PrincipalResolver:      namespaceBindingPrincipalResolver(),
				NamespaceBindingReader: reader,
			})
			rec := httptest.NewRecorder()
			req := namespaceBindingRequest(tt.method, tt.path, "ns_123")

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
			}
			if reader.calls != 0 {
				t.Fatalf("reader calls = %d, want 0 for unimplemented route", reader.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != CodeCapabilityDenied {
				t.Fatalf("error code = %s, want CAPABILITY_DENIED", env.Error.Code)
			}
			if strings.Contains(env.Error.Message, "neutral shell") {
				t.Fatalf("partial shell capability denied message mentions neutral shell: %q", env.Error.Message)
			}
		})
	}
}

func TestInternalAPIShellServesRepoReadRoutesThroughRepoReader(t *testing.T) {
	repoReader := &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "agentsmith-api",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
	})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: bindingReader,
		RepoReader:             repoReader,
	})

	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, repoReadRequest(http.MethodGet, "/internal/v1/repos/repo_123?token=query-secret", "ns_123"))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s, want 200", getRec.Code, getRec.Body.String())
	}
	if repoReader.getInNamespaceCalls != 1 || repoReader.getCalls != 0 || repoReader.lastNamespaceID != "ns_123" || repoReader.lastRepoID != "repo_123" {
		t.Fatalf("get scoped/global calls ns/repo = %d/%d %q/%q, want scoped ns_123/repo_123", repoReader.getInNamespaceCalls, repoReader.getCalls, repoReader.lastNamespaceID, repoReader.lastRepoID)
	}

	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, repoReadRequest(http.MethodGet, "/internal/v1/repos?namespace_id=ns_123", "ns_123"))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s, want 200", listRec.Code, listRec.Body.String())
	}
	if repoReader.listCalls != 1 || repoReader.lastNamespaceID != "ns_123" {
		t.Fatalf("list calls/ns = %d/%q, want ns_123", repoReader.listCalls, repoReader.lastNamespaceID)
	}
	for _, body := range []string{getRec.Body.String(), listRec.Body.String()} {
		if strings.Contains(body, "query-secret") {
			t.Fatalf("repo read response leaked query secret: %s", body)
		}
		assertRepoReadResponseDoesNotLeak(t, body)
	}
}

func TestInternalAPIShellServesCreateRepoThroughOperationIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "agentsmith-api",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
	})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: bindingReader,
		RepoCreateIntakeStore:  store,
		OperationIntakeStore:   store,
		GenerateOperationID:    func() string { return "op_repo_shell" },
		Now:                    fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()
	req := createRepoRequest("ns_123", createRepoRequestBody("ns_123", "repo_123"))
	req.URL.RawQuery = "token=query-secret"
	req.Header.Set(auth.HeaderAuthorization, "Bearer auth-secret")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 || store.spec.Scope.OperationType != operations.OperationRepoCreate || store.spec.RepoID != "repo_123" {
		t.Fatalf("store/spec = %d/%#v, want repo create", store.calls, store.spec)
	}
	body := rec.Body.String()
	if strings.Contains(body, "query-secret") || strings.Contains(body, "auth-secret") {
		t.Fatalf("response leaked secret: %s", body)
	}
}

func TestInternalAPIShellCreateRepoFailsClosedWithoutDedicatedIntakeStore(t *testing.T) {
	genericStore := &fakeOperationIntakeStore{}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "agentsmith-api",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
	})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: bindingReader,
		OperationIntakeStore:   genericStore,
		GenerateOperationID:    func() string { return "op_repo_shell" },
		Now:                    fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, createRepoRequest("ns_123", createRepoRequestBody("ns_123", "repo_123")))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500 fail closed", rec.Code, rec.Body.String())
	}
	if genericStore.calls != 0 {
		t.Fatalf("generic intake calls = %d, want 0", genericStore.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeInternalError {
		t.Fatalf("error code = %s, want INTERNAL_ERROR", env.Error.Code)
	}
}

func TestInternalAPIShellServesNamespaceVolumeBindingPutThroughOperationIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	reader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "agentsmith-api",
		Roles:         []resources.CallerRole{resources.CallerRoleNamespaceAdmin},
	})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: reader,
		DeploymentNamespaceCallers: []auth.AllowedCaller{{
			CallerService: "agentsmith-api",
			Kind:          auth.CallerKindProduct,
			Roles:         []auth.Role{auth.RoleNamespaceAdmin},
		}},
		OperationIntakeStore: store,
		GenerateOperationID:  func() string { return "op_binding_shell" },
		Now:                  fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()
	req := namespaceBindingRequestWithBody(http.MethodPut, "/internal/v1/namespaces/ns_123/volume-binding?token=query-secret", "ns_123", namespaceBindingRequestBody("ns_123"))
	req.Header.Set(auth.HeaderAuthorization, "Bearer auth-secret")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if reader.calls != 0 {
		t.Fatalf("binding reader calls = %d, want 0 for PUT", reader.calls)
	}
	if store.calls != 1 || store.spec.Scope.OperationType != operations.OperationNamespaceVolumeBindingPut {
		t.Fatalf("intake calls/spec = %d/%#v, want binding put", store.calls, store.spec)
	}
	body := rec.Body.String()
	if strings.Contains(body, "query-secret") || strings.Contains(body, "auth-secret") {
		t.Fatalf("response leaked secret: %s", body)
	}
}

func TestInternalAPIShellServesEnsureVolumeThroughOperationIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		DeploymentGlobalCallers: []auth.AllowedCaller{{
			CallerService: "agentsmith-api",
			Kind:          auth.CallerKindAdmin,
			Roles:         []auth.Role{auth.RoleVolumeAdmin},
		}},
		OperationIntakeStore: store,
		GenerateOperationID:  func() string { return "op_volume_shell" },
		Now:                  fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()
	req := ensureVolumeRequest("/internal/v1/volumes/vol_123:ensure?token=query-secret", ensureVolumeRequestBody("vol_123"))
	req.Header.Set(auth.HeaderAuthorization, "Bearer auth-secret")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 || store.spec.Scope.OperationType != operations.OperationVolumeEnsure || store.spec.NamespaceID != "" {
		t.Fatalf("intake calls/spec = %d/%#v, want volume ensure", store.calls, store.spec)
	}
	body := rec.Body.String()
	if strings.Contains(body, "query-secret") || strings.Contains(body, "auth-secret") {
		t.Fatalf("response leaked secret: %s", body)
	}
}

func TestInternalAPIShellUnknownRoutePathDenied(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleNamespaceAdmin}})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: reader,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/not-volume-binding", "ns_123"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d, want 0 for unknown route", reader.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodePathDenied {
		t.Fatalf("error code = %s, want PATH_DENIED", env.Error.Code)
	}
}

func TestInternalAPIShellPropagatesBindingPolicyStorageUnavailable(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{err: errors.Join(sql.ErrConnDone, errors.New("postgres dsn password=secret"))}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: reader,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
		t.Fatalf("error = %#v, want STORAGE_UNAVAILABLE retryable", env.Error)
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "postgres") {
		t.Fatalf("response leaked raw store error: %s", rec.Body.String())
	}
}

func TestInternalAPIShellHealthAndReadyMatchNeutralShell(t *testing.T) {
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: &fakeNamespaceVolumeBindingReader{},
	})

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", ready.Code)
	}
}
