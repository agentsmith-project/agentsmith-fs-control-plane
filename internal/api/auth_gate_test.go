package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
)

func TestAuthGateReturnsEnvelopeForMissingNamespaceWithoutReadingBody(t *testing.T) {
	body := &trackingReadCloser{}
	req := httptest.NewRequest(http.MethodGet, "/fake", body)
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")

	nextCalled := false
	handler := AuthGate(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			nextCalled = true
		}),
		fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
			Subject:                "service-account:agentsmith-api",
			CanonicalCallerService: "agentsmith-api",
		}},
		fakeRouteClassResolver{route: RouteMetadata{
			Method:      http.MethodGet,
			Path:        "/fake",
			OperationID: "fakeNamespaceBound",
			Class:       auth.RouteClassNamespaceBound,
			Mutating:    false,
		}},
		nil,
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called")
	}
	if body.reads != 0 {
		t.Fatalf("auth gate read request body %d time(s)", body.reads)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var envelope ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope did not decode: %v", err)
	}
	if envelope.Error.Code != CodeResourceNamespaceMismatch {
		t.Fatalf("expected %s, got %s", CodeResourceNamespaceMismatch, envelope.Error.Code)
	}
	if envelope.Error.CorrelationID != "corr_auth_gate" {
		t.Fatalf("expected correlation id corr_auth_gate, got %q", envelope.Error.CorrelationID)
	}
	if envelope.Error.OperationID == nil || *envelope.Error.OperationID != "fakeNamespaceBound" {
		t.Fatalf("expected operation id fakeNamespaceBound, got %#v", envelope.Error.OperationID)
	}
}

func TestAuthGateReturnsEnvelopeForAuthFailureWithoutReadingBody(t *testing.T) {
	body := &trackingReadCloser{}
	req := httptest.NewRequest(http.MethodGet, "/fake", body)
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")
	req.Header.Set(auth.HeaderNamespaceID, "ns_123")

	handler := AuthGate(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("next handler was called")
		}),
		fakePrincipalResolver{err: errors.New("principal resolver failed")},
		fakeRouteClassResolver{route: RouteMetadata{
			Method:      http.MethodGet,
			Path:        "/fake",
			OperationID: "fakeVolumeGlobal",
			Class:       auth.RouteClassVolumeGlobal,
			Mutating:    false,
		}},
		nil,
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if body.reads != 0 {
		t.Fatalf("auth gate read request body %d time(s)", body.reads)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}

	var envelope ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope did not decode: %v", err)
	}
	if envelope.Error.Code != CodeAuthenticationFailed {
		t.Fatalf("expected %s, got %s", CodeAuthenticationFailed, envelope.Error.Code)
	}
}

func TestAuthGateAllowsVolumeGlobalAndOperationInspectionWithoutNamespace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		class auth.RouteClass
	}{
		{name: "volume global", class: auth.RouteClassVolumeGlobal},
		{name: "operation inspection", class: auth.RouteClassOperationInspection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackingReadCloser{}
			req := httptest.NewRequest(http.MethodGet, "/fake", body)
			req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
			req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")

			nextCalled := false
			handler := AuthGate(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					nextCalled = true
					w.WriteHeader(http.StatusNoContent)
				}),
				fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
					Subject:                "service-account:agentsmith-api",
					CanonicalCallerService: "agentsmith-api",
				}},
				fakeRouteClassResolver{route: RouteMetadata{
					Method:      http.MethodGet,
					Path:        "/fake",
					OperationID: "fakeOperation",
					Class:       tc.class,
					Mutating:    false,
				}},
				nil,
			)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if !nextCalled {
				t.Fatal("next handler was not called")
			}
			if body.reads != 0 {
				t.Fatalf("auth gate read request body %d time(s)", body.reads)
			}
			if rec.Code != http.StatusNoContent {
				t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthGateFailsClosedForRequiredRoleWithoutAllowedCallerPolicy(t *testing.T) {
	body := &trackingReadCloser{}
	req := httptest.NewRequest(http.MethodGet, "/fake", body)
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")
	req.Header.Set(auth.HeaderNamespaceID, "ns_123")

	nextCalled := false
	handler := AuthGate(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			nextCalled = true
		}),
		fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
			Subject:                "service-account:agentsmith-api",
			CanonicalCallerService: "agentsmith-api",
		}},
		fakeRouteClassResolver{route: RouteMetadata{
			Method:       http.MethodGet,
			Path:         "/fake",
			OperationID:  "fakeRepoRead",
			Class:        auth.RouteClassNamespaceBound,
			Mutating:     false,
			RequiredRole: auth.RoleRepoAdmin,
		}},
		nil,
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called")
	}
	if body.reads != 0 {
		t.Fatalf("auth gate read request body %d time(s)", body.reads)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}

	var envelope ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope did not decode: %v", err)
	}
	if envelope.Error.Code != CodeCapabilityDenied {
		t.Fatalf("expected %s, got %s", CodeCapabilityDenied, envelope.Error.Code)
	}
}

