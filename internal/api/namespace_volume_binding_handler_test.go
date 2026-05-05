package api

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

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestNamespaceVolumeBindingHandlerCreatesOperationIntake(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := &fakeOperationIntakeStore{}
	handler := namespaceBindingPutHandlerForTest(store, func() string { return "op_binding" }, func() time.Time { return now }, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequestWithBody(http.MethodPut, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123", namespaceBindingRequestBody("ns_123")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("intake calls = %d, want 1", store.calls)
	}
	spec := store.spec
	wantScope := operations.NewIdempotencyScope("agentsmith-api", "ns_123", operations.OperationNamespaceVolumeBindingPut, "idem_binding")
	if spec.OperationID != "op_binding" || spec.Scope != wantScope {
		t.Fatalf("spec op/scope = %q/%#v, want op_binding/%#v", spec.OperationID, spec.Scope, wantScope)
	}
	if spec.Scope.OperationType != operations.OperationNamespaceVolumeBindingPut {
		t.Fatalf("spec type = %q, want namespace_volume_binding_put", spec.Scope.OperationType)
	}
	if spec.Phase != operations.OperationPhaseNamespaceVolumeBindingPutValidate {
		t.Fatalf("phase = %q, want %s", spec.Phase, operations.OperationPhaseNamespaceVolumeBindingPutValidate)
	}
	if spec.NamespaceID != "ns_123" || spec.Resource.Type != "namespace_volume_binding" || spec.Resource.ID != "ns_123" {
		t.Fatalf("namespace/resource = %q/%#v", spec.NamespaceID, spec.Resource)
	}
	if spec.CorrelationID != "corr_binding" || spec.CallerService != "agentsmith-api" {
		t.Fatalf("correlation/caller = %q/%q", spec.CorrelationID, spec.CallerService)
	}
	if spec.AuthorizedActor.Type != "system" || spec.AuthorizedActor.ID != "svc-binding" {
		t.Fatalf("actor = %#v", spec.AuthorizedActor)
	}
	if spec.InputSummary["namespace_id"] != "ns_123" || spec.InputSummary["default_volume_id"] != "vol_123" {
		t.Fatalf("input summary = %#v, want reconstructable binding fields", spec.InputSummary)
	}
	if _, ok := spec.InputSummary["allowed_callers"]; !ok {
		t.Fatalf("input summary missing allowed_callers: %#v", spec.InputSummary)
	}
	if len(spec.ExternalResourceIDs) != 0 {
		t.Fatalf("external ids = %#v, want none", spec.ExternalResourceIDs)
	}

	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_binding" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope id/state = %q/%q, want op_binding/queued", env.OperationID, env.OperationState)
	}
	if env.Resource.Type != "namespace_volume_binding" || env.Resource.ID != "ns_123" {
		t.Fatalf("envelope resource = %#v", env.Resource)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, env)
}

