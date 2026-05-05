package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
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
			body := &trackingReadCloser{payload: []byte("body-secret")}
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

func TestAuthGateInjectsBoundRequestContextForNextHandler(t *testing.T) {
	tests := []struct {
		name         string
		headerCaller string
		wantCaller   string
	}{
		{name: "missing caller header uses canonical principal service", wantCaller: "agentsmith-api"},
		{name: "caller header whitespace is normalized to canonical principal service", headerCaller: "  agentsmith-api  ", wantCaller: "agentsmith-api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/fake", nil)
			req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
			req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")
			if tt.headerCaller != "" {
				req.Header.Set(auth.HeaderCallerService, tt.headerCaller)
			}

			nextCalled := false
			handler := AuthGate(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					nextCalled = true
					requestContext, ok := RequestContextFromRequest(r)
					if !ok {
						t.Fatal("RequestContextFromRequest ok = false, want true")
					}
					if requestContext.CallerService != tt.wantCaller {
						t.Fatalf("CallerService = %q, want %q", requestContext.CallerService, tt.wantCaller)
					}
					w.WriteHeader(http.StatusNoContent)
				}),
				fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
					Subject:                "service-account:agentsmith-api",
					CanonicalCallerService: "agentsmith-api",
				}},
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

			if !nextCalled {
				t.Fatal("next handler was not called")
			}
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d body = %s, want 204", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAuthGateCallerServiceMismatchDoesNotInjectOrCallNext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/fake", nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_auth_gate")
	req.Header.Set(auth.HeaderCallerService, "other-service")

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
			OperationID: "fakeVolumeGlobal",
			Class:       auth.RouteClassVolumeGlobal,
			Mutating:    false,
		}},
		nil,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("next handler was called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s, want 401", rec.Code, rec.Body.String())
	}
	env := decodeErrorEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != CodeAuthenticationFailed {
		t.Fatalf("error code = %s, want AUTHENTICATION_FAILED", env.Error.Code)
	}
	if _, ok := RequestContextFromRequest(req); ok {
		t.Fatal("original request unexpectedly has injected request context")
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
	if envelope.Error.Code != CodeCallerNotAllowed {
		t.Fatalf("expected %s, got %s", CodeCallerNotAllowed, envelope.Error.Code)
	}
}