func TestAuthGateDeniesProductCallerWithoutRequiredRole(t *testing.T) {
	body := &trackingReadCloser{}
	req := httptest.NewRequest(http.MethodGet, "/fake", body)
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")
	req.Header.Set(auth.HeaderNamespaceID, "ns_123")

	handler := AuthGate(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("next handler was called")
		}),
		fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
			Subject:                "service-account:agentsmith-api",
			CanonicalCallerService: "agentsmith-api",
		}},
		fakeRouteClassResolver{route: RouteMetadata{
			Method:       http.MethodGet,
			Path:         "/fake",
			OperationID:  "fakeExportRead",
			Class:        auth.RouteClassNamespaceBound,
			Mutating:     false,
			RequiredRole: auth.RoleExportAdmin,
		}},
		fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{
			{
				CallerService: "agentsmith-api",
				Kind:          auth.CallerKindProduct,
				Roles:         []auth.Role{auth.RoleRepoAdmin},
			},
		}},
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if body.reads != 0 {
		t.Fatalf("auth gate read request body %d time(s)", body.reads)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}

	var envelope ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope did not decode: %v", err)
	}
	if envelope.Error.Code != CodeCapabilityDenied {
		t.Fatalf("expected %s, got %s", CodeCapabilityDenied, envelope.Error.Code)
	}
}

func TestAuthGateAllowsRequiredRoleWhenPolicyGrantsCaller(t *testing.T) {
	body := &trackingReadCloser{}
	req := httptest.NewRequest(http.MethodGet, "/fake", body)
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")
	req.Header.Set(auth.HeaderNamespaceID, "ns_123")

	nextCalled := false
	handler := AuthGate(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusNoContent)
		}),
		fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
			Subject:                "service-account:agentsmith-api",
			CanonicalCallerService: "agentsmith-api",
		}},
		fakeRouteClassResolver{route: RouteMetadata{
			Method:       http.MethodGet,
			Path:         "/fake",
			OperationID:  "fakeRepoRead",
			Class:        auth.RouteClassNamespaceBound,
			Mutating:     false,
			RequiredRole: auth.RoleRepoAdmin,
		}},
		fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{
			{
				CallerService: "agentsmith-api",
				Kind:          auth.CallerKindProduct,
				Roles:         []auth.Role{auth.RoleRepoAdmin},
			},
		}},
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatal("next handler was not called")
	}
	if body.reads != 0 {
		t.Fatalf("auth gate read request body %d time(s)", body.reads)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
	}
}

