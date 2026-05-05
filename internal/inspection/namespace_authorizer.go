package inspection

import (
	"context"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type NamespaceVolumeBindingReader interface {
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
}

type NamespaceVolumeBindingAuthorizer struct {
	Reader NamespaceVolumeBindingReader
}

func (authorizer NamespaceVolumeBindingAuthorizer) AllowsOperationInspection(ctx context.Context, namespaceID string, caller auth.AllowedCaller) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if authorizer.Reader == nil {
		return false
	}
	if caller.Kind != auth.CallerKindProduct {
		return false
	}

	namespaceID = strings.TrimSpace(namespaceID)
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		return false
	}

	binding, err := authorizer.Reader.GetNamespaceVolumeBinding(ctx, namespaceID)
	if err != nil {
		return false
	}
	if strings.TrimSpace(binding.NamespaceID) != namespaceID {
		return false
	}
	if binding.Status != resources.NamespaceStatusActive {
		return false
	}

	for _, storedCaller := range binding.AllowedCallers {
		mapped, ok := mapNamespaceAllowedCaller(storedCaller)
		if !ok {
			continue
		}
		if !auth.CallerNotAllowed(caller.CallerService, auth.RoleOperationInspector, []auth.AllowedCaller{mapped}) {
			return true
		}
	}

	return false
}

func mapNamespaceAllowedCaller(caller resources.AllowedCaller) (auth.AllowedCaller, bool) {
	roles := make([]auth.Role, 0, len(caller.Roles))
	for _, role := range caller.Roles {
		mapped, ok := mapNamespaceCallerRole(role)
		if !ok {
			return auth.AllowedCaller{}, false
		}
		roles = append(roles, mapped)
	}
	if len(roles) == 0 {
		return auth.AllowedCaller{}, false
	}
	return auth.AllowedCaller{
		CallerService: caller.CallerService,
		Kind:          auth.CallerKindProduct,
		Roles:         roles,
	}, true
}

func mapNamespaceCallerRole(role resources.CallerRole) (auth.Role, bool) {
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