func TestNamespaceVolumeBindingHandlerReusesExistingOperation(t *testing.T) {
	store := &fakeOperationIntakeStore{existingOperationID: "op_existing_binding", reused: true}
	handler := namespaceBindingPutHandlerForTest(store, func() string { return "op_new_binding" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequestWithBody(http.MethodPut, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123", namespaceBindingRequestBody("ns_123")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	env := decodeOperationEnvelope(t, rec.Body.Bytes())
	if env.OperationID != "op_existing_binding" || env.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want reused existing queued operation", env)
	}
}

func TestNamespaceVolumeBindingHandlerPutMapsIntakeErrorsWithoutLeakingStoreDetail(t *testing.T) {
	tests := []struct {
		name     string
		store    *fakeOperationIntakeStore
		generate OperationIDGenerator
		wantCode ErrorCode
		status   int
		retry    bool
	}{
		{name: "conflict", store: &fakeOperationIntakeStore{err: operations.ErrIdempotencyConflict}, generate: func() string { return "op_binding" }, wantCode: CodeIdempotencyConflict, status: http.StatusConflict},
		{name: "store outage", store: &fakeOperationIntakeStore{err: errors.New("postgres dsn password=secret failed")}, generate: func() string { return "op_binding" }, wantCode: CodeStorageUnavailable, status: http.StatusServiceUnavailable, retry: true},
		{name: "nil store", generate: func() string { return "op_binding" }, wantCode: CodeInternalError, status: http.StatusInternalServerError},
		{name: "empty operation id", store: &fakeOperationIntakeStore{}, generate: func() string { return "" }, wantCode: CodeInternalError, status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := namespaceBindingPutHandlerForTest(tt.store, tt.generate, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, namespaceBindingRequestWithBody(http.MethodPut, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123", namespaceBindingRequestBody("ns_123")))

			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable != tt.retry {
				t.Fatalf("error = %#v, want code/retry %s/%v", env.Error, tt.wantCode, tt.retry)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "postgres") {
				t.Fatalf("error leaked raw detail: %s", rec.Body.String())
			}
		})
	}
}

func TestNamespaceVolumeBindingHandlerPutValidationDeniesBeforeIntakeAndAudits(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		headerNS   string
		body       string
		wantCode   ErrorCode
		wantStatus int
	}{
		{name: "invalid path namespace", path: "/internal/v1/namespaces/bad_ns/volume-binding", headerNS: "bad_ns", body: namespaceBindingRequestBody("bad_ns"), wantCode: CodeInvalidID, wantStatus: http.StatusBadRequest},
		{name: "missing namespace header", path: "/internal/v1/namespaces/ns_123/volume-binding", body: namespaceBindingRequestBody("ns_123"), wantCode: CodeResourceNamespaceMismatch, wantStatus: http.StatusBadRequest},
		{name: "mismatched namespace header", path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_456", body: namespaceBindingRequestBody("ns_123"), wantCode: CodeResourceNamespaceMismatch, wantStatus: http.StatusBadRequest},
		{name: "mismatched body namespace", path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_123", body: namespaceBindingRequestBody("ns_456"), wantCode: CodeResourceNamespaceMismatch, wantStatus: http.StatusBadRequest},
		{name: "unknown field", path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_123", body: strings.Replace(namespaceBindingRequestBody("ns_123"), `"status":"active"`, `"status":"active","extra":true`, 1), wantCode: CodeInvalidID, wantStatus: http.StatusBadRequest},
		{name: "malformed json", path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_123", body: `{"namespace_id":`, wantCode: CodeInvalidID, wantStatus: http.StatusBadRequest},
		{name: "invalid policy", path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_123", body: strings.Replace(namespaceBindingRequestBody("ns_123"), `"max_session_seconds":3600`, `"max_session_seconds":30`, 1), wantCode: CodeInvalidID, wantStatus: http.StatusBadRequest},
		{name: "secret-like policy", path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_123", body: strings.Replace(namespaceBindingRequestBody("ns_123"), `"max_session_seconds":3600`, `"max_session_seconds":3600,"credential_ref":"secret-ref"`, 1), wantCode: CodeInvalidID, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeOperationIntakeStore{}
			sink := &fakeAuditSink{}
			handler := namespaceBindingPutHandlerForTestWithAudit(store, func() string { return "op_binding" }, fixedNamespaceNow, namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin), sink)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, namespaceBindingRequestWithBody(http.MethodPut, tt.path, tt.headerNS, tt.body))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}
			if store.calls != 0 {
				t.Fatalf("intake calls = %d, want 0", store.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable {
				t.Fatalf("error = %#v, want code %s retryable false", env.Error, tt.wantCode)
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit events = %#v, want validation denial", sink.events)
			}
			for _, leaked := range []string{"secret-ref", "credential_ref"} {
				if strings.Contains(rec.Body.String(), leaked) {
					t.Fatalf("validation response leaked %q: %s", leaked, rec.Body.String())
				}
			}
		})
	}
}

func TestNamespaceVolumeBindingHandlerPutUsesDeploymentPolicyNotBindingPolicy(t *testing.T) {
	store := &fakeOperationIntakeStore{}
	bindingReader := &fakeNamespaceVolumeBindingReader{err: sql.ErrNoRows}
	handler := NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
		Reader:            bindingReader,
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers: RouteAwareAllowedCallerPolicy{
			DeploymentNamespace: namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
			NamespaceBinding:    NamespaceVolumeBindingAllowedCallerPolicy{Reader: bindingReader},
		},
		OperationID: func() string { return "op_binding" },
		Now:         fixedNamespaceNow,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequestWithBody(http.MethodPut, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123", namespaceBindingRequestBody("ns_123")))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 through deployment policy", rec.Code, rec.Body.String())
	}
	if bindingReader.calls != 0 {
		t.Fatalf("binding reader calls = %d, want 0 for PUT authz/intake", bindingReader.calls)
	}
}

func TestNamespaceVolumeBindingHandlerReturnsBindingDTO(t *testing.T) {
	now := namespaceBindingHandlerTestNow()
	store := &fakeNamespaceVolumeBindingReader{binding: resources.NamespaceVolumeBinding{
		NamespaceID:     "ns_123",
		DefaultVolumeID: "vol_123",
		AllowedCallers: []resources.AllowedCaller{{
			CallerService: "agentsmith-api",
			Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleOperationInspector},
		}},
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600), "credential_ref": "secret-ref"},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now.Add(time.Minute),
	}}
	handler := namespaceBindingHandler(store)
	req := namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if store.calls != 1 || store.namespaceID != "ns_123" || store.ctx == nil {
		t.Fatalf("store calls/id/ctx = %d/%q/%v, want one call for ns_123 with request context", store.calls, store.namespaceID, store.ctx != nil)
	}
	bound, ok := RequestContextFromRequest(req.WithContext(store.ctx))
	if !ok {
		t.Fatal("store context did not include AuthGate request context")
	}
	if bound.CallerService != "agentsmith-api" {
		t.Fatalf("bound CallerService = %q, want canonical agentsmith-api", bound.CallerService)
	}
	var got map[string]any
	mustUnmarshalJSON(t, rec.Body.Bytes(), &got)
	assertOnlyJSONKeys(t, got,
		"namespace_id",
		"default_volume_id",
		"allowed_callers",
		"quota_bytes_default",
		"export_policy",
		"lifecycle_policy",
		"mount_policy",
		"template_policy",
		"status",
	)
	if got["namespace_id"] != "ns_123" || got["default_volume_id"] != "vol_123" || got["status"] != "active" {
		t.Fatalf("response = %#v, want binding DTO values", got)
	}
	callers := got["allowed_callers"].([]any)
	roles := callers[0].(map[string]any)["roles"].([]any)
	if roles[0] != "repo_admin" || roles[1] != "operation_inspector" {
		t.Fatalf("allowed caller roles = %#v, want serialized roles", roles)
	}
	body := rec.Body.String()
	for _, leaked := range []string{"created_at", "updated_at", "CreatedAt", "UpdatedAt", "credential_ref", "secret-ref"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("response leaked %q: %s", leaked, body)
		}
	}
}

