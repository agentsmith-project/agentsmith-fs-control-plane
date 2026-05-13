package resources

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

type VolumeBackend string

const VolumeBackendJuiceFS VolumeBackend = "juicefs"

func (backend VolumeBackend) valid() bool {
	return backend == VolumeBackendJuiceFS
}

type VolumeIsolationClass string

const (
	VolumeIsolationShared    VolumeIsolationClass = "shared"
	VolumeIsolationDedicated VolumeIsolationClass = "dedicated"
)

func (class VolumeIsolationClass) valid() bool {
	switch class {
	case VolumeIsolationShared, VolumeIsolationDedicated:
		return true
	default:
		return false
	}
}

type VolumeStatus string

const (
	VolumeStatusActive   VolumeStatus = "active"
	VolumeStatusDisabled VolumeStatus = "disabled"
	VolumeStatusDegraded VolumeStatus = "degraded"
)

func (status VolumeStatus) valid() bool {
	switch status {
	case VolumeStatusActive, VolumeStatusDisabled, VolumeStatusDegraded:
		return true
	default:
		return false
	}
}

type NamespaceStatus string

const (
	NamespaceStatusActive   NamespaceStatus = "active"
	NamespaceStatusDisabled NamespaceStatus = "disabled"
)

func (status NamespaceStatus) valid() bool {
	switch status {
	case NamespaceStatusActive, NamespaceStatusDisabled:
		return true
	default:
		return false
	}
}

type CallerRole string

const (
	CallerRoleVolumeAdmin        CallerRole = "volume_admin"
	CallerRoleNamespaceAdmin     CallerRole = "namespace_admin"
	CallerRoleRepoAdmin          CallerRole = "repo_admin"
	CallerRoleRepoLifecycleAdmin CallerRole = "repo_lifecycle_admin"
	CallerRoleRestoreAdmin       CallerRole = "restore_admin"
	CallerRoleTemplateAdmin      CallerRole = "template_admin"
	CallerRoleExportAdmin        CallerRole = "export_admin"
	CallerRoleMountAdmin         CallerRole = "mount_admin"
	CallerRoleOperationInspector CallerRole = "operation_inspector"
	CallerRoleOrchestratorMount  CallerRole = "orchestrator_mount"
	CallerRoleMigrationAdmin     CallerRole = "migration_admin"
	CallerRoleOperatorAdmin      CallerRole = "operator_admin"
	CallerRoleBreakGlassAdmin    CallerRole = "break_glass_admin"
)

func (role CallerRole) valid() bool {
	switch role {
	case CallerRoleVolumeAdmin,
		CallerRoleNamespaceAdmin,
		CallerRoleRepoAdmin,
		CallerRoleRepoLifecycleAdmin,
		CallerRoleRestoreAdmin,
		CallerRoleTemplateAdmin,
		CallerRoleExportAdmin,
		CallerRoleMountAdmin,
		CallerRoleOperationInspector,
		CallerRoleOrchestratorMount,
		CallerRoleMigrationAdmin,
		CallerRoleOperatorAdmin,
		CallerRoleBreakGlassAdmin:
		return true
	default:
		return false
	}
}

type RepoKind string

const (
	// RepoKindRepo records ordinary repo storage identity using repo_ IDs,
	// canonical repo paths, and repo lifecycle status metadata.
	RepoKindRepo RepoKind = "repo"

	// RepoKindTemplate records internal template storage identity using tmpl_
	// IDs and canonical template paths. It is not the API RepoTemplate
	// publication lifecycle and does not grant archive/delete/purge semantics
	// to templates.
	RepoKindTemplate RepoKind = "template"
)

func (kind RepoKind) valid() bool {
	switch kind {
	case RepoKindRepo, RepoKindTemplate:
		return true
	default:
		return false
	}
}

type RepoStatus string

const (
	RepoStatusActive                       RepoStatus = "active"
	RepoStatusArchiving                    RepoStatus = "archiving"
	RepoStatusArchived                     RepoStatus = "archived"
	RepoStatusRestoringArchived            RepoStatus = "restoring_archived"
	RepoStatusDeleting                     RepoStatus = "deleting"
	RepoStatusTombstoned                   RepoStatus = "tombstoned"
	RepoStatusRestoringTombstoned          RepoStatus = "restoring_tombstoned"
	RepoStatusPurging                      RepoStatus = "purging"
	RepoStatusPurged                       RepoStatus = "purged"
	RepoStatusOperatorInterventionRequired RepoStatus = "operator_intervention_required"
)

