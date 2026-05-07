package api

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

var _ RepoFenceReader = (store.RepoFenceReader)(nil)

func TestInternalAPIShellServesNamespaceVolumeBindingThroughInjectedHandler(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	reader := &fakeNamespaceVolumeBindingReader{binding: resources.NamespaceVolumeBinding{
		NamespaceID:     "ns_123",
		DefaultVolumeID: "vol_123",
		AllowedCallers: []resources.AllowedCaller{{
			CallerService: "product-caller",
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
	if bound.CallerService != "product-caller" {
		t.Fatalf("bound CallerService = %q, want canonical product-caller", bound.CallerService)
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
		CallerService: "product-caller",
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
		{name: "volume health", method: http.MethodGet, path: "/internal/v1/volumes/vol_123/health"},
		{name: "create export", method: http.MethodPost, path: "/internal/v1/repos/repo_123/exports"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleNamespaceAdmin}})}
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

func TestInternalAPIShellCreateExportUsesConfiguredWebDAVPublicBaseURL(t *testing.T) {
	meta := exportMetaFixture()
	store := &fakeExportStore{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:         namespaceBindingPrincipalResolver(),
		NamespaceBindingReader:    meta.bindingReader,
		NamespaceReader:           meta.namespaceReader,
		RepoReader:                meta.repoReader,
		VolumeReader:              meta.volumeReader,
		RepoFenceReader:           &fakeRepoFenceReader{fences: meta.fences},
		ExportStore:               store,
		GenerateOperationID:       func() string { return "op_export_shell" },
		Now:                       fixedNamespaceNow,
		WebDAVExportPublicBaseURL: "https://files.example.test/public",
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
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
}

func TestInternalAPIShellCreateExportCapabilityDeniedWhenWebDAVAdmissionDisabled(t *testing.T) {
	meta := exportMetaFixture()
	store := &fakeExportStore{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:             namespaceBindingPrincipalResolver(),
		NamespaceBindingReader:        meta.bindingReader,
		NamespaceReader:               meta.namespaceReader,
		RepoReader:                    meta.repoReader,
		VolumeReader:                  meta.volumeReader,
		RepoFenceReader:               &fakeRepoFenceReader{fences: meta.fences},
		ExportStore:                   store,
		GenerateOperationID:           func() string { return "op_export_shell" },
		Now:                           fixedNamespaceNow,
		WebDAVExportAdmissionDisabled: true,
		WebDAVExportPublicBaseURL:     "https://files.example.test",
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, exportRequest(http.MethodPost, "/internal/v1/repos/repo_123/exports", `{"mode":"read_only","ttl_seconds":120}`, "ns_123"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeCapabilityDenied {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeCapabilityDenied)
	}
	if store.createCalls != 0 {
		t.Fatalf("create calls = %d, want denied before durable export create", store.createCalls)
	}
}

func TestInternalAPIShellGetAndRevokeExportRemainAvailableWhenWebDAVAdmissionDisabled(t *testing.T) {
	meta := exportMetaFixture()
	store := &fakeExportStore{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:             namespaceBindingPrincipalResolver(),
		NamespaceBindingReader:        meta.bindingReader,
		NamespaceReader:               meta.namespaceReader,
		RepoReader:                    meta.repoReader,
		VolumeReader:                  meta.volumeReader,
		RepoFenceReader:               &fakeRepoFenceReader{fences: meta.fences},
		ExportStore:                   store,
		GenerateOperationID:           func() string { return "op_export_shell" },
		Now:                           fixedNamespaceNow,
		WebDAVExportAdmissionDisabled: true,
		WebDAVExportPublicBaseURL:     "https://files.example.test",
	})

	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, exportRequest(http.MethodGet, "/internal/v1/exports/export_123", "", "ns_123"))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s, want 200", getRec.Code, getRec.Body.String())
	}
	if store.getCalls != 1 {
		t.Fatalf("get calls = %d, want 1", store.getCalls)
	}

	revokeRec := httptest.NewRecorder()
	handler.ServeHTTP(revokeRec, exportRequest(http.MethodDelete, "/internal/v1/exports/export_123", "", "ns_123"))
	if revokeRec.Code != http.StatusAccepted {
		t.Fatalf("revoke status = %d body = %s, want 202", revokeRec.Code, revokeRec.Body.String())
	}
	if store.revokeCalls != 1 {
		t.Fatalf("revoke calls = %d, want 1", store.revokeCalls)
	}
}

func TestInternalAPIShellServesRepoReadRoutesThroughRepoReader(t *testing.T) {
	repoReader := &fakeRepoReader{repos: []resources.Repo{repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)}}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "product-caller",
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

func TestInternalAPIShellServesOperationInspectionThroughInjectedReader(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_secret": operationInspectionRecord("op_secret", "ns_123"),
	}}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "product-caller",
		Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
	})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:         namespaceBindingPrincipalResolver(),
		NamespaceBindingReader:    bindingReader,
		OperationInspectionReader: fakeOperationInspectionStoreReader{reader: reader},
	})
	rec := httptest.NewRecorder()
	req := operationInspectionRequest("op_secret", "", "product-caller")
	req.URL.RawQuery = "token=query-secret"
	req.Header.Set(auth.HeaderAuthorization, "Bearer auth-secret")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if reader.calls != 1 || bindingReader.calls != 1 {
		t.Fatalf("reader/binding calls = %d/%d, want operation and stored namespace auth", reader.calls, bindingReader.calls)
	}
	body := rec.Body.String()
	if strings.Contains(body, "query-secret") || strings.Contains(body, "auth-secret") {
		t.Fatalf("operation inspection response leaked request secret: %s", body)
	}
	assertOperationInspectionResponseDoesNotLeak(t, body)
}