func TestAuthGateDeniesCallerServiceOutsideAllowlist(t *testing.T) {
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
			OperationID:  "fakeRepoRead",
			Class:        auth.RouteClassNamespaceBound,
			Mutating:     false,
			RequiredRole: auth.RoleRepoAdmin,
		}},
		fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{
			{
				CallerService: "other-product-api",
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
	if envelope.Error.Code != CodeCallerNotAllowed {
		t.Fatalf("expected %s, got %s", CodeCallerNotAllowed, envelope.Error.Code)
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
	if envelope.Error.Code != CodeRoleNotAllowed {
		t.Fatalf("expected %s, got %s", CodeRoleNotAllowed, envelope.Error.Code)
	}
}

func TestAuthGateMapsClassifiedAllowedCallerPolicyErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  ErrorCode
		wantHTTP  int
		retryable bool
	}{
		{name: "storage unavailable", err: NewAllowedCallerPolicyError(CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", "store_unavailable"), wantCode: CodeStorageUnavailable, wantHTTP: http.StatusServiceUnavailable, retryable: true},
		{name: "namespace not found", err: NewAllowedCallerPolicyError(CodeNamespaceNotFound, http.StatusNotFound, false, "namespace was not found", "namespace_not_found"), wantCode: CodeNamespaceNotFound, wantHTTP: http.StatusNotFound},
		{name: "invalid namespace", err: NewAllowedCallerPolicyError(CodeInvalidID, http.StatusBadRequest, false, "invalid namespace id", "invalid_namespace_id"), wantCode: CodeInvalidID, wantHTTP: http.StatusBadRequest},
		{name: "namespace mismatch", err: NewAllowedCallerPolicyError(CodeResourceNamespaceMismatch, http.StatusBadRequest, false, "request namespace does not match route namespace", "namespace_mismatch"), wantCode: CodeResourceNamespaceMismatch, wantHTTP: http.StatusBadRequest},
		{name: "internal invariant", err: NewAllowedCallerPolicyError(CodeInternalError, http.StatusInternalServerError, false, "internal server error", "policy_invariant_failed"), wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &fakeAuditSink{}
			req := httptest.NewRequest(http.MethodGet, "/fake", nil)
			req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
			req.Header.Set(auth.HeaderCorrelationID, "corr_policy")
			req.Header.Set(auth.HeaderNamespaceID, "ns_123")
			req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
			handler := AuthGateWithAuditSink(
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler was called") }),
				fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "service-account:agentsmith-api", CanonicalCallerService: "agentsmith-api"}},
				fakeRouteClassResolver{route: RouteMetadata{Method: http.MethodGet, Path: "/fake", OperationID: "fakeRepoRead", Class: auth.RouteClassNamespaceBound, RequiredRole: auth.RoleRepoAdmin}},
				fakeAllowedCallerPolicy{err: errors.Join(tt.err, errors.New("postgres dsn password=secret-password"))},
				sink,
			)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantHTTP {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.wantHTTP)
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error envelope did not decode: %v", err)
			}
			if envelope.Error.Code != tt.wantCode || envelope.Error.Retryable != tt.retryable {
				t.Fatalf("error = %#v, want code %s retryable %v", envelope.Error, tt.wantCode, tt.retryable)
			}
			for _, leaked := range []string{"postgres", "dsn", "secret-password"} {
				if strings.Contains(rec.Body.String(), leaked) {
					t.Fatalf("classified policy error leaked raw error %q: %s", leaked, rec.Body.String())
				}
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit events = %#v, want one", sink.events)
			}
			if got := sink.events[0].Details["error_code"]; got != string(tt.wantCode) {
				t.Fatalf("audit error_code = %#v, want %s", got, tt.wantCode)
			}
			auditBody := auditEventString(t, sink.events[0])
			if strings.Contains(auditBody, "secret-password") || strings.Contains(auditBody, "postgres") {
				t.Fatalf("audit event leaked raw policy error: %s", auditBody)
			}
		})
	}
}

func TestAuthGateMapsUnclassifiedAllowedCallerPolicyErrorToInternalError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/fake", nil)
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_policy")
	req.Header.Set(auth.HeaderNamespaceID, "ns_123")
	req.Header.Set(auth.HeaderCallerService, "agentsmith-api")
	handler := AuthGate(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler was called") }),
		fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "service-account:agentsmith-api", CanonicalCallerService: "agentsmith-api"}},
		fakeRouteClassResolver{route: RouteMetadata{Method: http.MethodGet, Path: "/fake", OperationID: "fakeRepoRead", Class: auth.RouteClassNamespaceBound, RequiredRole: auth.RoleRepoAdmin}},
		fakeAllowedCallerPolicy{err: errors.New("postgres dsn password=secret-password")},
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s, want 500", rec.Code, rec.Body.String())
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope did not decode: %v", err)
	}
	if envelope.Error.Code != CodeInternalError || envelope.Error.Retryable {
		t.Fatalf("error = %#v, want INTERNAL_ERROR retryable false", envelope.Error)
	}
	for _, leaked := range []string{"postgres", "dsn", "secret-password"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("unclassified policy error leaked raw error %q: %s", leaked, rec.Body.String())
		}
	}
}

