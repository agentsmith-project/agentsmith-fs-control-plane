package api

import (
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type VolumeResponse struct {
	VolumeID       string                         `json:"volume_id"`
	Backend        resources.VolumeBackend        `json:"backend"`
	IsolationClass resources.VolumeIsolationClass `json:"isolation_class"`
	Status         resources.VolumeStatus         `json:"status"`
	Capabilities   map[string]any                 `json:"capabilities"`
}

type NamespaceResponse struct {
	NamespaceID    string                    `json:"namespace_id"`
	Status         resources.NamespaceStatus `json:"status"`
	DisabledReason *string                   `json:"disabled_reason"`
	CreatedAt      time.Time                 `json:"created_at"`
	DisabledAt     *time.Time                `json:"disabled_at"`
}

type NamespaceVolumeBindingResponse struct {
	NamespaceID       string                    `json:"namespace_id"`
	DefaultVolumeID   string                    `json:"default_volume_id"`
	AllowedCallers    []AllowedCallerResponse   `json:"allowed_callers"`
	QuotaBytesDefault int64                     `json:"quota_bytes_default"`
	ExportPolicy      map[string]any            `json:"export_policy"`
	LifecyclePolicy   map[string]any            `json:"lifecycle_policy"`
	MountPolicy       map[string]any            `json:"mount_policy"`
	TemplatePolicy    map[string]any            `json:"template_policy"`
	Status            resources.NamespaceStatus `json:"status"`
}

type AllowedCallerResponse struct {
	CallerService string                 `json:"caller_service"`
	Roles         []resources.CallerRole `json:"roles"`
}

func VolumeResponseFromResource(volume resources.Volume) VolumeResponse {
	return VolumeResponse{
		VolumeID:       volume.ID,
		Backend:        volume.Backend,
		IsolationClass: volume.IsolationClass,
		Status:         volume.Status,
		Capabilities:   projectStringAnyMap(volume.Capabilities, volumeCapabilityKeys),
	}
}

func NamespaceResponseFromResource(namespace resources.Namespace) NamespaceResponse {
	var disabledReason *string
	if namespace.DisabledReason != "" {
		reason := namespace.DisabledReason
		disabledReason = &reason
	}
	return NamespaceResponse{
		NamespaceID:    namespace.ID,
		Status:         namespace.Status,
		DisabledReason: disabledReason,
		CreatedAt:      namespace.CreatedAt,
		DisabledAt:     copyTimePtr(namespace.DisabledAt),
	}
}

func NamespaceVolumeBindingResponseFromResource(binding resources.NamespaceVolumeBinding) NamespaceVolumeBindingResponse {
	return NamespaceVolumeBindingResponse{
		NamespaceID:       binding.NamespaceID,
		DefaultVolumeID:   binding.DefaultVolumeID,
		AllowedCallers:    allowedCallerResponsesFromResources(binding.AllowedCallers),
		QuotaBytesDefault: binding.QuotaBytesDefault,
		ExportPolicy:      projectStringAnyMap(binding.ExportPolicy, exportPolicyKeys),
		LifecyclePolicy:   projectStringAnyMap(binding.LifecyclePolicy, lifecyclePolicyKeys),
		MountPolicy:       projectStringAnyMap(binding.MountPolicy, mountPolicyKeys),
		TemplatePolicy:    projectStringAnyMap(binding.TemplatePolicy, templatePolicyKeys),
		Status:            binding.Status,
	}
}

func allowedCallerResponsesFromResources(callers []resources.AllowedCaller) []AllowedCallerResponse {
	if callers == nil {
		return nil
	}
	out := make([]AllowedCallerResponse, len(callers))
	for idx, caller := range callers {
		out[idx] = AllowedCallerResponse{
			CallerService: caller.CallerService,
			Roles:         copyCallerRoles(caller.Roles),
		}
	}
	return out
}

func copyCallerRoles(roles []resources.CallerRole) []resources.CallerRole {
	if roles == nil {
		return nil
	}
	out := make([]resources.CallerRole, len(roles))
	copy(out, roles)
	return out
}

var (
	volumeCapabilityKeys = []string{
		"webdav_export",
		"workload_mount",
		"jvs_external_control_root",
		"directory_quota",
		"filtered_mount",
		"csi_driver",
		"storage_class",
		"permission_model",
	}
	exportPolicyKeys    = []string{"webdav_enabled", "max_session_seconds"}
	lifecyclePolicyKeys = []string{"tombstone_retention_seconds", "purge_requires_lifecycle_admin", "break_glass_purge_enabled"}
	mountPolicyKeys     = []string{"workload_mount_enabled", "workload_mount_requires_jvs_external_control_root", "allow_privileged_workload"}
	templatePolicyKeys  = []string{"namespace_templates_enabled", "cross_namespace_clone_enabled"}
)

func projectStringAnyMap(in map[string]any, allowedKeys []string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(allowedKeys))
	for _, key := range allowedKeys {
		if value, ok := in[key]; ok {
			out[key] = value
		}
	}
	return out
}

func copyTimePtr(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
