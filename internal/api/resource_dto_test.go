package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestVolumeResponseJSONShapeAndDefensiveCopy(t *testing.T) {
	now := resourceDTOTestNow()
	volume := resources.Volume{
		ID:             "vol_123",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities: map[string]any{
			"webdav_export":             true,
			"workload_mount":            true,
			"jvs_external_control_root": true,
			"directory_quota":           false,
			"quota_enforced":            true,
			"filtered_mount":            true,
			"csi_driver":                "csi.juicefs.example",
			"credential_ref":            "secret-capability-ref",
		},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}

	dto := VolumeResponseFromResource(volume)
	volume.Capabilities["workload_mount"] = false
	volume.Capabilities["credential_ref"] = "mutated-secret"
	body := mustMarshalJSON(t, dto)
	assertJSONDoesNotLeakFields(t, body, "CreatedAt", "UpdatedAt", "created_at", "updated_at", "ID", "Backend", "credential_ref", "secret-capability-ref")

	var got map[string]any
	mustUnmarshalJSON(t, body, &got)
	wantKeys := []string{"volume_id", "backend", "isolation_class", "status", "capabilities"}
	assertOnlyJSONKeys(t, got, wantKeys...)
	if got["volume_id"] != "vol_123" || got["backend"] != "juicefs" || got["isolation_class"] != "shared" || got["status"] != "active" {
		t.Fatalf("volume JSON = %#v, want schema field values", got)
	}
	capabilities := got["capabilities"].(map[string]any)
	assertMapHasKeys(t, capabilities, "webdav_export", "workload_mount", "jvs_external_control_root", "directory_quota", "filtered_mount", "csi_driver")
	assertMapLacksKeys(t, capabilities, "credential_ref", "quota_enforced")
	if capabilities["workload_mount"] != true {
		t.Fatalf("capabilities = %#v, want defensive copy before source mutation", capabilities)
	}
}

func TestNamespaceResponseJSONShapeActiveDisabledFieldsAndNoUpdatedAt(t *testing.T) {
	now := resourceDTOTestNow()
	namespace := resources.Namespace{
		ID:        "ns_123",
		Status:    resources.NamespaceStatusActive,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}

	body := mustMarshalJSON(t, NamespaceResponseFromResource(namespace))
	assertJSONDoesNotLeakFields(t, body, "UpdatedAt", "updated_at", "ID", "Status")

	var got map[string]any
	mustUnmarshalJSON(t, body, &got)
	assertOnlyJSONKeys(t, got, "namespace_id", "status", "disabled_reason", "created_at", "disabled_at")
	if got["namespace_id"] != "ns_123" || got["status"] != "active" {
		t.Fatalf("namespace JSON = %#v, want active namespace values", got)
	}
	if got["disabled_reason"] != nil || got["disabled_at"] != nil {
		t.Fatalf("active disabled fields = %v/%v, want null/null", got["disabled_reason"], got["disabled_at"])
	}
	if got["created_at"] == "" {
		t.Fatalf("created_at = %#v, want timestamp", got["created_at"])
	}
}

func TestNamespaceResponseJSONShapeDisabledFields(t *testing.T) {
	now := resourceDTOTestNow()
	disabledAt := now.Add(time.Minute)
	namespace := resources.Namespace{
		ID:             "ns_123",
		Status:         resources.NamespaceStatusDisabled,
		DisabledReason: "tenant requested",
		DisabledAt:     &disabledAt,
		CreatedAt:      now,
		UpdatedAt:      now.Add(2 * time.Minute),
	}

	body := mustMarshalJSON(t, NamespaceResponseFromResource(namespace))
	var got map[string]any
	mustUnmarshalJSON(t, body, &got)
	if got["disabled_reason"] != "tenant requested" || got["disabled_at"] == nil {
		t.Fatalf("disabled fields = %#v/%#v, want populated fields", got["disabled_reason"], got["disabled_at"])
	}
}

