package resources

import (
	"strings"
	"testing"
	"time"
)

func TestVolumeValidateAcceptsControlPlaneCapabilities(t *testing.T) {
	volume := Volume{
		ID:             "vol_shared01",
		Backend:        VolumeBackendJuiceFS,
		IsolationClass: VolumeIsolationShared,
		Status:         VolumeStatusActive,
		Capabilities: map[string]any{
			"webdav_export":             true,
			"workload_mount":            true,
			"jvs_external_control_root": true,
			"directory_quota":           false,
			"filtered_mount":            true,
			"csi_driver":                "juicefs.csi",
			"storage_class":             "shared-fast",
			"permission_model":          "namespace",
		},
		CreatedAt: time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC),
	}

	if err := volume.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestVolumeValidateRejectsInvalidIDsEnumsAndSensitiveMaterial(t *testing.T) {
	base := validVolume()
	tests := []struct {
		name string
		edit func(*Volume)
	}{
		{name: "empty id", edit: func(volume *Volume) { volume.ID = "" }},
		{name: "unknown backend", edit: func(volume *Volume) { volume.Backend = "localfs" }},
		{name: "unknown status", edit: func(volume *Volume) { volume.Status = "ready" }},
		{name: "sensitive capability key", edit: func(volume *Volume) {
			volume.Capabilities["credential_ref"] = "platform-managed"
		}},
		{name: "non object capabilities", edit: func(volume *Volume) { volume.Capabilities = nil }},
		{name: "missing required capability", edit: func(volume *Volume) { delete(volume.Capabilities, "webdav_export") }},
		{name: "wrong required capability type", edit: func(volume *Volume) { volume.Capabilities["workload_mount"] = "true" }},
		{name: "wrong optional capability type", edit: func(volume *Volume) { volume.Capabilities["storage_class"] = true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volume := base
			volume.Capabilities = cloneObject(base.Capabilities)
			tt.edit(&volume)
			if err := volume.Validate(); err == nil {
				t.Fatal("Validate succeeded, want error")
			}
		})
	}
}

func TestNamespaceValidateRequiresDurableDisableMetadata(t *testing.T) {
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	namespace := Namespace{
		ID:             "ns_alpha01",
		Status:         NamespaceStatusDisabled,
		DisabledReason: "billing hold",
		DisabledAt:     &now,
		CreatedAt:      now.Add(-time.Hour),
		UpdatedAt:      now,
	}

	if err := namespace.Validate(); err != nil {
		t.Fatalf("Validate disabled namespace: %v", err)
	}

	namespace.DisabledAt = nil
	if err := namespace.Validate(); err == nil {
		t.Fatal("Validate disabled namespace without disabled_at succeeded, want error")
	}
}

