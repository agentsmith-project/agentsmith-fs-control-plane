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

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

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
	if store.calls != 1 || store.namespaceID != "ns_123" || store.ctx != req.Context() {
		t.Fatalf("store calls/id/ctx = %d/%q/%v, want one call for ns_123 with request ctx", store.calls, store.namespaceID, store.ctx == req.Context())
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

func namespaceBindingHandler(reader NamespaceVolumeBindingReader) http.Handler {
	return NamespaceVolumeBindingHandler(NamespaceVolumeBindingHandlerConfig{
		Reader:            reader,
		PrincipalResolver: namespaceBindingPrincipalResolver(),
		AllowedCallers:    namespaceBindingAllowedPolicy(auth.RoleNamespaceAdmin),
	})
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
