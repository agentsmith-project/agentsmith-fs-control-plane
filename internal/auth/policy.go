package auth

type CallerKind string

const (
	CallerKindProduct      CallerKind = "product"
	CallerKindAdmin        CallerKind = "admin"
	CallerKindOperator     CallerKind = "operator"
	CallerKindMigration    CallerKind = "migration"
	CallerKindOrchestrator CallerKind = "orchestrator"
)

type Role string

const (
	RoleVolumeAdmin        Role = "volume_admin"
	RoleNamespaceAdmin     Role = "namespace_admin"
	RoleRepoAdmin          Role = "repo_admin"
	RoleRepoLifecycleAdmin Role = "repo_lifecycle_admin"
	RoleRestoreAdmin       Role = "restore_admin"
	RoleTemplateAdmin      Role = "template_admin"
	RoleExportAdmin        Role = "export_admin"
	RoleMountAdmin         Role = "mount_admin"
	RoleOperationInspector Role = "operation_inspector"
	RoleOrchestratorMount  Role = "orchestrator_mount"
	RoleMigrationAdmin     Role = "migration_admin"
	RoleOperatorAdmin      Role = "operator_admin"
	RoleBreakGlassAdmin    Role = "break_glass_admin"
)

var allCallerRoles = []Role{
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

func CallerRoles() []Role {
	roles := make([]Role, len(allCallerRoles))
	copy(roles, allCallerRoles)
	return roles
}

type AllowedCaller struct {
	CallerService string
	Kind          CallerKind
	Roles         []Role
}

type CallerRoleDenialReason string

const (
	CallerRoleAllowed       CallerRoleDenialReason = ""
	CallerServiceNotAllowed CallerRoleDenialReason = "caller_not_allowed"
	CallerRoleNotAllowed    CallerRoleDenialReason = "role_not_allowed"
)

func NamespaceMismatch(requestNamespaceID, resourceNamespaceID string) bool {
	requestNamespaceID = normalize(requestNamespaceID)
	resourceNamespaceID = normalize(resourceNamespaceID)

	if requestNamespaceID == "" && resourceNamespaceID == "" {
		return false
	}

	return requestNamespaceID != resourceNamespaceID
}

func NamespaceBoundMismatch(requestNamespaceID, resourceNamespaceID string) bool {
	requestNamespaceID = normalize(requestNamespaceID)
	resourceNamespaceID = normalize(resourceNamespaceID)

	if requestNamespaceID == "" || resourceNamespaceID == "" {
		return true
	}

	return requestNamespaceID != resourceNamespaceID
}

func CallerNotAllowed(callerService string, requiredRole Role, allowedCallers []AllowedCaller) bool {
	return CallerRoleDenialReasonFor(callerService, requiredRole, allowedCallers) != CallerRoleAllowed
}

func CallerRoleDenialReasonFor(callerService string, requiredRole Role, allowedCallers []AllowedCaller) CallerRoleDenialReason {
	callerService = normalize(callerService)
	if callerService == "" {
		return CallerServiceNotAllowed
	}

	callerFound := false
	for _, allowed := range allowedCallers {
		if normalize(allowed.CallerService) != callerService {
			continue
		}
		callerFound = true
		if allowed.hasRole(requiredRole) {
			return CallerRoleAllowed
		}
	}
	if callerFound {
		return CallerRoleNotAllowed
	}

	return CallerServiceNotAllowed
}

func (c AllowedCaller) hasRole(required Role) bool {
	for _, role := range c.Roles {
		if roleSatisfiesRequiredRole(c.Kind, role, required) {
			return true
		}
	}

	return false
}

func roleSatisfiesRequiredRole(kind CallerKind, granted Role, required Role) bool {
	if granted == required && kindCanUseRole(kind, required) {
		return true
	}
	if required == RoleOperationInspector && granted == RoleOperatorAdmin {
		return kind == CallerKindAdmin || kind == CallerKindOperator
	}
	return false
}

func kindCanUseRole(kind CallerKind, role Role) bool {
	switch kind {
	case CallerKindProduct:
		return isProductControlPlaneRole(role)
	case CallerKindOrchestrator:
		return role == RoleOrchestratorMount
	case CallerKindMigration:
		return role == RoleMigrationAdmin
	case CallerKindAdmin, CallerKindOperator:
		return isProductControlPlaneRole(role) || isAdminOperatorRole(role)
	default:
		return false
	}
}

func isProductControlPlaneRole(role Role) bool {
	switch role {
	case RoleRepoAdmin,
		RoleRepoLifecycleAdmin,
		RoleRestoreAdmin,
		RoleTemplateAdmin,
		RoleExportAdmin,
		RoleMountAdmin,
		RoleOperationInspector,
		RoleNamespaceAdmin:
		return true
	default:
		return false
	}
}

func isAdminOperatorRole(role Role) bool {
	switch role {
	case RoleVolumeAdmin,
		RoleOperatorAdmin,
		RoleBreakGlassAdmin:
		return true
	default:
		return false
	}
}