func TestInternalAPIShellOperationInspectionAllowsGlobalOperatorBeforeProductFallback(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_global": operationInspectionRecord("op_global", ""),
	}}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		DeploymentGlobalCallers: []auth.AllowedCaller{{
			CallerService: "product-caller",
			Kind:          auth.CallerKindOperator,
			Roles:         []auth.Role{auth.RoleOperatorAdmin},
		}},
		OperationInspectionReader: fakeOperationInspectionStoreReader{reader: reader},
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_global", "", "product-caller"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"namespace_id":null`) {
		t.Fatalf("global operation response = %s, want namespace_id null", rec.Body.String())
	}
}

func TestInternalAPIShellOperationInspectionOperatorAdminIgnoresNamespaceHeaderForOtherScopes(t *testing.T) {
	tests := []struct {
		name        string
		record      operations.OperationRecord
		requestNS   string
		wantNSField string
	}{
		{
			name:        "global operation with namespace header",
			record:      operationInspectionRecord("op_global", ""),
			requestNS:   "ns_123",
			wantNSField: `"namespace_id":null`,
		},
		{
			name:        "other namespace operation with namespace header",
			record:      operationInspectionRecord("op_other_ns", "ns_456"),
			requestNS:   "ns_123",
			wantNSField: `"namespace_id":"ns_456"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
				tt.record.ID: tt.record,
			}}
			handler := NewInternalAPIShell(InternalAPIShellConfig{
				PrincipalResolver: namespaceBindingPrincipalResolver(),
				DeploymentGlobalCallers: []auth.AllowedCaller{{
					CallerService: "product-caller",
					Kind:          auth.CallerKindOperator,
					Roles:         []auth.Role{auth.RoleOperatorAdmin},
				}},
				OperationInspectionReader: fakeOperationInspectionStoreReader{reader: reader},
			})
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, operationInspectionRequest(tt.record.ID, tt.requestNS, "product-caller"))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantNSField) {
				t.Fatalf("operation response = %s, want %s", rec.Body.String(), tt.wantNSField)
			}
		})
	}
}

func TestInternalAPIShellOperationInspectionProductStillRequiresStoredBinding(t *testing.T) {
	reader := &fakeInspectionOperationReader{records: map[string]operations.OperationRecord{
		"op_secret": operationInspectionRecord("op_secret", "ns_123"),
	}}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "product-caller",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
	})}
	sink := &fakeAuditSink{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		AuditSink:                 sink,
		PrincipalResolver:         namespaceBindingPrincipalResolver(),
		NamespaceBindingReader:    bindingReader,
		OperationInspectionReader: fakeOperationInspectionStoreReader{reader: reader},
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operationInspectionRequest("op_secret", "", "product-caller"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	if bindingReader.calls != 1 {
		t.Fatalf("binding reader calls = %d, want stored namespace binding auth", bindingReader.calls)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
}

func TestInternalAPIShellServesCreateRepoThroughOperationIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "product-caller",
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

func TestInternalAPIShellServesRepoLifecycleThroughOperationIntake(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusActive)
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: meta.bindingReader,
		NamespaceReader:        meta.namespaceRead,
		RepoReader:             meta.repoReader,
		RepoFenceReader:        meta.fenceReader,
		OperationIntakeStore:   store,
		GenerateOperationID:    func() string { return "op_lifecycle_shell" },
		Now:                    fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:archive?token=query-secret", "ns_123", `{}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.calls != 1 || store.spec.Scope.OperationType != operations.OperationRepoArchive || store.spec.RepoID != "repo_123" {
		t.Fatalf("store/spec = %d/%#v, want repo archive", store.calls, store.spec)
	}
	if strings.Contains(rec.Body.String(), "query-secret") {
		t.Fatalf("response leaked query secret: %s", rec.Body.String())
	}
}

func TestInternalAPIShellPurgeOverrideUsesDeploymentBreakGlassPolicy(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	retention := fixedNamespaceNow().Add(time.Hour)
	meta.repo.Lifecycle.RetentionExpiresAt = &retention
	meta.repoReader.repos = []resources.Repo{meta.repo}
	meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
	meta.bindingReader.binding = meta.binding
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: meta.bindingReader,
		NamespaceReader:        meta.namespaceRead,
		RepoReader:             meta.repoReader,
		RepoFenceReader:        meta.fenceReader,
		OperationIntakeStore:   store,
		DeploymentGlobalCallers: []auth.AllowedCaller{{
			CallerService: "product-caller",
			Kind:          auth.CallerKindOperator,
			Roles:         []auth.Role{auth.RoleBreakGlassAdmin},
		}},
		GenerateOperationID: func() string { return "op_lifecycle_shell" },
		Now:                 fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw reason secret","product_confirmation_ref":"confirm-secret","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
	if store.calls != 1 || store.spec.Scope.OperationType != operations.OperationRepoPurge {
		t.Fatalf("store/spec = %d/%#v, want purge intake", store.calls, store.spec)
	}
	rendered := renderLifecycleArgs(t, store.spec.InputSummary)
	if !strings.Contains(rendered, `"break_glass_authorized":true`) {
		t.Fatalf("summary = %s, want break_glass_authorized true", rendered)
	}
	for _, forbidden := range []string{"raw reason secret", "confirm-secret", "approval-secret"} {
		if strings.Contains(rendered, forbidden) || strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("purge override leaked %q summary=%s body=%s", forbidden, rendered, rec.Body.String())
		}
	}
	wantHash, err := operations.HashRequest(repoLifecycleCanonicalRequest{RepoID: "repo_123", Body: purgeRepoRequestDTO{
		Reason:                     "raw reason secret",
		ProductConfirmationRef:     "confirm-secret",
		RetentionOverrideRequested: true,
		OperatorApprovalRef:        "approval-secret",
	}})
	if err != nil {
		t.Fatalf("hash canonical purge: %v", err)
	}
	if store.spec.RequestHash != wantHash {
		t.Fatalf("request hash = %q, want canonical path+body hash %q", store.spec.RequestHash, wantHash)
	}
}

func TestInternalAPIShellPurgeOverrideRejectsWithoutDeploymentBreakGlass(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	retention := fixedNamespaceNow().Add(time.Hour)
	meta.repo.Lifecycle.RetentionExpiresAt = &retention
	meta.repoReader.repos = []resources.Repo{meta.repo}
	meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
	meta.bindingReader.binding = meta.binding
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: meta.bindingReader,
		NamespaceReader:        meta.namespaceRead,
		RepoReader:             meta.repoReader,
		RepoFenceReader:        meta.fenceReader,
		OperationIntakeStore:   store,
		GenerateOperationID:    func() string { return "op_lifecycle_shell" },
		Now:                    fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw reason secret","product_confirmation_ref":"confirm-secret","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("intake calls = %d, want none", store.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodePurgeRequiresOperatorApproval {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodePurgeRequiresOperatorApproval)
	}
	if strings.Contains(rec.Body.String(), "raw reason secret") || strings.Contains(rec.Body.String(), "confirm-secret") || strings.Contains(rec.Body.String(), "approval-secret") {
		t.Fatalf("response leaked raw purge detail: %s", rec.Body.String())
	}
}

func TestInternalAPIShellPurgeOverrideBreakGlassPolicyFailureFailsClosed(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	retention := fixedNamespaceNow().Add(time.Hour)
	meta.repo.Lifecycle.RetentionExpiresAt = &retention
	meta.repoReader.repos = []resources.Repo{meta.repo}
	meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
	meta.bindingReader.binding = meta.binding
	sink := &fakeAuditSink{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: meta.bindingReader,
		NamespaceReader:        meta.namespaceRead,
		RepoReader:             meta.repoReader,
		RepoFenceReader:        meta.fenceReader,
		OperationIntakeStore:   store,
		DeploymentGlobalPolicy: fakeAllowedCallerPolicy{err: errors.New("global policy password=secret failed")},
		GenerateOperationID:    func() string { return "op_lifecycle_shell" },
		Now:                    fixedNamespaceNow,
		AuditSink:              sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw reason secret","product_confirmation_ref":"confirm-secret","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("intake calls = %d, want none", store.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeInternalError {
		t.Fatalf("error code = %s, want %s", env.Error.Code, CodeInternalError)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "global policy") || strings.Contains(rec.Body.String(), "password") {
		t.Fatalf("response leaked policy/input detail: %s", rec.Body.String())
	}
}

func TestInternalAPIShellPurgeOverridePreservesClassifiedBreakGlassPolicyError(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	meta := repoLifecycleMetaFixture(resources.RepoStatusTombstoned)
	retention := fixedNamespaceNow().Add(time.Hour)
	meta.repo.Lifecycle.RetentionExpiresAt = &retention
	meta.repoReader.repos = []resources.Repo{meta.repo}
	meta.binding.LifecyclePolicy["break_glass_purge_enabled"] = true
	meta.bindingReader.binding = meta.binding
	sink := &fakeAuditSink{}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: meta.bindingReader,
		NamespaceReader:        meta.namespaceRead,
		RepoReader:             meta.repoReader,
		RepoFenceReader:        meta.fenceReader,
		OperationIntakeStore:   store,
		DeploymentGlobalPolicy: fakeAllowedCallerPolicy{err: NewAllowedCallerPolicyError(CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", "store_unavailable")},
		GenerateOperationID:    func() string { return "op_lifecycle_shell" },
		Now:                    fixedNamespaceNow,
		AuditSink:              sink,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, repoLifecycleRequest("/internal/v1/repos/repo_123:purge", "ns_123", `{"reason":"raw reason secret","product_confirmation_ref":"confirm-secret","retention_override_requested":true,"operator_approval_ref":"approval-secret"}`))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("intake calls = %d, want none", store.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
		t.Fatalf("error = %#v, want STORAGE_UNAVAILABLE retryable", env.Error)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit events = %#v, want denied audit", sink.events)
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "approval") {
		t.Fatalf("response leaked raw purge detail: %s", rec.Body.String())
	}
}

func TestInternalAPIShellCreateRepoFailsClosedWithoutDedicatedIntakeStore(t *testing.T) {
	genericStore := &fakeOperationIntakeStore{}
	bindingReader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{
		CallerService: "product-caller",
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
		CallerService: "product-caller",
		Roles:         []resources.CallerRole{resources.CallerRoleNamespaceAdmin},
	})}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:      namespaceBindingPrincipalResolver(),
		NamespaceBindingReader: reader,
		DeploymentNamespaceCallers: []auth.AllowedCaller{{
			CallerService: "product-caller",
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
			CallerService: "product-caller",
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
	reader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleNamespaceAdmin}})}
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
