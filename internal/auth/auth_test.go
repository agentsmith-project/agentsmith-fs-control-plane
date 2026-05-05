package auth

import (
	"errors"
	"net/http"
	"slices"
	"testing"
)

func TestCallerRolesExposeStableSchemaEnumOrder(t *testing.T) {
	want := []Role{
		RoleVolumeAdmin,
		RoleNamespaceAdmin,
		RoleRepoAdmin,
		RoleRepoLifecycleAdmin,
		RoleRestoreAdmin,
		RoleTemplateAdmin,
		RoleExportAdmin,
		RoleMountAdmin,
		RoleOperationInspector,
		RoleOrchestratorMount,
		RoleMigrationAdmin,
		RoleOperatorAdmin,
		RoleBreakGlassAdmin,
	}

	got := CallerRoles()
	if !slices.Equal(got, want) {
		t.Fatalf("CallerRoles() = %#v, want %#v", got, want)
	}

	got[0] = RoleBreakGlassAdmin
	if CallerRoles()[0] != RoleVolumeAdmin {
		t.Fatal("CallerRoles returned mutable backing storage")
	}
}

func TestParseRequestContextCanonicalHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/internal/v1/repos", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set(HeaderAuthorization, "Bearer service-token")
	req.Header.Set(HeaderIdempotencyKey, "idem-123")
	req.Header.Set(HeaderCorrelationID, "corr-123")
	req.Header.Set(HeaderNamespaceID, "ns-123")
	req.Header.Set(HeaderActorType, "user")
	req.Header.Set(HeaderActorID, "user-456")
	req.Header.Set(HeaderCallerService, "agentsmith-api")

	ctx := ParseRequestContext(req)

	if ctx.Authorization != "Bearer service-token" {
		t.Fatalf("Authorization = %q", ctx.Authorization)
	}
	if ctx.IdempotencyKey != "idem-123" {
		t.Fatalf("IdempotencyKey = %q", ctx.IdempotencyKey)
	}
	if ctx.CorrelationID != "corr-123" {
		t.Fatalf("CorrelationID = %q", ctx.CorrelationID)
	}
	if ctx.NamespaceID != "ns-123" {
		t.Fatalf("NamespaceID = %q", ctx.NamespaceID)
	}
	if ctx.Actor.Type != "user" {
		t.Fatalf("Actor.Type = %q", ctx.Actor.Type)
	}
	if ctx.Actor.ID != "user-456" {
		t.Fatalf("Actor.ID = %q", ctx.Actor.ID)
	}
	if ctx.CallerService != "agentsmith-api" {
		t.Fatalf("CallerService = %q", ctx.CallerService)
	}
}

func TestValidateRequestContextRequiresActorAndIdempotencyForMutations(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/internal/v1/repos", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderAuthorization, "Bearer service-token")
	req.Header.Set(HeaderCorrelationID, "corr-123")
	req.Header.Set(HeaderCallerService, "agentsmith-api")

	err = ValidateRequestContext(ParseRequestContext(req), req.Method)
	if !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("expected ErrMissingIdempotencyKey, got %v", err)
	}
	if !errors.Is(err, ErrMissingActor) {
		t.Fatalf("expected ErrMissingActor, got %v", err)
	}
}

func TestValidateRequestContextAllowsSafeRequestWithoutActorOrIdempotency(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/internal/v1/repos/repo-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderAuthorization, "Bearer service-token")
	req.Header.Set(HeaderCorrelationID, "corr-123")
	req.Header.Set(HeaderCallerService, "agentsmith-api")

	if err := ValidateRequestContext(ParseRequestContext(req), req.Method); err != nil {
		t.Fatalf("safe request validation failed: %v", err)
	}
}

func TestValidateRequestContextRequiresAuthorization(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/internal/v1/repos/repo-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderCorrelationID, "corr-123")
	req.Header.Set(HeaderCallerService, "agentsmith-api")

	err = ValidateRequestContext(ParseRequestContext(req), req.Method)
	if !errors.Is(err, ErrMissingAuthorization) {
		t.Fatalf("expected ErrMissingAuthorization, got %v", err)
	}
}