func TestNamespaceVolumeBindingValidateMatchesRoleAndPolicyContract(t *testing.T) {
	binding := validBinding()

	if err := binding.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	t.Run("rejects operator admin as ordinary namespace role", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers[0].Roles = append(binding.AllowedCallers[0].Roles, CallerRoleOperatorAdmin)
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want operator_admin rejection")
		}
	})

	t.Run("rejects deployment admin roles as ordinary namespace role", func(t *testing.T) {
		for _, role := range []CallerRole{CallerRoleBreakGlassAdmin, CallerRoleVolumeAdmin} {
			binding := validBinding()
			binding.AllowedCallers[0].Roles = []CallerRole{role}
			if err := binding.Validate(); err == nil {
				t.Fatalf("Validate with role %q succeeded, want rejection", role)
			}
		}
	})

	t.Run("allows dedicated orchestrator mount caller", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers[0] = AllowedCaller{
			CallerService: "runtime-orchestrator",
			Roles:         []CallerRole{CallerRoleOrchestratorMount},
		}
		if err := binding.Validate(); err != nil {
			t.Fatalf("Validate dedicated orchestrator caller: %v", err)
		}
	})

	t.Run("allows dedicated migration caller", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers[0] = AllowedCaller{
			CallerService: "afscp-migration",
			Roles:         []CallerRole{CallerRoleMigrationAdmin},
		}
		if err := binding.Validate(); err != nil {
			t.Fatalf("Validate dedicated migration caller: %v", err)
		}
	})

	t.Run("rejects orchestrator role mixed with product roles", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers[0].Roles = []CallerRole{CallerRoleOrchestratorMount, CallerRoleRepoAdmin}
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want orchestrator role mixing rejection")
		}
	})

	t.Run("rejects migration role mixed with product roles", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers[0].Roles = []CallerRole{CallerRoleMigrationAdmin, CallerRoleRepoAdmin}
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want migration role mixing rejection")
		}
	})

	t.Run("rejects migration role mixed with orchestrator role", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers[0].Roles = []CallerRole{CallerRoleMigrationAdmin, CallerRoleOrchestratorMount}
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want dedicated role mixing rejection")
		}
	})

	t.Run("rejects duplicate caller service ordinary entries", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers = []AllowedCaller{
			{CallerService: "product-caller", Roles: []CallerRole{CallerRoleRepoAdmin}},
			{CallerService: " product-caller ", Roles: []CallerRole{CallerRoleOperationInspector}},
		}
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want duplicate caller_service rejection")
		}
	})

	t.Run("rejects duplicate caller service across ordinary and dedicated migration", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers = []AllowedCaller{
			{CallerService: "product-caller", Roles: []CallerRole{CallerRoleRepoAdmin}},
			{CallerService: "product-caller", Roles: []CallerRole{CallerRoleMigrationAdmin}},
		}
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want duplicate ordinary/migration caller_service rejection")
		}
	})

	t.Run("rejects duplicate caller service across ordinary and dedicated orchestrator", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers = []AllowedCaller{
			{CallerService: "product-caller", Roles: []CallerRole{CallerRoleRepoAdmin}},
			{CallerService: "product-caller", Roles: []CallerRole{CallerRoleOrchestratorMount}},
		}
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want duplicate ordinary/orchestrator caller_service rejection")
		}
	})

	t.Run("allows distinct ordinary and dedicated caller services", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers = []AllowedCaller{
			{CallerService: "product-caller", Roles: []CallerRole{CallerRoleRepoAdmin, CallerRoleOperationInspector}},
			{CallerService: "afscp-migration", Roles: []CallerRole{CallerRoleMigrationAdmin}},
			{CallerService: "runtime-orchestrator", Roles: []CallerRole{CallerRoleOrchestratorMount}},
		}
		if err := binding.Validate(); err != nil {
			t.Fatalf("Validate distinct ordinary/dedicated callers: %v", err)
		}
	})

	t.Run("rejects unknown role", func(t *testing.T) {
		binding := validBinding()
		binding.AllowedCallers[0].Roles = []CallerRole{"workspace_owner"}
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want role rejection")
		}
	})

	t.Run("requires policy JSON objects", func(t *testing.T) {
		binding := validBinding()
		binding.ExportPolicy = nil
		if err := binding.Validate(); err == nil {
			t.Fatal("Validate succeeded, want export policy object rejection")
		}
	})

	t.Run("requires schema policy fields", func(t *testing.T) {
		tests := []struct {
			name string
			edit func(*NamespaceVolumeBinding)
		}{
			{name: "export missing webdav enabled", edit: func(binding *NamespaceVolumeBinding) { delete(binding.ExportPolicy, "webdav_enabled") }},
			{name: "export session too short", edit: func(binding *NamespaceVolumeBinding) { binding.ExportPolicy["max_session_seconds"] = 59 }},
			{name: "lifecycle missing purge role", edit: func(binding *NamespaceVolumeBinding) {
				delete(binding.LifecyclePolicy, "purge_requires_lifecycle_admin")
			}},
			{name: "mount wrong bool type", edit: func(binding *NamespaceVolumeBinding) { binding.MountPolicy["allow_privileged_workload"] = "false" }},
			{name: "template missing cross namespace flag", edit: func(binding *NamespaceVolumeBinding) { delete(binding.TemplatePolicy, "cross_namespace_clone_enabled") }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				binding := validBinding()
				tt.edit(&binding)
				if err := binding.Validate(); err == nil {
					t.Fatal("Validate succeeded, want schema policy rejection")
				}
			})
		}
	})
}

