package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
)

func TestStaticAllowedCallerPolicyReturnsDefensiveCopy(t *testing.T) {
	source := []auth.AllowedCaller{{
		CallerService: "deployment-admin",
		Kind:          auth.CallerKindProduct,
		Roles:         []auth.Role{auth.RoleVolumeAdmin},
	}}
	policy := NewStaticAllowedCallerPolicy(source)

	first, err := policy.AllowedCallers(httptest.NewRequest(http.MethodGet, "/internal/v1/volumes/vol_123/health", nil))
	if err != nil {
		t.Fatalf("AllowedCallers returned error: %v", err)
	}
	source[0].CallerService = "mutated-source"
	source[0].Roles[0] = auth.RoleRepoAdmin
	first[0].Roles[0] = auth.RoleMountAdmin

	second, err := policy.AllowedCallers(httptest.NewRequest(http.MethodGet, "/internal/v1/volumes/vol_123/health", nil))
	if err != nil {
		t.Fatalf("AllowedCallers returned error after mutation: %v", err)
	}
	if second[0].CallerService != "deployment-admin" || second[0].Roles[0] != auth.RoleVolumeAdmin {
		t.Fatalf("callers = %#v, want original caller/role after source and result mutation", second)
	}
}

func TestRouteAwareAllowedCallerPolicySelectsDeploymentPolicies(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want *recordingAllowedCallerPolicy
	}{
		{
			name: "ensure volume uses deployment global policy",
			req:  namespaceBindingRequest(http.MethodPost, "/internal/v1/volumes/vol_123:ensure", "ns_123"),
		},
		{
			name: "volume health uses deployment global policy",
			req:  namespaceBindingRequest(http.MethodGet, "/internal/v1/volumes/vol_123/health", "ns_123"),
		},
		{
			name: "upsert namespace uses deployment namespace policy",
			req:  namespaceBindingRequest(http.MethodPut, "/internal/v1/namespaces/ns_123", "ns_123"),
		},
		{
			name: "disable namespace uses deployment namespace policy",
			req:  namespaceBindingRequest(http.MethodPost, "/internal/v1/namespaces/ns_123:disable", "ns_123"),
		},
		{
			name: "put namespace binding uses deployment namespace policy",
			req:  namespaceBindingRequest(http.MethodPut, "/internal/v1/namespaces/ns_123/volume-binding", "ns_123"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			global := &recordingAllowedCallerPolicy{callers: namespaceAllowedCallers(auth.RoleVolumeAdmin)}
			namespace := &recordingAllowedCallerPolicy{callers: namespaceAllowedCallers(auth.RoleNamespaceAdmin)}
			binding := &recordingAllowedCallerPolicy{callers: namespaceAllowedCallers(auth.RoleRepoAdmin)}
			policy := RouteAwareAllowedCallerPolicy{
				DeploymentGlobal:    global,
				DeploymentNamespace: namespace,
				NamespaceBinding:    binding,
			}

			_, err := policy.AllowedCallers(tt.req)
			if err != nil {
				t.Fatalf("AllowedCallers returned error: %v", err)
			}

			route, ok := RouteMetadataForRequest(tt.req)
			if !ok {
				t.Fatalf("test request did not match a route: %s %s", tt.req.Method, tt.req.URL.Path)
			}
			switch route.OperationID {
			case "ensureVolume", "getVolumeHealth":
				if global.calls != 1 || namespace.calls != 0 || binding.calls != 0 {
					t.Fatalf("calls global/namespace/binding = %d/%d/%d, want 1/0/0", global.calls, namespace.calls, binding.calls)
				}
			case "upsertNamespace", "disableNamespace", "putNamespaceVolumeBinding":
				if global.calls != 0 || namespace.calls != 1 || binding.calls != 0 {
					t.Fatalf("calls global/namespace/binding = %d/%d/%d, want 0/1/0", global.calls, namespace.calls, binding.calls)
				}
			default:
				t.Fatalf("unexpected operation id %q", route.OperationID)
			}
		})
	}
}