func TestAuthGateDeniesCallerKindThatCannotUseConfiguredRole(t *testing.T) {
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
			OperationID:  "fakeOrchestratorPlan",
			Class:        auth.RouteClassNamespaceBound,
			Mutating:     false,
			RequiredRole: auth.RoleOrchestratorMount,
		}},
		fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{
			{
				CallerService: "agentsmith-api",
				Kind:          auth.CallerKindProduct,
				Roles:         []auth.Role{auth.RoleOrchestratorMount},
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
	if envelope.Error.Code != CodeRoleNotAllowed {
		t.Fatalf("expected %s, got %s", CodeRoleNotAllowed, envelope.Error.Code)
	}
}

func TestAuthGateWithAuditSinkEmitsDeniedEventsWithoutSensitiveRequestData(t *testing.T) {
	for _, tc := range []struct {
		name              string
		principalResolver PrincipalResolver
		callerPolicy      AllowedCallerPolicy
		route             RouteMetadata
		wantStatus        int
		wantCode          ErrorCode
		wantCallerService string
	}{
		{
			name:              "authentication denial",
			principalResolver: fakePrincipalResolver{err: errors.New("principal resolver failed")},
			route: RouteMetadata{
				Method:      http.MethodGet,
				Path:        "/fake/{resourceId}",
				OperationID: "fakeVolumeGlobal",
				Class:       auth.RouteClassVolumeGlobal,
				Mutating:    false,
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeAuthenticationFailed,
		},
		{
			name: "namespace denial",
			principalResolver: fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
				Subject:                "service-account:agentsmith-api",
				CanonicalCallerService: "agentsmith-api",
			}},
			route: RouteMetadata{
				Method:      http.MethodGet,
				Path:        "/fake/{resourceId}",
				OperationID: "fakeNamespaceBound",
				Class:       auth.RouteClassNamespaceBound,
				Mutating:    false,
			},
			wantStatus:        http.StatusBadRequest,
			wantCode:          CodeResourceNamespaceMismatch,
			wantCallerService: "agentsmith-api",
		},
		{
			name: "caller denial",
			principalResolver: fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
				Subject:                "service-account:agentsmith-api",
				CanonicalCallerService: "agentsmith-api",
			}},
			route: RouteMetadata{
				Method:       http.MethodGet,
				Path:         "/fake/{resourceId}",
				OperationID:  "fakeRepoRead",
				Class:        auth.RouteClassNamespaceBound,
				Mutating:     false,
				RequiredRole: auth.RoleRepoAdmin,
			},
			wantStatus:        http.StatusForbidden,
			wantCode:          CodeCallerNotAllowed,
			wantCallerService: "agentsmith-api",
		},
		{
			name: "role denial",
			principalResolver: fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
				Subject:                "service-account:agentsmith-api",
				CanonicalCallerService: "agentsmith-api",
			}},
			callerPolicy: fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{
				{
					CallerService: "agentsmith-api",
					Kind:          auth.CallerKindProduct,
					Roles:         []auth.Role{auth.RoleRepoAdmin},
				},
			}},
			route: RouteMetadata{
				Method:       http.MethodGet,
				Path:         "/fake/{resourceId}",
				OperationID:  "fakeExportRead",
				Class:        auth.RouteClassNamespaceBound,
				Mutating:     false,
				RequiredRole: auth.RoleExportAdmin,
			},
			wantStatus:        http.StatusForbidden,
			wantCode:          CodeRoleNotAllowed,
			wantCallerService: "agentsmith-api",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeAuditSink{}
			body := &trackingReadCloser{payload: []byte("body-secret")}
			req := httptest.NewRequest(tc.route.Method, "/fake/resource_123?token=query-token", body)
			req.Header.Set(auth.HeaderAuthorization, "Bearer request-authorization-token")
			req.Header.Set(auth.HeaderCorrelationID, "corr_auth_audit")
			req.Header.Set(auth.HeaderNamespaceID, "ns_123")
			req.Header.Set(auth.HeaderActorType, "user")
			req.Header.Set(auth.HeaderActorID, "user_123")

			if tc.name == "namespace denial" {
				req.Header.Del(auth.HeaderNamespaceID)
			}

			handler := AuthGateWithAuditSink(
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Fatal("next handler was called")
				}),
				tc.principalResolver,
				fakeRouteClassResolver{route: tc.route},
				tc.callerPolicy,
				sink,
			)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if body.reads != 0 {
				t.Fatalf("auth gate read request body %d time(s)", body.reads)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error envelope did not decode: %v", err)
			}
			if envelope.Error.Code != tc.wantCode {
				t.Fatalf("expected %s, got %s", tc.wantCode, envelope.Error.Code)
			}
			if len(sink.events) != 1 {
				t.Fatalf("expected one audit event, got %d", len(sink.events))
			}

			event := sink.events[0]
			if event.Type != audit.EventTypeAuthzDenied {
				t.Fatalf("event Type = %q, want %q", event.Type, audit.EventTypeAuthzDenied)
			}
			if event.Outcome != audit.OutcomeDenied {
				t.Fatalf("event Outcome = %q, want %q", event.Outcome, audit.OutcomeDenied)
			}
			if event.CorrelationID != "corr_auth_audit" {
				t.Fatalf("event CorrelationID = %q, want corr_auth_audit", event.CorrelationID)
			}
			if event.OperationID != "" {
				t.Fatalf("denied audit event OperationID = %q, want empty", event.OperationID)
			}
			if event.Resource.Type != "route" || event.Resource.ID != "/fake/{resourceId}" {
				t.Fatalf("event Resource = %#v, want route /fake/{resourceId}", event.Resource)
			}
			if got, want := event.Resource.NamespaceID, strings.TrimSpace(req.Header.Get(auth.HeaderNamespaceID)); got != want {
				t.Fatalf("event Resource.NamespaceID = %#v, want %#v", got, want)
			}
			if tc.wantCallerService != "" && event.CallerService != tc.wantCallerService {
				t.Fatalf("event CallerService = %q, want %q", event.CallerService, tc.wantCallerService)
			}
			if got, want := event.Details["method"], tc.route.Method; got != want {
				t.Fatalf("event Details[method] = %#v, want %#v", got, want)
			}
			if got, want := event.Details["path"], "/fake/resource_123"; got != want {
				t.Fatalf("event Details[path] = %#v, want %#v", got, want)
			}
			if got, want := event.Details["error_code"], string(tc.wantCode); got != want {
				t.Fatalf("event Details[error_code] = %#v, want %#v", got, want)
			}

			rendered := auditEventString(t, event)
			for _, leaked := range []string{"request-authorization-token", "query-token", "body-secret"} {
				if strings.Contains(rendered, leaked) {
					t.Fatalf("denied audit event leaked %q in %s", leaked, rendered)
				}
			}
		})
	}
}