func TestRepoValidateCoversLifecycleAndRecordedPathIdentity(t *testing.T) {
	repo := validRepo()

	if err := repo.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Repo)
	}{
		{name: "bad repo id", edit: func(repo *Repo) { repo.ID = "tmpl_wrong" }},
		{name: "bad volume id", edit: func(repo *Repo) { repo.VolumeID = "" }},
		{name: "unknown kind", edit: func(repo *Repo) { repo.Kind = "workspace" }},
		{name: "unknown status", edit: func(repo *Repo) { repo.Status = "deleted" }},
		{name: "empty jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = "" }},
		{name: "too long jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = strings.Repeat("a", 129) }},
		{name: "control jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = "jvs\nrepo" }},
		{name: "whitespace jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = "jvs repo" }},
		{name: "slash jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = "jvs/repo" }},
		{name: "backslash jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = `jvs\repo` }},
		{name: "equals jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = "jvs=repo" }},
		{name: "schema-disallowed punctuation jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = "jvs;repo" }},
		{name: "raw path jvs repo id", edit: func(repo *Repo) { repo.JVSRepoID = "/srv/secret" }},
		{name: "lifecycle mismatch", edit: func(repo *Repo) { repo.Lifecycle.Status = RepoStatusArchived }},
		{name: "missing control subdir", edit: func(repo *Repo) { repo.ControlVolumeSubdir = "" }},
		{name: "raw path identity", edit: func(repo *Repo) { repo.PayloadVolumeSubdir = "/var/lib/repos/repo_alpha" }},
		{name: "namespace path mismatch", edit: func(repo *Repo) {
			repo.ControlVolumeSubdir = "afscp/namespaces/ns_other01/repos/repo_alpha01/control"
		}},
		{name: "repo path mismatch", edit: func(repo *Repo) {
			repo.PayloadVolumeSubdir = "afscp/namespaces/ns_alpha01/repos/repo_other01/payload"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := validRepo()
			tt.edit(&repo)
			if err := repo.Validate(); err == nil {
				t.Fatal("Validate succeeded, want error")
			}
		})
	}
}

func TestRepoValidateSupportsTemplateKindWithCanonicalTemplateIdentity(t *testing.T) {
	now := time.Date(2026, 5, 5, 11, 30, 0, 0, time.UTC)
	template := Repo{
		ID:                  "tmpl_base01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_shared01",
		JVSRepoID:           "jvs-template-alpha",
		Kind:                RepoKindTemplate,
		Status:              RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/templates/tmpl_base01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/templates/tmpl_base01/payload",
		Lifecycle:           RepoLifecycle{Status: RepoStatusActive},
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := template.Validate(); err != nil {
		t.Fatalf("Validate template kind: %v", err)
	}

	template.PayloadVolumeSubdir = "afscp/namespaces/ns_alpha01/repos/tmpl_base01/payload"
	if err := template.Validate(); err == nil {
		t.Fatal("Validate template with repo path succeeded, want canonical template path rejection")
	}
}

func TestRepoLifecycleValidateEnforcesTombstoneMetadataCombinations(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	valid := []RepoLifecycle{
		{Status: RepoStatusActive},
		{Status: RepoStatusArchived},
		{Status: RepoStatusTombstoned, RetentionExpiresAt: &now, PreDeleteStatus: RepoStatusActive},
		{Status: RepoStatusPurged, PreDeleteStatus: RepoStatusArchived},
		{Status: RepoStatusOperatorInterventionRequired},
		{Status: RepoStatusOperatorInterventionRequired, RetentionExpiresAt: &now, PreDeleteStatus: RepoStatusActive},
	}
	for _, lifecycle := range valid {
		if err := lifecycle.Validate(); err != nil {
			t.Fatalf("Validate(%#v): %v", lifecycle, err)
		}
	}

	invalid := []RepoLifecycle{
		{Status: RepoStatusTombstoned, RetentionExpiresAt: &now},
		{Status: RepoStatusDeleting, PreDeleteStatus: RepoStatusDeleting},
		{Status: RepoStatusActive, PreDeleteStatus: RepoStatusActive},
		{Status: RepoStatusArchived, RetentionExpiresAt: &now},
		{Status: RepoStatusPurging, PreDeleteStatus: RepoStatusActive},
		{Status: RepoStatusPurged},
		{Status: RepoStatusOperatorInterventionRequired, PreDeleteStatus: RepoStatusPurging},
	}
	for _, lifecycle := range invalid {
		if err := lifecycle.Validate(); err == nil {
			t.Fatalf("Validate(%#v) succeeded, want rejection", lifecycle)
		}
	}
}

func validVolume() Volume {
	now := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)
	return Volume{
		ID:             "vol_shared01",
		Backend:        VolumeBackendJuiceFS,
		IsolationClass: VolumeIsolationShared,
		Status:         VolumeStatusActive,
		Capabilities: map[string]any{
			"webdav_export":             true,
			"workload_mount":            true,
			"jvs_external_control_root": true,
			"directory_quota":           false,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func validBinding() NamespaceVolumeBinding {
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	return NamespaceVolumeBinding{
		NamespaceID:     "ns_alpha01",
		DefaultVolumeID: "vol_shared01",
		AllowedCallers: []AllowedCaller{{
			CallerService: "product-caller",
			Roles:         []CallerRole{CallerRoleRepoAdmin, CallerRoleOperationInspector},
		}},
		QuotaBytesDefault: 1024,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_jvs_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func validRepo() Repo {
	now := time.Date(2026, 5, 5, 11, 0, 0, 0, time.UTC)
	return Repo{
		ID:                  "repo_alpha01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_shared01",
		JVSRepoID:           "jvs-alpha",
		Kind:                RepoKindRepo,
		Status:              RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
		Lifecycle: RepoLifecycle{
			Status: RepoStatusActive,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func cloneObject(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