func TestValidateAuthenticatedRequestBindsCallerServiceFromPrincipal(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/internal/v1/repos/repo-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderAuthorization, "Bearer service-token")
	req.Header.Set(HeaderCorrelationID, "corr-123")

	ctx, err := ValidateAuthenticatedRequest(req, AuthenticatedPrincipal{
		Subject:                "service-account:agentsmith-api",
		CanonicalCallerService: "agentsmith-api",
	})
	if err != nil {
		t.Fatalf("authenticated request validation failed: %v", err)
	}
	if ctx.CallerService != "agentsmith-api" {
		t.Fatalf("CallerService = %q", ctx.CallerService)
	}
}

func TestValidateAuthenticatedRequestRejectsCallerServiceHeaderMismatch(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/internal/v1/repos/repo-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderAuthorization, "Bearer service-token")
	req.Header.Set(HeaderCorrelationID, "corr-123")
	req.Header.Set(HeaderCallerService, "afscp-admin")

	_, err = ValidateAuthenticatedRequest(req, AuthenticatedPrincipal{
		Subject:                "service-account:agentsmith-api",
		CanonicalCallerService: "agentsmith-api",
	})
	if !errors.Is(err, ErrCallerServiceMismatch) {
		t.Fatalf("expected ErrCallerServiceMismatch, got %v", err)
	}
}

func TestValidateAuthenticatedRequestRequiresAuthenticatedPrincipal(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/internal/v1/repos/repo-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderAuthorization, "Bearer service-token")
	req.Header.Set(HeaderCorrelationID, "corr-123")

	_, err = ValidateAuthenticatedRequest(req, AuthenticatedPrincipal{})
	if !errors.Is(err, ErrMissingAuthenticatedPrincipal) {
		t.Fatalf("expected ErrMissingAuthenticatedPrincipal, got %v", err)
	}
}

func TestNamespaceMismatch(t *testing.T) {
	tests := []struct {
		name              string
		requestNamespace  string
		resourceNamespace string
		want              bool
	}{
		{name: "same", requestNamespace: "ns-123", resourceNamespace: "ns-123", want: false},
		{name: "different", requestNamespace: "ns-123", resourceNamespace: "ns-456", want: true},
		{name: "missing request namespace for namespaced resource", requestNamespace: "", resourceNamespace: "ns-123", want: true},
		{name: "not namespace scoped", requestNamespace: "", resourceNamespace: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NamespaceMismatch(tt.requestNamespace, tt.resourceNamespace); got != tt.want {
				t.Fatalf("NamespaceMismatch(%q, %q) = %v, want %v", tt.requestNamespace, tt.resourceNamespace, got, tt.want)
			}
		})
	}
}

func TestNamespaceBoundMismatchRequiresBothNamespacesToMatch(t *testing.T) {
	tests := []struct {
		name              string
		requestNamespace  string
		resourceNamespace string
		want              bool
	}{
		{name: "same", requestNamespace: "ns-123", resourceNamespace: "ns-123", want: false},
		{name: "different", requestNamespace: "ns-123", resourceNamespace: "ns-456", want: true},
		{name: "missing request namespace", requestNamespace: "", resourceNamespace: "ns-123", want: true},
		{name: "missing resource namespace", requestNamespace: "ns-123", resourceNamespace: "", want: true},
		{name: "both missing", requestNamespace: "", resourceNamespace: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NamespaceBoundMismatch(tt.requestNamespace, tt.resourceNamespace); got != tt.want {
				t.Fatalf("NamespaceBoundMismatch(%q, %q) = %v, want %v", tt.requestNamespace, tt.resourceNamespace, got, tt.want)
			}
		})
	}
}

func TestCallerNotAllowedRequiresConfiguredCallerAndRole(t *testing.T) {
	allowed := []AllowedCaller{
		{
			CallerService: "agentsmith-api",
			Kind:          CallerKindProduct,
			Roles:         []Role{RoleRepoAdmin, RoleExportAdmin, RoleMountAdmin},
		},
		{
			CallerService: "afscp-operator",
			Kind:          CallerKindOperator,
			Roles:         []Role{RoleOperatorAdmin, RoleBreakGlassAdmin},
		},
		{
			CallerService: "afscp-admin",
			Kind:          CallerKindAdmin,
			Roles:         []Role{RoleNamespaceAdmin, RoleBreakGlassAdmin},
		},
	}

	if CallerNotAllowed("agentsmith-api", RoleRepoAdmin, allowed) {
		t.Fatal("expected agentsmith-api repo admin to be allowed")
	}
	if !CallerNotAllowed("agentsmith-api", RoleRestoreAdmin, allowed) {
		t.Fatal("expected missing role to be denied")
	}
	if !CallerNotAllowed("unknown-service", RoleRepoAdmin, allowed) {
		t.Fatal("expected unknown caller to be denied")
	}
	if CallerNotAllowed("afscp-operator", RoleBreakGlassAdmin, allowed) {
		t.Fatal("expected operator break-glass role to be allowed")
	}
	if CallerNotAllowed("afscp-admin", RoleNamespaceAdmin, allowed) {
		t.Fatal("expected admin namespace role to be allowed")
	}
	if CallerNotAllowed("afscp-admin", RoleBreakGlassAdmin, allowed) {
		t.Fatal("expected admin break-glass role to be allowed")
	}
}

