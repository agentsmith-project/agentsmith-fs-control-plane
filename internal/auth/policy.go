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

type AllowedCaller struct {
	CallerService string
	Kind          CallerKind
	Roles         []Role
}

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
	callerService = normalize(callerService)
	if callerService == "" {
		return true
	}

	for _, allowed := range allowedCallers {
		if normalize(allowed.CallerService) != callerService {
			continue
		}
		if allowed.hasRole(requiredRole) {
			return false
		}
	}

	return true
}

func (c AllowedCaller) hasRole(required Role) bool {
	if !kindCanUseRole(c.Kind, required) {
		return false
	}

	for _, role := range c.Roles {
		if role == required {
			return true
		}
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