func TestRouteAwareAllowedCallerPolicySelectsBindingForNamespaceResources(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "get namespace binding", method: http.MethodGet, path: "/internal/v1/namespaces/ns_123/volume-binding"},
		{name: "repo read", method: http.MethodGet, path: "/internal/v1/repos/repo_123"},
		{name: "save point create", method: http.MethodPost, path: "/internal/v1/repos/repo_123/save-points"},
		{name: "restore preview", method: http.MethodPost, path: "/internal/v1/repos/repo_123/restore-preview"},
		{name: "restore preview discard", method: http.MethodPost, path: "/internal/v1/repos/repo_123/restore-preview:discard"},
		{name: "restore run", method: http.MethodPost, path: "/internal/v1/repos/repo_123/restore-run"},
		{name: "template clone", method: http.MethodPost, path: "/internal/v1/repo-templates/tpl_123:clone"},
		{name: "export read", method: http.MethodGet, path: "/internal/v1/exports/exp_123"},
		{name: "mount read", method: http.MethodGet, path: "/internal/v1/workload-mount-bindings/wmb_123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			global := &recordingAllowedCallerPolicy{callers: namespaceAllowedCallers(auth.RoleVolumeAdmin)}
			namespace := &recordingAllowedCallerPolicy{callers: namespaceAllowedCallers(auth.RoleNamespaceAdmin)}
			binding := &recordingAllowedCallerPolicy{callers: namespaceAllowedCallers(auth.RoleRepoAdmin)}
			policy := RouteAwareAllowedCallerPolicy{
				DeploymentGlobal:    global,
				DeploymentNamespace: namespace,
				NamespaceBinding:    binding,
			}

			callers, err := policy.AllowedCallers(namespaceBindingRequest(tt.method, tt.path, "ns_123"))
			if err != nil {
				t.Fatalf("AllowedCallers returned error: %v", err)
			}
			if len(callers) != 1 || callers[0].Roles[0] != auth.RoleRepoAdmin {
				t.Fatalf("callers = %#v, want binding policy callers", callers)
			}
			if global.calls != 0 || namespace.calls != 0 || binding.calls != 1 {
				t.Fatalf("calls global/namespace/binding = %d/%d/%d, want 0/0/1", global.calls, namespace.calls, binding.calls)
			}
		})
	}
}

func TestRouteAwareAllowedCallerPolicyDoesNotHandleOperationInspectionWithBindingPolicy(t *testing.T) {
	binding := &recordingAllowedCallerPolicy{callers: namespaceAllowedCallers(auth.RoleOperationInspector)}
	policy := RouteAwareAllowedCallerPolicy{NamespaceBinding: binding}

	_, err := policy.AllowedCallers(namespaceBindingRequest(http.MethodGet, "/internal/v1/operations/op_123", "ns_123"))
	if err == nil {
		t.Fatal("AllowedCallers succeeded for operation inspection, want internal policy error")
	}
	if binding.calls != 0 {
		t.Fatalf("binding policy calls = %d, want 0", binding.calls)
	}
	perr := policyError(t, err)
	if perr.Code != CodeInternalError || perr.Status != http.StatusInternalServerError {
		t.Fatalf("policy error = %#v, want INTERNAL_ERROR 500", perr)
	}
}

func TestRouteAwareAllowedCallerPolicyClassifiedErrorPropagatesThroughAuthGate(t *testing.T) {
	binding := &recordingAllowedCallerPolicy{
		err: NewAllowedCallerPolicyError(CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", "store_unavailable"),
	}
	handler := AuthGate(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler was called") }),
		namespaceBindingPrincipalResolver(),
		InternalV1RouteClassResolver(),
		RouteAwareAllowedCallerPolicy{NamespaceBinding: binding},
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/repos/repo_123", "ns_123"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s, want 503", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeStorageUnavailable || !env.Error.Retryable {
		t.Fatalf("error = %#v, want STORAGE_UNAVAILABLE retryable", env.Error)
	}
	if binding.calls != 1 {
		t.Fatalf("binding calls = %d, want 1", binding.calls)
	}
}

func TestRouteAwareAllowedCallerPolicyMissingSelectedPolicyIsInternalError(t *testing.T) {
	handler := AuthGate(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler was called") }),
		namespaceBindingPrincipalResolver(),
		InternalV1RouteClassResolver(),
		RouteAwareAllowedCallerPolicy{},
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, namespaceBindingRequest(http.MethodGet, "/internal/v1/repos/repo_123", "ns_123"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeInternalError || env.Error.Retryable {
		t.Fatalf("error = %#v, want INTERNAL_ERROR retryable false", env.Error)
	}
}

type recordingAllowedCallerPolicy struct {
	calls   int
	request *http.Request
	callers []auth.AllowedCaller
	err     error
}

func (policy *recordingAllowedCallerPolicy) AllowedCallers(r *http.Request) ([]auth.AllowedCaller, error) {
	policy.calls++
	policy.request = r
	if policy.err != nil {
		return nil, errors.Join(policy.err, errors.New("raw policy detail password=secret"))
	}
	return policy.callers, nil
}

func namespaceAllowedCallers(roles ...auth.Role) []auth.AllowedCaller {
	return []auth.AllowedCaller{{
		CallerService: "product-caller",
		Kind:          auth.CallerKindProduct,
		Roles:         roles,
	}}
}
