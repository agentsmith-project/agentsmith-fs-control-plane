package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestNamespaceVolumeBindingAllowedCallerPolicyMapsActiveBinding(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123",
		resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}},
		resources.AllowedCaller{CallerService: "runtime-orchestrator", Roles: []resources.CallerRole{resources.CallerRoleOrchestratorMount}},
	)}
	policy := NamespaceVolumeBindingAllowedCallerPolicy{Reader: reader}

	callers, err := policy.AllowedCallers(namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))
	if err != nil {
		t.Fatalf("AllowedCallers returned error: %v", err)
	}
	if len(callers) != 2 {
		t.Fatalf("callers = %#v, want two mapped callers", callers)
	}
	if callers[0].CallerService != "product-caller" || callers[0].Kind != auth.CallerKindProduct || callers[0].Roles[0] != auth.RoleRepoAdmin {
		t.Fatalf("product caller = %#v", callers[0])
	}
	if callers[1].Kind != auth.CallerKindOrchestrator || callers[1].Roles[0] != auth.RoleOrchestratorMount {
		t.Fatalf("orchestrator caller = %#v", callers[1])
	}
	if reader.calls != 1 || reader.namespaceID != "ns_123" {
		t.Fatalf("reader calls/ns = %d/%q, want one ns_123 read", reader.calls, reader.namespaceID)
	}
}

func TestNamespaceVolumeBindingAllowedCallerPolicyRejectsInvalidAndMismatchWithoutStoreRead(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		ns     string
		code   ErrorCode
	}{
		{name: "invalid path namespace", method: http.MethodGet, path: "/internal/v1/namespaces/bad_ns/volume-binding", ns: "bad_ns", code: CodeInvalidID},
		{name: "invalid header namespace", method: http.MethodGet, path: "/internal/v1/namespaces/ns_123/volume-binding", ns: "bad_ns", code: CodeInvalidID},
		{name: "path header mismatch", method: http.MethodGet, path: "/internal/v1/namespaces/ns_123/volume-binding", ns: "ns_456", code: CodeResourceNamespaceMismatch},
		{name: "mutating route invalid path namespace", method: http.MethodPut, path: "/internal/v1/namespaces/bad_ns", ns: "bad_ns", code: CodeInvalidID},
		{name: "mutating route path header mismatch", method: http.MethodPut, path: "/internal/v1/namespaces/ns_123", ns: "ns_456", code: CodeResourceNamespaceMismatch},
		{name: "mutating volume binding route path header mismatch", method: http.MethodPut, path: "/internal/v1/namespaces/ns_123/volume-binding", ns: "ns_456", code: CodeResourceNamespaceMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeNamespaceVolumeBindingReader{}
			_, err := (NamespaceVolumeBindingAllowedCallerPolicy{Reader: reader}).AllowedCallers(namespaceBindingRequest(tt.method, tt.path, tt.ns))
			if err == nil {
				t.Fatal("AllowedCallers succeeded, want error")
			}
			if reader.calls != 0 {
				t.Fatalf("reader calls = %d, want 0", reader.calls)
			}
			if got := policyErrorCode(t, err); got != tt.code {
				t.Fatalf("policy error code = %s, want %s", got, tt.code)
			}
		})
	}
}

func TestNamespaceVolumeBindingAllowedCallerPolicyDisabledBindingIsRouteAware(t *testing.T) {
	disabled := namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleNamespaceAdmin}})
	disabled.Status = resources.NamespaceStatusDisabled

	t.Run("mutating namespace route denied", func(t *testing.T) {
		reader := &fakeNamespaceVolumeBindingReader{binding: disabled}
		_, err := (NamespaceVolumeBindingAllowedCallerPolicy{Reader: reader}).AllowedCallers(namespaceBindingRequest(http.MethodPut, "/internal/v1/namespaces/ns_123", "ns_123"))
		if err == nil {
			t.Fatal("AllowedCallers succeeded, want namespace disabled error")
		}
		perr := policyError(t, err)
		if perr.Code != CodeNamespaceDisabled || perr.Status != http.StatusForbidden || perr.Retryable {
			t.Fatalf("policy error = %#v, want NAMESPACE_DISABLED 403 retryable false", perr)
		}
	})

	t.Run("read only binding route allowed", func(t *testing.T) {
		reader := &fakeNamespaceVolumeBindingReader{binding: disabled}
		callers, err := (NamespaceVolumeBindingAllowedCallerPolicy{Reader: reader}).AllowedCallers(namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))
		if err != nil {
			t.Fatalf("AllowedCallers returned error for read-only route: %v", err)
		}
		if len(callers) != 1 || callers[0].Roles[0] != auth.RoleNamespaceAdmin {
			t.Fatalf("callers = %#v, want mapped read-only policy callers", callers)
		}
	})
}

func TestNamespaceVolumeBindingAllowedCallerPolicyMapsNotFoundOutageAndInternalInvariant(t *testing.T) {
	tests := []struct {
		name   string
		reader NamespaceVolumeBindingReader
		code   ErrorCode
		status int
	}{
		{name: "not found", reader: &fakeNamespaceVolumeBindingReader{err: sql.ErrNoRows}, code: CodeNamespaceNotFound, status: http.StatusNotFound},
		{name: "store outage", reader: &fakeNamespaceVolumeBindingReader{err: errors.New("postgres password=secret failed")}, code: CodeStorageUnavailable, status: http.StatusServiceUnavailable},
		{name: "nil reader", code: CodeInternalError, status: http.StatusInternalServerError},
		{name: "returned namespace mismatch", reader: &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_other", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})}, code: CodeInternalError, status: http.StatusInternalServerError},
		{name: "stored binding invalid", reader: &fakeNamespaceVolumeBindingReader{binding: resources.NamespaceVolumeBinding{NamespaceID: "ns_123", DefaultVolumeID: "vol_123", Status: resources.NamespaceStatusActive}}, code: CodeInternalError, status: http.StatusInternalServerError},
		{name: "stored caller cannot map", reader: &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleVolumeAdmin}})}, code: CodeInternalError, status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (NamespaceVolumeBindingAllowedCallerPolicy{Reader: tt.reader}).AllowedCallers(namespaceBindingRequest(http.MethodGet, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"))
			if err == nil {
				t.Fatal("AllowedCallers succeeded, want error")
			}
			perr := policyError(t, err)
			if perr.Code != tt.code || perr.Status != tt.status {
				t.Fatalf("policy error = %#v, want code/status %s/%d", perr, tt.code, tt.status)
			}
			if strings.Contains(perr.Error(), "secret") || strings.Contains(perr.Error(), "postgres") {
				t.Fatalf("policy error leaked raw store/internal detail: %v", perr)
			}
		})
	}
}

func namespacePolicyBindingFixture(namespaceID string, callers ...resources.AllowedCaller) resources.NamespaceVolumeBinding {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	return resources.NamespaceVolumeBinding{
		NamespaceID:       namespaceID,
		DefaultVolumeID:   "vol_123",
		AllowedCallers:    callers,
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func policyErrorCode(t *testing.T, err error) ErrorCode {
	t.Helper()
	return policyError(t, err).Code
}

func policyError(t *testing.T, err error) *AllowedCallerPolicyError {
	t.Helper()
	var perr *AllowedCallerPolicyError
	if !errors.As(err, &perr) {
		t.Fatalf("error = %T %v, want AllowedCallerPolicyError", err, err)
	}
	return perr
}