func (status RepoStatus) valid() bool {
	switch status {
	case RepoStatusActive,
		RepoStatusArchiving,
		RepoStatusArchived,
		RepoStatusRestoringArchived,
		RepoStatusDeleting,
		RepoStatusTombstoned,
		RepoStatusRestoringTombstoned,
		RepoStatusPurging,
		RepoStatusPurged,
		RepoStatusOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

type Volume struct {
	ID             string
	Backend        VolumeBackend
	IsolationClass VolumeIsolationClass
	Status         VolumeStatus
	Capabilities   map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (volume Volume) Validate() error {
	if err := pathresolver.ValidateID(pathresolver.VolumeID, volume.ID); err != nil {
		return err
	}
	if !volume.Backend.valid() {
		return fmt.Errorf("unknown volume backend %q", volume.Backend)
	}
	if !volume.IsolationClass.valid() {
		return fmt.Errorf("unknown volume isolation class %q", volume.IsolationClass)
	}
	if !volume.Status.valid() {
		return fmt.Errorf("unknown volume status %q", volume.Status)
	}
	if err := validateObject("capabilities", volume.Capabilities); err != nil {
		return err
	}
	if err := rejectSensitiveKeys("capabilities", volume.Capabilities); err != nil {
		return err
	}
	if err := validateVolumeCapabilities(volume.Capabilities); err != nil {
		return err
	}
	return validateTimestamps(volume.CreatedAt, volume.UpdatedAt)
}

type Namespace struct {
	ID             string
	Status         NamespaceStatus
	DisabledReason string
	DisabledAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (namespace Namespace) Validate() error {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespace.ID); err != nil {
		return err
	}
	if !namespace.Status.valid() {
		return fmt.Errorf("unknown namespace status %q", namespace.Status)
	}
	switch namespace.Status {
	case NamespaceStatusActive:
		if namespace.DisabledAt != nil {
			return fmt.Errorf("active namespace must not have disabled_at")
		}
	case NamespaceStatusDisabled:
		if namespace.DisabledAt == nil {
			return fmt.Errorf("disabled namespace must have disabled_at")
		}
		if strings.TrimSpace(namespace.DisabledReason) == "" {
			return fmt.Errorf("disabled namespace must have disabled_reason")
		}
	}
	return validateTimestamps(namespace.CreatedAt, namespace.UpdatedAt)
}

type AllowedCaller struct {
	CallerService string       `json:"caller_service"`
	Roles         []CallerRole `json:"roles"`
}

var namespaceBindingCallerRoles = []CallerRole{
	CallerRoleNamespaceAdmin,
	CallerRoleRepoAdmin,
	CallerRoleRepoLifecycleAdmin,
	CallerRoleRestoreAdmin,
	CallerRoleTemplateAdmin,
	CallerRoleExportAdmin,
	CallerRoleMountAdmin,
	CallerRoleOperationInspector,
	CallerRoleOrchestratorMount,
	CallerRoleMigrationAdmin,
}

func NamespaceBindingCallerRoles() []CallerRole {
	roles := make([]CallerRole, len(namespaceBindingCallerRoles))
	copy(roles, namespaceBindingCallerRoles)
	return roles
}

func (caller AllowedCaller) Validate() error {
	if strings.TrimSpace(caller.CallerService) == "" {
		return fmt.Errorf("allowed caller missing caller_service")
	}
	if len(caller.Roles) == 0 {
		return fmt.Errorf("allowed caller %q missing roles", caller.CallerService)
	}
	seen := map[CallerRole]bool{}
	hasOrchestratorMount := false
	hasMigrationAdmin := false
	for _, role := range caller.Roles {
		if !role.valid() {
			return fmt.Errorf("unknown caller role %q", role)
		}
		switch role {
		case CallerRoleOperatorAdmin, CallerRoleBreakGlassAdmin, CallerRoleVolumeAdmin:
			return fmt.Errorf("%s is deployment/operator policy, not an ordinary namespace role", role)
		case CallerRoleOrchestratorMount:
			hasOrchestratorMount = true
		case CallerRoleMigrationAdmin:
			hasMigrationAdmin = true
		}
		if seen[role] {
			return fmt.Errorf("duplicate caller role %q", role)
		}
		seen[role] = true
	}
	if hasOrchestratorMount && len(caller.Roles) != 1 {
		return fmt.Errorf("orchestrator_mount must be a dedicated caller role")
	}
	if hasMigrationAdmin && len(caller.Roles) != 1 {
		return fmt.Errorf("migration_admin must be a dedicated caller role")
	}
	return nil
}

type NamespaceVolumeBinding struct {
	NamespaceID       string
	DefaultVolumeID   string
	AllowedCallers    []AllowedCaller
	QuotaBytesDefault int64
	ExportPolicy      map[string]any
	LifecyclePolicy   map[string]any
	MountPolicy       map[string]any
	TemplatePolicy    map[string]any
	Status            NamespaceStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (binding NamespaceVolumeBinding) Validate() error {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, binding.NamespaceID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.VolumeID, binding.DefaultVolumeID); err != nil {
		return err
	}
	if binding.QuotaBytesDefault < 0 {
		return fmt.Errorf("quota_bytes_default must be non-negative")
	}
	if !binding.Status.valid() {
		return fmt.Errorf("unknown namespace binding status %q", binding.Status)
	}
	if len(binding.AllowedCallers) == 0 {
		return fmt.Errorf("allowed_callers must not be empty")
	}
	seenCallerServices := map[string]bool{}
	for _, caller := range binding.AllowedCallers {
		if err := caller.Validate(); err != nil {
			return err
		}
		callerService := strings.TrimSpace(caller.CallerService)
		if seenCallerServices[callerService] {
			return fmt.Errorf("duplicate allowed caller_service %q", callerService)
		}
		seenCallerServices[callerService] = true
	}
	if err := validateObject("export_policy", binding.ExportPolicy); err != nil {
		return err
	}
	if err := validateExportPolicy(binding.ExportPolicy); err != nil {
		return err
	}
	if err := validateObject("lifecycle_policy", binding.LifecyclePolicy); err != nil {
		return err
	}
	if err := validateLifecyclePolicy(binding.LifecyclePolicy); err != nil {
		return err
	}
	if err := validateObject("mount_policy", binding.MountPolicy); err != nil {
		return err
	}
	if err := validateMountPolicy(binding.MountPolicy); err != nil {
		return err
	}
	if err := validateObject("template_policy", binding.TemplatePolicy); err != nil {
		return err
	}
	if err := validateTemplatePolicy(binding.TemplatePolicy); err != nil {
		return err
	}
	return validateTimestamps(binding.CreatedAt, binding.UpdatedAt)
}

type RepoLifecycle struct {
	Status                   RepoStatus
	RetentionExpiresAt       *time.Time
	LastLifecycleOperationID string
	PreDeleteStatus          RepoStatus
}

func (lifecycle RepoLifecycle) Validate() error {
	if !lifecycle.Status.valid() {
		return fmt.Errorf("unknown repo lifecycle status %q", lifecycle.Status)
	}
	if lifecycle.LastLifecycleOperationID != "" {
		if err := pathresolver.ValidateID(pathresolver.OperationID, lifecycle.LastLifecycleOperationID); err != nil {
			return err
		}
	}
	if lifecycle.PreDeleteStatus != "" && lifecycle.PreDeleteStatus != RepoStatusActive && lifecycle.PreDeleteStatus != RepoStatusArchived {
		return fmt.Errorf("unknown pre-delete repo status %q", lifecycle.PreDeleteStatus)
	}
	if lifecycle.statusRequiresPreDelete() {
		if lifecycle.PreDeleteStatus == "" {
			return fmt.Errorf("repo lifecycle status %q requires pre_delete_status", lifecycle.Status)
		}
	} else if lifecycle.Status != RepoStatusOperatorInterventionRequired && lifecycle.PreDeleteStatus != "" {
		return fmt.Errorf("repo lifecycle status %q must not have pre_delete_status", lifecycle.Status)
	}
	if lifecycle.statusRequiresRetention() {
		if lifecycle.RetentionExpiresAt == nil {
			return fmt.Errorf("repo lifecycle status %q requires retention_expires_at", lifecycle.Status)
		}
	} else if lifecycle.statusForbidsRetention() && lifecycle.RetentionExpiresAt != nil {
		return fmt.Errorf("repo lifecycle status %q must not have retention_expires_at", lifecycle.Status)
	}
	return nil
}

func (lifecycle RepoLifecycle) statusRequiresPreDelete() bool {
	switch lifecycle.Status {
	case RepoStatusDeleting, RepoStatusTombstoned, RepoStatusRestoringTombstoned, RepoStatusPurging, RepoStatusPurged:
		return true
	default:
		return false
	}
}

func (lifecycle RepoLifecycle) statusRequiresRetention() bool {
	switch lifecycle.Status {
	case RepoStatusTombstoned, RepoStatusRestoringTombstoned, RepoStatusPurging:
		return true
	default:
		return false
	}
}

func (lifecycle RepoLifecycle) statusForbidsRetention() bool {
	switch lifecycle.Status {
	case RepoStatusActive, RepoStatusArchiving, RepoStatusArchived, RepoStatusRestoringArchived:
		return true
	default:
		return false
	}
}

type Repo struct {
	ID                  string
	NamespaceID         string
	VolumeID            string
	JVSRepoID           string
	Kind                RepoKind
	Status              RepoStatus
	ControlVolumeSubdir string
	PayloadVolumeSubdir string
	Lifecycle           RepoLifecycle
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (repo Repo) Validate() error {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, repo.NamespaceID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.VolumeID, repo.VolumeID); err != nil {
		return err
	}
	if err := ValidateJVSRepoID(repo.JVSRepoID); err != nil {
		return err
	}
	if !repo.Kind.valid() {
		return fmt.Errorf("unknown repo kind %q", repo.Kind)
	}
	if err := repo.validateCanonicalPathIdentity(); err != nil {
		return err
	}
	if !repo.Status.valid() {
		return fmt.Errorf("unknown repo status %q", repo.Status)
	}
	if err := repo.Lifecycle.Validate(); err != nil {
		return err
	}
	if repo.Status != repo.Lifecycle.Status {
		return fmt.Errorf("repo status %q must match lifecycle status %q", repo.Status, repo.Lifecycle.Status)
	}
	return validateTimestamps(repo.CreatedAt, repo.UpdatedAt)
}

func (repo Repo) validateCanonicalPathIdentity() error {
	switch repo.Kind {
	case RepoKindRepo:
		paths, err := pathresolver.ResolveRepoPaths(repo.NamespaceID, repo.ID)
		if err != nil {
			return err
		}
		if repo.ControlVolumeSubdir != paths.ControlVolumeSubdir {
			return fmt.Errorf("control_volume_subdir must equal canonical repo control subdir")
		}
		if repo.PayloadVolumeSubdir != paths.PayloadVolumeSubdir {
			return fmt.Errorf("payload_volume_subdir must equal canonical repo payload subdir")
		}
	case RepoKindTemplate:
		paths, err := pathresolver.ResolveTemplatePaths(repo.NamespaceID, repo.ID)
		if err != nil {
			return err
		}
		if repo.ControlVolumeSubdir != paths.ControlVolumeSubdir {
			return fmt.Errorf("control_volume_subdir must equal canonical template control subdir")
		}
		if repo.PayloadVolumeSubdir != paths.PayloadVolumeSubdir {
			return fmt.Errorf("payload_volume_subdir must equal canonical template payload subdir")
		}
	default:
		return fmt.Errorf("unknown repo kind %q", repo.Kind)
	}
	return nil
}

func ValidateJVSRepoID(id string) error {
	if id == "" {
		return fmt.Errorf("missing jvs_repo_id")
	}
	if len(id) > 128 {
		return fmt.Errorf("invalid jvs_repo_id")
	}
	for idx, r := range id {
		if idx == 0 {
			if !asciiAlphaNum(r) {
				return fmt.Errorf("invalid jvs_repo_id")
			}
			continue
		}
		if !asciiAlphaNum(r) && r != '_' && r != '.' && r != ':' && r != '-' {
			return fmt.Errorf("invalid jvs_repo_id")
		}
	}
	return nil
}

func asciiAlphaNum(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func validateObject(name string, object map[string]any) error {
	if object == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	return nil
}

func validateVolumeCapabilities(capabilities map[string]any) error {
	allowed := map[string]bool{
		"webdav_export":             true,
		"workload_mount":            true,
		"jvs_external_control_root": true,
		"directory_quota":           true,
		"filtered_mount":            true,
		"csi_driver":                true,
		"storage_class":             true,
		"permission_model":          true,
	}
	if err := rejectUnknownKeys("capabilities", capabilities, allowed); err != nil {
		return err
	}
	for _, key := range []string{"webdav_export", "workload_mount", "jvs_external_control_root", "directory_quota"} {
		if err := requireBool("capabilities", capabilities, key); err != nil {
			return err
		}
	}
	if err := optionalBool("capabilities", capabilities, "filtered_mount"); err != nil {
		return err
	}
	for _, key := range []string{"csi_driver", "storage_class", "permission_model"} {
		if err := optionalString("capabilities", capabilities, key); err != nil {
			return err
		}
	}
	return nil
}

func validateExportPolicy(policy map[string]any) error {
	allowed := map[string]bool{"webdav_enabled": true, "max_session_seconds": true}
	if err := rejectUnknownKeys("export_policy", policy, allowed); err != nil {
		return err
	}
	if err := requireBool("export_policy", policy, "webdav_enabled"); err != nil {
		return err
	}
	seconds, err := requireNumber("export_policy", policy, "max_session_seconds")
	if err != nil {
		return err
	}
	if seconds < 60 {
		return fmt.Errorf("export_policy.max_session_seconds must be at least 60")
	}
	return nil
}

func validateLifecyclePolicy(policy map[string]any) error {
	allowed := map[string]bool{
		"tombstone_retention_seconds":    true,
		"purge_requires_lifecycle_admin": true,
		"break_glass_purge_enabled":      true,
	}
	if err := rejectUnknownKeys("lifecycle_policy", policy, allowed); err != nil {
		return err
	}
	seconds, err := requireNumber("lifecycle_policy", policy, "tombstone_retention_seconds")
	if err != nil {
		return err
	}
	if seconds < 0 {
		return fmt.Errorf("lifecycle_policy.tombstone_retention_seconds must be non-negative")
	}
	if err := requireBool("lifecycle_policy", policy, "purge_requires_lifecycle_admin"); err != nil {
		return err
	}
	if err := requireBool("lifecycle_policy", policy, "break_glass_purge_enabled"); err != nil {
		return err
	}
	return nil
}

func validateMountPolicy(policy map[string]any) error {
	allowed := map[string]bool{
		"workload_mount_enabled":                        true,
		"workload_mount_requires_external_control_root": true,
		"allow_privileged_workload":                     true,
	}
	if err := rejectUnknownKeys("mount_policy", policy, allowed); err != nil {
		return err
	}
	for _, key := range []string{"workload_mount_enabled", "workload_mount_requires_external_control_root", "allow_privileged_workload"} {
		if err := requireBool("mount_policy", policy, key); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplatePolicy(policy map[string]any) error {
	allowed := map[string]bool{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": true}
	if err := rejectUnknownKeys("template_policy", policy, allowed); err != nil {
		return err
	}
	if err := requireBool("template_policy", policy, "namespace_templates_enabled"); err != nil {
		return err
	}
	if err := requireBool("template_policy", policy, "cross_namespace_clone_enabled"); err != nil {
		return err
	}
	return nil
}

func rejectUnknownKeys(name string, object map[string]any, allowed map[string]bool) error {
	for key := range object {
		if !allowed[key] {
			return fmt.Errorf("%s.%s is not a supported field", name, key)
		}
	}
	return nil
}

func requireBool(name string, object map[string]any, key string) error {
	value, ok := object[key]
	if !ok {
		return fmt.Errorf("%s.%s is required", name, key)
	}
	if _, ok := value.(bool); !ok {
		return fmt.Errorf("%s.%s must be boolean", name, key)
	}
	return nil
}

func optionalBool(name string, object map[string]any, key string) error {
	value, ok := object[key]
	if !ok {
		return nil
	}
	if _, ok := value.(bool); !ok {
		return fmt.Errorf("%s.%s must be boolean", name, key)
	}
	return nil
}

func optionalString(name string, object map[string]any, key string) error {
	value, ok := object[key]
	if !ok {
		return nil
	}
	if _, ok := value.(string); !ok {
		return fmt.Errorf("%s.%s must be string", name, key)
	}
	return nil
}

func requireNumber(name string, object map[string]any, key string) (float64, error) {
	value, ok := object[key]
	if !ok {
		return 0, fmt.Errorf("%s.%s is required", name, key)
	}
	switch typed := value.(type) {
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case float64:
		return typed, nil
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("%s.%s must be numeric", name, key)
		}
		return number, nil
	default:
		return 0, fmt.Errorf("%s.%s must be numeric", name, key)
	}
}

func rejectSensitiveKeys(path string, object map[string]any) error {
	for key, value := range object {
		lower := strings.ToLower(key)
		for _, forbidden := range []string{"credential", "secret", "token", "password", "private_key", "metadata_url", "raw_path"} {
			if strings.Contains(lower, forbidden) {
				return fmt.Errorf("%s.%s contains sensitive material", path, key)
			}
		}
		nested, ok := value.(map[string]any)
		if ok {
			if err := rejectSensitiveKeys(path+"."+key, nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTimestamps(createdAt, updatedAt time.Time) error {
	if createdAt.IsZero() {
		return fmt.Errorf("missing created_at")
	}
	if updatedAt.IsZero() {
		return fmt.Errorf("missing updated_at")
	}
	return nil
}
