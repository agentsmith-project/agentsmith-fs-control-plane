package namespaceauth

import (
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func MapAllowedCaller(caller resources.AllowedCaller) (auth.AllowedCaller, bool) {
	caller.CallerService = strings.TrimSpace(caller.CallerService)
	if caller.CallerService == "" {
		return auth.AllowedCaller{}, false
	}
	if len(caller.Roles) == 0 {
		return auth.AllowedCaller{}, false
	}
	seen := map[resources.CallerRole]bool{}
	for _, role := range caller.Roles {
		if role == "" || seen[role] {
			return auth.AllowedCaller{}, false
		}
		seen[role] = true
	}

	if len(caller.Roles) == 1 {
		switch caller.Roles[0] {
		case resources.CallerRoleOrchestratorMount:
			return auth.AllowedCaller{
				CallerService: caller.CallerService,
				Kind:          auth.CallerKindOrchestrator,
				Roles:         []auth.Role{auth.RoleOrchestratorMount},
			}, true
		case resources.CallerRoleMigrationAdmin:
			return auth.AllowedCaller{
				CallerService: caller.CallerService,
				Kind:          auth.CallerKindMigration,
				Roles:         []auth.Role{auth.RoleMigrationAdmin},
			}, true
		}
	}

	roles := make([]auth.Role, 0, len(caller.Roles))
	for _, role := range caller.Roles {
		mapped, ok := mapProductRole(role)
		if !ok {
			return auth.AllowedCaller{}, false
		}
		roles = append(roles, mapped)
	}
	return auth.AllowedCaller{
		CallerService: caller.CallerService,
		Kind:          auth.CallerKindProduct,
		Roles:         roles,
	}, true
}

func mapProductRole(role resources.CallerRole) (auth.Role, bool) {
	switch role {
	case resources.CallerRoleNamespaceAdmin:
		return auth.RoleNamespaceAdmin, true
	case resources.CallerRoleRepoAdmin:
		return auth.RoleRepoAdmin, true
	case resources.CallerRoleRepoLifecycleAdmin:
		return auth.RoleRepoLifecycleAdmin, true
	case resources.CallerRoleRestoreAdmin:
		return auth.RoleRestoreAdmin, true
	case resources.CallerRoleTemplateAdmin:
		return auth.RoleTemplateAdmin, true
	case resources.CallerRoleExportAdmin:
		return auth.RoleExportAdmin, true
	case resources.CallerRoleMountAdmin:
		return auth.RoleMountAdmin, true
	case resources.CallerRoleOperationInspector:
		return auth.RoleOperationInspector, true
	default:
		return "", false
	}
}