func TestCallerRoleDenialReasonDistinguishesCallerAndRoleFailures(t *testing.T) {
	allowed := []AllowedCaller{
		{
			CallerService: "agentsmith-api",
			Kind:          CallerKindProduct,
			Roles:         []Role{RoleRepoAdmin},
		},
		{
			CallerService: "sandbox-manager",
			Kind:          CallerKindProduct,
			Roles:         []Role{RoleOrchestratorMount},
		},
	}

	tests := []struct {
		name         string
		caller       string
		requiredRole Role
		want         CallerRoleDenialReason
	}{
		{name: "allowed", caller: "agentsmith-api", requiredRole: RoleRepoAdmin, want: CallerRoleAllowed},
		{name: "caller missing", caller: "unknown-service", requiredRole: RoleRepoAdmin, want: CallerServiceNotAllowed},
		{name: "role missing", caller: "agentsmith-api", requiredRole: RoleExportAdmin, want: CallerRoleNotAllowed},
		{name: "kind cannot use configured role", caller: "sandbox-manager", requiredRole: RoleOrchestratorMount, want: CallerRoleNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CallerRoleDenialReasonFor(tt.caller, tt.requiredRole, allowed); got != tt.want {
				t.Fatalf("CallerRoleDenialReasonFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOperatorAdminSatisfiesOperationInspectorForAdminAndOperatorCallers(t *testing.T) {
	for _, caller := range []AllowedCaller{
		{
			CallerService: "afscp-operator",
			Kind:          CallerKindOperator,
			Roles:         []Role{RoleOperatorAdmin},
		},
		{
			CallerService: "afscp-admin",
			Kind:          CallerKindAdmin,
			Roles:         []Role{RoleOperatorAdmin},
		},
	} {
		if CallerNotAllowed(caller.CallerService, RoleOperationInspector, []AllowedCaller{caller}) {
			t.Fatalf("expected %s operator_admin to satisfy operation_inspector", caller.CallerService)
		}
	}

	product := AllowedCaller{
		CallerService: "agentsmith-api",
		Kind:          CallerKindProduct,
		Roles:         []Role{RoleOperatorAdmin},
	}
	if !CallerNotAllowed(product.CallerService, RoleOperationInspector, []AllowedCaller{product}) {
		t.Fatal("ordinary product caller must not use operator_admin as operation_inspector")
	}
}

func TestOrdinaryProductCallerCannotUsePrivilegedCapabilities(t *testing.T) {
	allowed := []AllowedCaller{
		{
			CallerService: "agentsmith-api",
			Kind:          CallerKindProduct,
			Roles: []Role{
				RoleRepoAdmin,
				RoleMountAdmin,
				RoleOrchestratorMount,
				RoleBreakGlassAdmin,
			},
		},
		{
			CallerService: "sandbox-manager",
			Kind:          CallerKindOrchestrator,
			Roles:         []Role{RoleOrchestratorMount},
		},
		{
			CallerService: "afscp-migration",
			Kind:          CallerKindMigration,
			Roles:         []Role{RoleMigrationAdmin},
		},
	}

	if !CallerNotAllowed("agentsmith-api", RoleOrchestratorMount, allowed) {
		t.Fatal("ordinary product caller must not be able to fetch orchestrator plans")
	}
	if !CallerNotAllowed("agentsmith-api", RoleBreakGlassAdmin, allowed) {
		t.Fatal("ordinary product caller must not be able to use break-glass")
	}
	if CallerNotAllowed("sandbox-manager", RoleOrchestratorMount, allowed) {
		t.Fatal("orchestrator caller should be allowed orchestrator_mount")
	}
	if CallerNotAllowed("afscp-migration", RoleMigrationAdmin, allowed) {
		t.Fatal("migration caller should be allowed migration_admin")
	}
}

func TestProductCallerRoleAllowlistIgnoresConfiguredPrivilegedRoles(t *testing.T) {
	allowed := []AllowedCaller{
		{
			CallerService: "agentsmith-api",
			Kind:          CallerKindProduct,
			Roles: []Role{
				RoleRepoAdmin,
				RoleRepoLifecycleAdmin,
				RoleRestoreAdmin,
				RoleTemplateAdmin,
				RoleExportAdmin,
				RoleMountAdmin,
				RoleNamespaceAdmin,
				RoleOperationInspector,
				RoleVolumeAdmin,
				RoleOperatorAdmin,
				RoleMigrationAdmin,
				RoleOrchestratorMount,
				RoleBreakGlassAdmin,
			},
		},
	}

	ordinaryRoles := []Role{
		RoleRepoAdmin,
		RoleRepoLifecycleAdmin,
		RoleRestoreAdmin,
		RoleTemplateAdmin,
		RoleExportAdmin,
		RoleMountAdmin,
		RoleNamespaceAdmin,
		RoleOperationInspector,
	}
	for _, role := range ordinaryRoles {
		if CallerNotAllowed("agentsmith-api", role, allowed) {
			t.Fatalf("expected product caller to be allowed ordinary role %q", role)
		}
	}

	privilegedRoles := []Role{
		RoleVolumeAdmin,
		RoleOperatorAdmin,
		RoleMigrationAdmin,
		RoleOrchestratorMount,
		RoleBreakGlassAdmin,
	}
	for _, role := range privilegedRoles {
		if !CallerNotAllowed("agentsmith-api", role, allowed) {
			t.Fatalf("expected product caller to be denied privileged role %q", role)
		}
	}
}

func TestSpecializedCallerKindsUseExplicitRoleAllowlists(t *testing.T) {
	allowed := []AllowedCaller{
		{
			CallerService: "sandbox-manager",
			Kind:          CallerKindOrchestrator,
			Roles: []Role{
				RoleOrchestratorMount,
				RoleRepoAdmin,
				RoleMigrationAdmin,
			},
		},
		{
			CallerService: "afscp-migration",
			Kind:          CallerKindMigration,
			Roles: []Role{
				RoleMigrationAdmin,
				RoleRepoAdmin,
				RoleOrchestratorMount,
			},
		},
		{
			CallerService: "afscp-operator",
			Kind:          CallerKindOperator,
			Roles: []Role{
				RoleVolumeAdmin,
				RoleOperatorAdmin,
				RoleBreakGlassAdmin,
			},
		},
	}

	if CallerNotAllowed("sandbox-manager", RoleOrchestratorMount, allowed) {
		t.Fatal("expected orchestrator caller to be allowed orchestrator_mount")
	}
	if !CallerNotAllowed("sandbox-manager", RoleRepoAdmin, allowed) {
		t.Fatal("expected orchestrator caller to be denied repo_admin")
	}
	if !CallerNotAllowed("sandbox-manager", RoleMigrationAdmin, allowed) {
		t.Fatal("expected orchestrator caller to be denied migration_admin")
	}
	if CallerNotAllowed("afscp-migration", RoleMigrationAdmin, allowed) {
		t.Fatal("expected migration caller to be allowed migration_admin")
	}
	if !CallerNotAllowed("afscp-migration", RoleRepoAdmin, allowed) {
		t.Fatal("expected migration caller to be denied repo_admin")
	}
	if !CallerNotAllowed("afscp-migration", RoleOrchestratorMount, allowed) {
		t.Fatal("expected migration caller to be denied orchestrator_mount")
	}
	if CallerNotAllowed("afscp-operator", RoleVolumeAdmin, allowed) {
		t.Fatal("expected operator caller to be allowed volume_admin")
	}
	if CallerNotAllowed("afscp-operator", RoleOperatorAdmin, allowed) {
		t.Fatal("expected operator caller to be allowed operator_admin")
	}
	if CallerNotAllowed("afscp-operator", RoleBreakGlassAdmin, allowed) {
		t.Fatal("expected operator caller to be allowed break_glass_admin")
	}
}