func TestAuthGateAuditSinkFailurePreservesDeniedResponse(t *testing.T) {
	sink := &fakeAuditSink{err: errors.New("audit sink unavailable")}
	req := httptest.NewRequest(http.MethodGet, "/fake/resource_123", &trackingReadCloser{payload: []byte("body-secret")})
	req.Header.Set(auth.HeaderAuthorization, "Bearer service-token")
	req.Header.Set(auth.HeaderCorrelationID, "corr_auth_audit_fail")
	req.Header.Set(auth.HeaderNamespaceID, "ns_123")

	handler := AuthGateWithAuditSink(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("next handler was called")
		}),
		fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{
			Subject:                "service-account:agentsmith-api",
			CanonicalCallerService: "agentsmith-api",
		}},
		fakeRouteClassResolver{route: RouteMetadata{
			Method:       http.MethodGet,
			Path:         "/fake/{resourceId}",
			OperationID:  "fakeRepoRead",
			Class:        auth.RouteClassNamespaceBound,
			Mutating:     false,
			RequiredRole: auth.RoleRepoAdmin,
		}},
		nil,
		sink,
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope did not decode: %v", err)
	}
	if envelope.Error.Code != CodeCallerNotAllowed {
		t.Fatalf("expected %s, got %s", CodeCallerNotAllowed, envelope.Error.Code)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected one attempted audit event, got %d", len(sink.events))
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

func TestAuthGateAllowsOperationInspectorAndOperatorAdminForOperationInspection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		callerService string
		kind          auth.CallerKind
		roles         []auth.Role
	}{
		{name: "product inspector", callerService: "agentsmith-api", kind: auth.CallerKindProduct, roles: []auth.Role{auth.RoleOperationInspector}},
		{name: "operator admin", callerService: "afscp-operator", kind: auth.CallerKindOperator, roles: []auth.Role{auth.RoleOperatorAdmin}},
		{name: "admin operator admin", callerService: "afscp-admin", kind: auth.CallerKindAdmin, roles: []auth.Role{auth.RoleOperatorAdmin}},
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
					RequiredRole: auth.RoleOperationInspector,
				}},
				fakeAllowedCallerPolicy{callers: []auth.AllowedCaller{
					{
						CallerService: tc.callerService,
						Kind:          tc.kind,
						Roles:         tc.roles,
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
		{operationID: "getOperation", wantClass: auth.RouteClassOperationInspection, wantRole: auth.RoleOperationInspector},
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
