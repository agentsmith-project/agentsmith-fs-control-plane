package namespaceauth

import (
	"slices"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestMapAllowedCallerMapsOrdinaryNamespaceProductRoles(t *testing.T) {
	caller := resources.AllowedCaller{
		CallerService: "product-caller",
		Roles: []resources.CallerRole{
			resources.CallerRoleNamespaceAdmin,
			resources.CallerRoleRepoAdmin,
			resources.CallerRoleRepoLifecycleAdmin,
			resources.CallerRoleRestoreAdmin,
			resources.CallerRoleTemplateAdmin,
			resources.CallerRoleExportAdmin,
			resources.CallerRoleMountAdmin,
			resources.CallerRoleOperationInspector,
		},
	}

	got, ok := MapAllowedCaller(caller)
	if !ok {
		t.Fatal("MapAllowedCaller ok = false, want true")
	}
	wantRoles := []auth.Role{
		auth.RoleNamespaceAdmin,
		auth.RoleRepoAdmin,
		auth.RoleRepoLifecycleAdmin,
		auth.RoleRestoreAdmin,
		auth.RoleTemplateAdmin,
		auth.RoleExportAdmin,
		auth.RoleMountAdmin,
		auth.RoleOperationInspector,
	}
	if got.CallerService != "product-caller" || got.Kind != auth.CallerKindProduct || !slices.Equal(got.Roles, wantRoles) {
		t.Fatalf("mapped caller = %#v, want product caller roles %#v", got, wantRoles)
	}
}

func TestMapAllowedCallerMapsDedicatedOrchestratorAndMigration(t *testing.T) {
	tests := []struct {
		name string
		role resources.CallerRole
		kind auth.CallerKind
		want auth.Role
	}{
		{name: "orchestrator", role: resources.CallerRoleOrchestratorMount, kind: auth.CallerKindOrchestrator, want: auth.RoleOrchestratorMount},
		{name: "migration", role: resources.CallerRoleMigrationAdmin, kind: auth.CallerKindMigration, want: auth.RoleMigrationAdmin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MapAllowedCaller(resources.AllowedCaller{CallerService: "dedicated-service", Roles: []resources.CallerRole{tt.role}})
			if !ok {
				t.Fatal("MapAllowedCaller ok = false, want true")
			}
			if got.Kind != tt.kind || !slices.Equal(got.Roles, []auth.Role{tt.want}) {
				t.Fatalf("mapped caller = %#v, want kind %s role %s", got, tt.kind, tt.want)
			}
		})
	}
}

func TestMapAllowedCallerDeniesPrivilegedMixedDuplicateEmptyAndUnknownRoles(t *testing.T) {
	tests := []struct {
		name  string
		roles []resources.CallerRole
	}{
		{name: "empty roles"},
		{name: "unknown role", roles: []resources.CallerRole{"unknown_role"}},
		{name: "duplicate ordinary role", roles: []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleRepoAdmin}},
		{name: "volume admin", roles: []resources.CallerRole{resources.CallerRoleVolumeAdmin}},
		{name: "operator admin", roles: []resources.CallerRole{resources.CallerRoleOperatorAdmin}},
		{name: "break glass admin", roles: []resources.CallerRole{resources.CallerRoleBreakGlassAdmin}},
		{name: "orchestrator mixed with product", roles: []resources.CallerRole{resources.CallerRoleOrchestratorMount, resources.CallerRoleRepoAdmin}},
		{name: "migration mixed with product", roles: []resources.CallerRole{resources.CallerRoleMigrationAdmin, resources.CallerRoleRepoAdmin}},
		{name: "dedicated roles mixed", roles: []resources.CallerRole{resources.CallerRoleOrchestratorMount, resources.CallerRoleMigrationAdmin}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := MapAllowedCaller(resources.AllowedCaller{CallerService: "svc", Roles: tt.roles}); ok {
				t.Fatalf("MapAllowedCaller = %#v/true, want denied", got)
			}
		})
	}
}

func TestMapAllowedCallerDeniesBlankCallerService(t *testing.T) {
	if got, ok := MapAllowedCaller(resources.AllowedCaller{
		CallerService: " \t",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
	}); ok {
		t.Fatalf("MapAllowedCaller = %#v/true, want denied for blank caller_service", got)
	}
}

func TestMapAllowedCallerReturnsDefensiveRoleCopy(t *testing.T) {
	caller := resources.AllowedCaller{
		CallerService: "product-caller",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleOperationInspector},
	}
	got, ok := MapAllowedCaller(caller)
	if !ok {
		t.Fatal("MapAllowedCaller ok = false, want true")
	}

	caller.Roles[0] = resources.CallerRoleMountAdmin
	got.Roles[0] = auth.RoleMountAdmin
	gotAgain, ok := MapAllowedCaller(resources.AllowedCaller{
		CallerService: "product-caller",
		Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleOperationInspector},
	})
	if !ok {
		t.Fatal("MapAllowedCaller second ok = false, want true")
	}
	if !slices.Equal(gotAgain.Roles, []auth.Role{auth.RoleRepoAdmin, auth.RoleOperationInspector}) {
		t.Fatalf("mapped roles = %#v, want fresh defensive copy", gotAgain.Roles)
	}
}