func TestAuthGateAllowsOperatorAndAdminWithOperatorAdminForOperationInspection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		callerService string
		kind          auth.CallerKind
	}{
		{name: "operator", callerService: "afscp-operator", kind: auth.CallerKindOperator},
		{name: "admin", callerService: "afscp-admin", kind: auth.CallerKindAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackingReadCloser{}
			req := httptest.NewRequest(http.MethodGet, "/fake", body)
			req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
			req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")

			nextCalled := false
			handler := AuthGate(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					nextCalled = true
					w.WriteHeader(http.StatusNoContent)
				}),
				fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
					Subject:                "service-account:" + tc.callerService,
					CanonicalCallerService: tc.callerService,
				}},
				fakeRouteClassResolver{route: RouteMetadata{
					Method:       http.MethodGet,
					Path:         "/fake",
					OperationID:  "fakeOperation",
					Class:        auth.RouteClassOperationInspection,
					Mutating:     false,
					RequiredRole: auth.RoleOperatorAdmin,
				}},
				fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{
					{
						CallerService: tc.callerService,
						Kind:          tc.kind,
						Roles:         []auth.Role{auth.RoleOperatorAdmin},
					},
				}},
			)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if !nextCalled {
				t.Fatal("next handler was not called")
			}
			if body.reads != 0 {
				t.Fatalf("auth gate read request body %d time(s)", body.reads)
			}
			if rec.Code != http.StatusNoContent {
				t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestInternalRouteMetadataClassifiesNamespaceVolumeAndOperationRoutes(t *testing.T) {
	tests := []struct {
		operationID string
		wantClass   auth.RouteClass
		wantRole    auth.Role
	}{
		{operationID: "ensureVolume", wantClass: auth.RouteClassVolumeGlobal, wantRole: auth.RoleVolumeAdmin},
		{operationID: "createRepo", wantClass: auth.RouteClassNamespaceBound, wantRole: auth.RoleRepoAdmin},
		{operationID: "getOperation", wantClass: auth.RouteClassOperationInspection, wantRole: auth.RoleOperatorAdmin},
	}

	for _, tt := range tests {
		t.Run(tt.operationID, func(t *testing.T) {
			metadata, ok := RouteMetadataByOperationID(tt.operationID)
			if !ok {
				t.Fatalf("missing route metadata for %s", tt.operationID)
			}
			if metadata.Class != tt.wantClass {
				t.Fatalf("Class = %q, want %q", metadata.Class, tt.wantClass)
			}
			if metadata.RequiredRole != tt.wantRole {
				t.Fatalf("RequiredRole = %q, want %q", metadata.RequiredRole, tt.wantRole)
			}
		})
	}
}

func TestInternalRouteMetadataMatchesTemplatedPaths(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		operationID string
		class       auth.RouteClass
	}{
		{name: "volume global", method: http.MethodPost, path: "/internal/v1/volumes/vol_123:ensure", operationID: "ensureVolume", class: auth.RouteClassVolumeGlobal},
		{name: "namespace bound", method: http.MethodGet, path: "/internal/v1/repos/repo_123", operationID: "getRepo", class: auth.RouteClassNamespaceBound},
		{name: "operation inspection", method: http.MethodGet, path: "/internal/v1/operations/op_123", operationID: "getOperation", class: auth.RouteClassOperationInspection},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, ok := RouteMetadataForRequest(httptest.NewRequest(tt.method, tt.path, nil))
			if !ok {
				t.Fatalf("missing route metadata for %s %s", tt.method, tt.path)
			}
			if metadata.OperationID != tt.operationID {
				t.Fatalf("OperationID = %q, want %q", metadata.OperationID, tt.operationID)
			}
			if metadata.Class != tt.class {
				t.Fatalf("Class = %q, want %q", metadata.Class, tt.class)
			}
		})
	}
}

type fakePrincipalResolver struct {
	principal auth.AuthenticatedPrincipal
	err       error
}

func (r fakePrincipalResolver) ResolvePrincipal(*http.Request) (auth.AuthenticatedPrincipal, error) {
	return r.principal, r.err
}

type fakeRouteClassResolver struct {
	route RouteMetadata
	ok    bool
}

func (r fakeRouteClassResolver) ResolveRouteClass(*http.Request) (RouteMetadata, bool) {
	if !r.ok && r.route.OperationID == "" {
		return RouteMetadata{}, false
	}
	return r.route, true
}

type fakeAllowedCallerPolicy struct {
	callers []auth.AllowedCaller
	err     error
}

func (p fakeAllowedCallerPolicy) AllowedCallers(*http.Request) ([]auth.AllowedCaller, error) {
	return p.callers, p.err
}