func TestNamespaceVolumeBindingHandlerRejectsValidationFailuresBeforeStore(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		headerNS string
		wantCode ErrorCode
		status   int
	}{
		{name: "wrong method", method: http.MethodPost, path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_123", wantCode: CodePathDenied, status: http.StatusNotFound},
		{name: "wrong path", method: http.MethodGet, path: "/internal/v1/namespaces/ns_123/not-volume-binding", headerNS: "ns_123", wantCode: CodePathDenied, status: http.StatusNotFound},
		{name: "invalid path namespace", method: http.MethodGet, path: "/internal/v1/namespaces/bad_ns/volume-binding", headerNS: "bad_ns", wantCode: CodeInvalidID, status: http.StatusBadRequest},
		{name: "missing namespace header", method: http.MethodGet, path: "/internal/v1/namespaces/ns_123/volume-binding", wantCode: CodeResourceNamespaceMismatch, status: http.StatusBadRequest},
		{name: "mismatched namespace header", method: http.MethodGet, path: "/internal/v1/namespaces/ns_123/volume-binding", headerNS: "ns_456", wantCode: CodeResourceNamespaceMismatch, status: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeNamespaceVolumeBindingReader{}
			rec := httptest.NewRecorder()

			namespaceBindingHandler(store).ServeHTTP(rec, namespaceBindingRequest(tt.method, tt.path, tt.headerNS))

			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want 0", store.calls)
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.wantCode || env.Error.Retryable {
				t.Fatalf("error = %#v, want code %s retryable false", env.Error, tt.wantCode)
			}
		})
	}
}