func TestNamespaceVolumeBindingResponseJSONShapeAllowedCallersAndPolicyCopies(t *testing.T) {
	now := resourceDTOTestNow()
	binding := resources.NamespaceVolumeBinding{
		NamespaceID:     "ns_123",
		DefaultVolumeID: "vol_123",
		AllowedCallers: []resources.AllowedCaller{{
			CallerService: "product-caller",
			Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleOperationInspector},
		}},
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600), "quota_enforced": true, "credential_ref": "secret-export-ref"},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false, "secret_path": "/secret/lifecycle"},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false, "credential_ref": "secret-mount-ref"},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false, "secret_path": "/secret/template"},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now.Add(time.Minute),
	}

	dto := NamespaceVolumeBindingResponseFromResource(binding)
	binding.AllowedCallers[0].Roles[0] = resources.CallerRoleMountAdmin
	binding.ExportPolicy["webdav_enabled"] = false
	binding.LifecyclePolicy["purge_requires_lifecycle_admin"] = false
	binding.MountPolicy["workload_mount_enabled"] = false
	binding.TemplatePolicy["cross_namespace_clone_enabled"] = true
	body := mustMarshalJSON(t, dto)
	assertJSONDoesNotLeakFields(t, body, "CreatedAt", "UpdatedAt", "created_at", "updated_at", "NamespaceID", "DefaultVolumeID", "quota_enforced", "credential_ref", "secret_path", "secret-export-ref", "secret-mount-ref", "/secret/lifecycle", "/secret/template")

	var got map[string]any
	mustUnmarshalJSON(t, body, &got)
	assertOnlyJSONKeys(t, got,
		"namespace_id",
		"default_volume_id",
		"allowed_callers",
		"quota_bytes_default",
		"export_policy",
		"lifecycle_policy",
		"mount_policy",
		"template_policy",
		"status",
	)
	callers := got["allowed_callers"].([]any)
	caller := callers[0].(map[string]any)
	roles := caller["roles"].([]any)
	if caller["caller_service"] != "product-caller" || roles[0] != "repo_admin" || roles[1] != "operation_inspector" {
		t.Fatalf("allowed_callers = %#v, want serialized caller roles from defensive copy", callers)
	}
	exportPolicy := got["export_policy"].(map[string]any)
	lifecyclePolicy := got["lifecycle_policy"].(map[string]any)
	mountPolicy := got["mount_policy"].(map[string]any)
	templatePolicy := got["template_policy"].(map[string]any)
	assertMapHasKeys(t, exportPolicy, "webdav_enabled", "max_session_seconds")
	assertMapHasKeys(t, lifecyclePolicy, "tombstone_retention_seconds", "purge_requires_lifecycle_admin", "break_glass_purge_enabled")
	assertMapHasKeys(t, mountPolicy, "workload_mount_enabled", "workload_mount_requires_external_control_root", "allow_privileged_workload")
	assertMapHasKeys(t, templatePolicy, "namespace_templates_enabled", "cross_namespace_clone_enabled")
	assertMapLacksKeys(t, exportPolicy, "credential_ref")
	assertMapLacksKeys(t, lifecyclePolicy, "secret_path")
	assertMapLacksKeys(t, mountPolicy, "credential_ref")
	assertMapLacksKeys(t, templatePolicy, "secret_path")
	if exportPolicy["webdav_enabled"] != true ||
		lifecyclePolicy["purge_requires_lifecycle_admin"] != true ||
		mountPolicy["workload_mount_enabled"] != true ||
		templatePolicy["cross_namespace_clone_enabled"] != false {
		t.Fatalf("policy maps = %#v, want defensive copies before source mutation", got)
	}
}

func TestRepoResponseJSONShapeOmitsInternalPaths(t *testing.T) {
	now := resourceDTOTestNow()
	repo := resources.Repo{
		ID:                  "repo_123",
		NamespaceID:         "ns_123",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_repo_alpha",
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/payload",
		Lifecycle: resources.RepoLifecycle{
			Status:                   resources.RepoStatusActive,
			LastLifecycleOperationID: "op_repo_create",
		},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}

	body := mustMarshalJSON(t, RepoResponseFromResource(repo))
	assertJSONDoesNotLeakFields(t, body, "ControlVolumeSubdir", "PayloadVolumeSubdir", "control_volume_subdir", "payload_volume_subdir", "UpdatedAt", "updated_at", "/srv", "secret")

	var got map[string]any
	mustUnmarshalJSON(t, body, &got)
	assertOnlyJSONKeys(t, got, "repo_id", "namespace_id", "volume_id", "repo_kind", "jvs_repo_id", "status", "lifecycle", "created_at")
	if got["repo_id"] != "repo_123" || got["namespace_id"] != "ns_123" || got["volume_id"] != "vol_123" || got["jvs_repo_id"] != "jvs_repo_alpha" || got["status"] != "active" {
		t.Fatalf("repo JSON = %#v, want schema field values", got)
	}
	lifecycle := got["lifecycle"].(map[string]any)
	assertOnlyJSONKeys(t, lifecycle, "status", "retention_expires_at", "last_lifecycle_operation_id")
	if lifecycle["status"] != "active" || lifecycle["retention_expires_at"] != nil || lifecycle["last_lifecycle_operation_id"] != "op_repo_create" {
		t.Fatalf("lifecycle = %#v, want active lifecycle", lifecycle)
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return body
}

func mustUnmarshalJSON(t *testing.T, body []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("unmarshal JSON %s: %v", string(body), err)
	}
}

func assertOnlyJSONKeys(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, key := range keys {
		want[key] = true
		if _, ok := got[key]; !ok {
			t.Fatalf("JSON keys = %#v, missing %q", got, key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Fatalf("JSON keys = %#v, unexpected %q", got, key)
		}
	}
}

func assertJSONDoesNotLeakFields(t *testing.T, body []byte, forbidden ...string) {
	t.Helper()
	text := string(body)
	for _, field := range forbidden {
		if strings.Contains(text, field) {
			t.Fatalf("JSON %s leaked forbidden field %q", text, field)
		}
	}
}

func assertMapHasKeys(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := got[key]; !ok {
			t.Fatalf("map = %#v, missing %q", got, key)
		}
	}
}

func assertMapLacksKeys(t *testing.T, got map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := got[key]; ok {
			t.Fatalf("map = %#v, leaked %q", got, key)
		}
	}
}

func resourceDTOTestNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}