func TestNamespaceVolumeBindingHandlerAuthGateDeniesBeforeStore(t *testing.T) {
	tests := []struct {
		name   string
		edit   func(*http.Request)
		policy AllowedCallerPolicy
		want   ErrorCode
		status int
	}{
		{
			name:   "missing auth",
			edit:   func(req *http.Request) { req.Header.Del(auth.HeaderAuthorization) },
			policy: namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
			want:   CodeAuthenticationFailed,
			status: http.StatusUnauthorized,
		},
		{
			name:   "missing policy",
			policy: nil,
			want:   CodeCallerNotAllowed,
			status: http.StatusForbidden,
		},
		{
			name:   "role insufficient",
			policy: namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
			want:   CodeRoleNotAllowed,
			status: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeNamespaceVolumeBindingReader{}
			handler := NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
				Reader:            store,
				PrincipalResolver: namespaceBindingPrincipalResolver(),
				AllowedCallers:    tt.policy,
			})
			req := namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123")
			if tt.edit != nil {
				tt.edit(req)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want 0", store.calls)
			}
			if strings.Contains(rec.Body.String(), "default_volume_id") {
				t.Fatalf("auth denial returned binding data: %s", rec.Body.String())
			}
			env := decodeErrorEnvelope(t, rec.Body.Bytes())
			if env.Error.Code != tt.want {
				t.Fatalf("error code = %s, want %s", env.Error.Code, tt.want)
			}
		})
	}
}

func TestNamespaceVolumeBindingHandlerDeniesOtherKnownInternalRouteWithoutRoleDecision(t *testing.T) {
	store := &fakeNamespaceVolumeBindingReader{}
	handler := NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
		Reader:            store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleRepoAdmin),
	})
	req := namespaceBindingRequest(http.MethodGet, "/internal/v1/repos/repo_123", "ns_123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodePathDenied {
		t.Fatalf("error code = %s, want PATH_DENIED", env.Error.Code)
	}
	if strings.Contains(rec.Body.String(), string(CodeRoleNotAllowed)) || strings.Contains(rec.Body.String(), string(CodeCallerNotAllowed)) {
		t.Fatalf("other route was treated as authz/role decision: %s", rec.Body.String())
	}
}

func TestNamespaceVolumeBindingHandlerAuditsLeafNamespaceMismatch(t *testing.T) {
	store := &fakeNamespaceVolumeBindingReader{}
	sink := &fakeAuditSink{}
	handler := namespaceBindingHandlerWithAudit(store, sink)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_456"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events = %#v, want one validation denial", sink.events)
	}
	if got := sink.events[0].Details["error_code"]; got != string(CodeResourceNamespaceMismatch) {
		t.Fatalf("audit error_code = %#v, want %s", got, CodeResourceNamespaceMismatch)
	}
}

func TestNamespaceVolumeBindingHandlerMapsNotFound(t *testing.T) {
	store := &fakeNamespaceVolumeBindingReader{err: sql.ErrNoRows}
	rec := httptest.NewRecorder()

	namespaceBindingHandler(store).ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeNamespaceNotFound || env.Error.Retryable || strings.Contains(rec.Body.String(), "sql:") {
		t.Fatalf("error/body = %#v/%s, want stable not found without raw sql error", env.Error, rec.Body.String())
	}
}

func TestNamespaceVolumeBindingHandlerRejectsStoreNamespaceMismatchWithoutLeakingBinding(t *testing.T) {
	store := &fakeNamespaceVolumeBindingReader{binding: resources.NamespaceVolumeBinding{
		NamespaceID:     "ns_other",
		DefaultVolumeID: "vol_secret",
		AllowedCallers: []resources.AllowedCaller{{
			CallerService: "other-service",
			Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
		}},
		Status: resources.NamespaceStatusActive,
	}}
	rec := httptest.NewRecorder()

	namespaceBindingHandler(store).ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeInternalError || env.Error.Retryable {
		t.Fatalf("error = %#v, want INTERNAL_ERROR retryable false", env.Error)
	}
	for _, leaked := range []string{"ns_other", "vol_secret", "other-service", "repo_admin"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("namespace mismatch leaked %q: %s", leaked, rec.Body.String())
		}
	}
}

func TestNamespaceVolumeBindingHandlerMapsStoreOutageWithoutLeakingRawError(t *testing.T) {
	store := &fakeNamespaceVolumeBindingReader{err: errors.New("postgres dsn password=secret-password connect failed")}
	rec := httptest.NewRecorder()

	namespaceBindingHandler(store).ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
		t.Fatalf("error = %#v, want STORAGE_UNAVAILABLE retryable", env.Error)
	}
	for _, leaked := range []string{"postgres", "dsn", "secret-password", "connect failed"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("store outage leaked %q: %s", leaked, rec.Body.String())
		}
	}
}

func TestNamespaceVolumeBindingHandlerNilStoreIsInternalError(t *testing.T) {
	rec := httptest.NewRecorder()

	namespaceBindingHandler(nil).ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeInternalError || env.Error.Retryable {
		t.Fatalf("error = %#v, want INTERNAL_ERROR retryable false", env.Error)
	}
}

type fakeNamespaceVolumeBindingReader struct {
	calls       int
	ctx         context.Context
	namespaceID string
	binding     resources.NamespaceVolumeBinding
	err         error
}

func (reader *fakeNamespaceVolumeBindingReader) GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error) {
	reader.calls++
	reader.ctx = ctx
	reader.namespaceID = namespaceID
	if reader.err != nil {
		return resources.NamespaceVolumeBinding{}, reader.err
	}
	return reader.binding, nil
}

func namespaceBindingRequest(method, path, namespaceID string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_binding")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func namespaceBindingRequestWithBody(method, path, namespaceID string, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_binding")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_binding")
	req.Header.Set(auth.HeaderActorType, "system")
	req.Header.Set(auth.HeaderActorID, "svc-binding")
	if namespaceID != "" {
		req.Header.Set(auth.HeaderNamespaceID, namespaceID)
	}
	return req
}

func namespaceBindingHandler(reader NamespaceVolumeBindingReader) http.Handler {
	return NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
		Reader:            reader,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
	})
}

func namespaceBindingPutHandlerForTest(store OperationIntakeStore, generate OperationIDGenerator, now func() time.Time, policy AllowedCallerPolicy) http.Handler {
	return namespaceBindingPutHandlerForTestWithAudit(store, generate, now, policy, nil)
}

func namespaceBindingPutHandlerForTestWithAudit(store OperationIntakeStore, generate OperationIDGenerator, now func() time.Time, policy AllowedCallerPolicy, sink auditSinkForBindingTest) http.Handler {
	return NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
		Reader:            &fakeNamespaceVolumeBindingReader{},
		IntakeStore:       store,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    policy,
		OperationID:       generate,
		Now:               now,
		AuditSink:         sink,
	})
}

type auditSinkForBindingTest interface {
	Emit(context.Context, audit.Event) error
}

func namespaceBindingHandlerWithAudit(reader NamespaceVolumeBindingReader, sink *fakeAuditSink) http.Handler {
	return NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
		Reader:            reader,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
		AuditSink:         sink,
	})
}

func namespaceBindingPrincipalResolver() PrincipalResolver {
	return fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:agentsmith-api", CanonicalCallerService: "agentsmith-api"}}
}

func namespaceBindingAllowedPolicy(roles ...auth.Role) AllowedCallerPolicy {
	return fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{{
		CallerService: "agentsmith-api",
		Kind:          auth.CallerKindProduct,
		Roles:         roles,
	}}}
}

func decodeErrorEnvelope(t *testing.T, body []byte) ErrorEnvelope {
	t.Helper()
	var env ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope %s: %v", string(body), err)
	}
	return env
}

func namespaceBindingHandlerTestNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func namespaceBindingRequestBody(namespaceID string) string {
	return `{"namespace_id":"` + namespaceID + `","default_volume_id":"vol_123","allowed_callers":[{"caller_service":"agentsmith-api","roles":["repo_admin","operation_inspector"]}],"quota_bytes_default":4096,"export_policy":{"webdav_enabled":true,"max_session_seconds":3600},"lifecycle_policy":{"tombstone_retention_seconds":604800,"purge_requires_lifecycle_admin":true,"break_glass_purge_enabled":false},"mount_policy":{"workload_mount_enabled":true,"workload_mount_requires_jvs_external_control_root":true,"allow_privileged_workload":false},"template_policy":{"namespace_templates_enabled":true,"cross_namespace_clone_enabled":false},"status":"active"}`
}
