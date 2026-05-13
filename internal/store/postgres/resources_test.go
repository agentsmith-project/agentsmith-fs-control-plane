package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsResourceContracts(t *testing.T) {
	var _ store.VolumeStore = (*Store)(nil)
	var _ store.NamespaceStore = (*Store)(nil)
	var _ store.NamespaceVolumeBindingStore = (*Store)(nil)
	var _ store.RepoReader = (*Store)(nil)
	var _ store.RepoWriter = (*Store)(nil)
	var _ store.RepoStore = (*Store)(nil)
}

func TestUpsertVolumePersistsControlPlaneCapabilityObjectOnly(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	volume := volumeFixture()
	volume.Capabilities = map[string]any{
		"webdav_export":             true,
		"workload_mount":            true,
		"jvs_external_control_root": true,
		"directory_quota":           false,
		"storage_class":             "shared-fast",
	}

	if err := st.UpsertVolume(context.Background(), volume); err != nil {
		t.Fatalf("UpsertVolume: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO volumes",
		"volume_id", "backend", "isolation_class", "status", "capabilities", "created_at", "updated_at",
		"ON CONFLICT (volume_id) DO UPDATE SET",
		"backend = EXCLUDED.backend",
		"capabilities = EXCLUDED.capabilities",
	)
	if strings.Contains(strings.ToLower(exec.query), "credential") || strings.Contains(strings.ToLower(exec.query), "secret") {
		t.Fatalf("volume query contains sensitive material column: %s", exec.query)
	}
	if len(exec.args) != len(volumeColumns) {
		t.Fatalf("arg count = %d, want %d", len(exec.args), len(volumeColumns))
	}
	if exec.args[0] != "vol_shared01" || exec.args[1] != string(resources.VolumeBackendJuiceFS) || exec.args[3] != string(resources.VolumeStatusActive) {
		t.Fatalf("volume args = %#v", exec.args[:4])
	}
	capabilities := mustJSONMap(t, exec.args[4])
	if capabilities["workload_mount"] != true || capabilities["storage_class"] != "shared-fast" {
		t.Fatalf("capabilities = %#v, want round trip object", capabilities)
	}
}

func TestUpsertVolumeRejectsInvalidCapabilitySchemaBeforeSQL(t *testing.T) {
	tests := []struct {
		name string
		edit func(*resources.Volume)
	}{
		{name: "sensitive material", edit: func(volume *resources.Volume) { volume.Capabilities["raw_secret"] = "nope" }},
		{name: "missing required key", edit: func(volume *resources.Volume) { delete(volume.Capabilities, "webdav_export") }},
		{name: "wrong required type", edit: func(volume *resources.Volume) { volume.Capabilities["directory_quota"] = "false" }},
		{name: "wrong optional type", edit: func(volume *resources.Volume) { volume.Capabilities["csi_driver"] = true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			volume := volumeFixture()
			tt.edit(&volume)

			err := st.UpsertVolume(context.Background(), volume)
			if err == nil {
				t.Fatal("UpsertVolume succeeded, want validation error")
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL for invalid volume", exec.query)
			}
		})
	}
}

func TestGetAndListActiveVolumesScanCapabilityJSON(t *testing.T) {
	volume := volumeFixture()
	exec := &fakeExecutor{
		row: fakeRow{values: volumeRowValues(volume)},
		rows: fakeRows{rows: []fakeRow{
			{values: volumeRowValues(volume)},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.GetVolume(context.Background(), "vol_shared01")
	if err != nil {
		t.Fatalf("GetVolume: %v", err)
	}
	if got.ID != "vol_shared01" || got.Capabilities["workload_mount"] != true {
		t.Fatalf("volume = %#v, want scanned capabilities", got)
	}

	active, err := st.ListActiveVolumes(context.Background())
	if err != nil {
		t.Fatalf("ListActiveVolumes: %v", err)
	}
	assertSQLContainsInOrder(t, exec.query, "FROM volumes", "WHERE status = $1", "ORDER BY volume_id")
	if len(active) != 1 || active[0].Status != resources.VolumeStatusActive || !exec.rows.closed {
		t.Fatalf("active = %#v closed=%v", active, exec.rows.closed)
	}
}

func TestUpsertNamespaceDoesNotAutoRecoverDisabledNamespace(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}

	if err := st.UpsertNamespace(context.Background(), namespaceFixture()); err != nil {
		t.Fatalf("UpsertNamespace: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO namespaces",
		"ON CONFLICT (namespace_id) DO UPDATE SET",
		"status = CASE WHEN namespaces.status = 'disabled' THEN namespaces.status ELSE EXCLUDED.status END",
		"disabled_reason = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_reason ELSE EXCLUDED.disabled_reason END",
		"disabled_at = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_at ELSE EXCLUDED.disabled_at END",
	)
}

func TestDisableNamespaceRecordsReasonAndIsIdempotent(t *testing.T) {
	firstDisabledAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	callTime := firstDisabledAt.Add(time.Hour)
	exec := &fakeExecutor{
		row: fakeRow{values: namespaceRowValues(resources.Namespace{
			ID:             "ns_alpha01",
			Status:         resources.NamespaceStatusDisabled,
			DisabledReason: "first reason",
			DisabledAt:     &firstDisabledAt,
			CreatedAt:      firstDisabledAt.Add(-time.Hour),
			UpdatedAt:      callTime,
		})},
		rowsAffected: 1,
	}
	st := &Store{exec: exec, clock: func() time.Time { return callTime }}

	got, err := st.DisableNamespace(context.Background(), "ns_alpha01", "second reason")
	if err != nil {
		t.Fatalf("DisableNamespace: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"UPDATE namespaces SET",
		"status = 'disabled'",
		"disabled_reason = CASE WHEN status = 'disabled' THEN disabled_reason ELSE $2 END",
		"disabled_at = CASE WHEN status = 'disabled' THEN disabled_at ELSE $3 END",
		"RETURNING",
	)
	if exec.args[0] != "ns_alpha01" || exec.args[1] != "second reason" || exec.args[2] != callTime {
		t.Fatalf("disable args = %#v", exec.args)
	}
	if got.DisabledReason != "first reason" || got.DisabledAt == nil || !got.DisabledAt.Equal(firstDisabledAt) {
		t.Fatalf("disabled namespace = %#v, want first disable metadata preserved", got)
	}
}

func TestDisableNamespaceRejectsInvalidRequestBeforeSQL(t *testing.T) {
	for _, tt := range []struct {
		name        string
		namespaceID string
		reason      string
	}{
		{name: "bad namespace", namespaceID: "repo_wrong", reason: "hold"},
		{name: "empty reason", namespaceID: "ns_alpha01", reason: " "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.DisableNamespace(context.Background(), tt.namespaceID, tt.reason)
			if err == nil {
				t.Fatal("DisableNamespace succeeded, want validation error")
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestNamespaceVolumeBindingRoundTripsPoliciesAndAllowedCallers(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	binding := bindingFixture()

	if err := st.PutNamespaceVolumeBinding(context.Background(), binding); err != nil {
		t.Fatalf("PutNamespaceVolumeBinding: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO namespace_volume_bindings",
		"namespace_id", "default_volume_id", "allowed_callers", "quota_bytes_default",
		"export_policy", "lifecycle_policy", "mount_policy", "template_policy", "status",
		"ON CONFLICT (namespace_id) DO UPDATE SET",
	)
	if got := mustJSONSlice(t, exec.args[2]); len(got) != 1 {
		t.Fatalf("allowed callers json = %#v, want one caller", got)
	}
	if got := mustJSONMap(t, exec.args[4])["webdav_enabled"]; got != true {
		t.Fatalf("export policy webdav_enabled = %#v, want true", got)
	}

	exec.row = fakeRow{values: bindingRowValues(binding)}
	got, err := st.GetNamespaceVolumeBinding(context.Background(), "ns_alpha01")
	if err != nil {
		t.Fatalf("GetNamespaceVolumeBinding: %v", err)
	}
	if got.DefaultVolumeID != "vol_shared01" || got.QuotaBytesDefault != 4096 {
		t.Fatalf("binding ids/quota = %#v", got)
	}
	if !reflect.DeepEqual(got.AllowedCallers, binding.AllowedCallers) || got.TemplatePolicy["cross_namespace_clone_enabled"] != false {
		t.Fatalf("binding policies = %#v, want round trip", got)
	}
}

func TestNamespaceVolumeBindingRejectsOperatorAdminOrdinaryPolicyBeforeSQL(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	binding := bindingFixture()
	binding.AllowedCallers[0].Roles = []resources.CallerRole{resources.CallerRoleOperatorAdmin}

	err := st.PutNamespaceVolumeBinding(context.Background(), binding)
	if err == nil {
		t.Fatal("PutNamespaceVolumeBinding succeeded, want validation error")
	}
	if exec.query != "" {
		t.Fatalf("query = %q, want no SQL", exec.query)
	}
}

func TestNamespaceVolumeBindingRejectsDeploymentAndMixedOrchestratorRolesBeforeSQL(t *testing.T) {
	tests := []struct {
		name  string
		roles []resources.CallerRole
	}{
		{name: "break glass admin", roles: []resources.CallerRole{resources.CallerRoleBreakGlassAdmin}},
		{name: "volume admin", roles: []resources.CallerRole{resources.CallerRoleVolumeAdmin}},
		{name: "orchestrator mixed with product role", roles: []resources.CallerRole{resources.CallerRoleOrchestratorMount, resources.CallerRoleRepoAdmin}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			binding := bindingFixture()
			binding.AllowedCallers[0].Roles = tt.roles

			err := st.PutNamespaceVolumeBinding(context.Background(), binding)
			if err == nil {
				t.Fatal("PutNamespaceVolumeBinding succeeded, want validation error")
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestNamespaceVolumeBindingRejectsInvalidPolicySchemaBeforeSQL(t *testing.T) {
	tests := []struct {
		name string
		edit func(*resources.NamespaceVolumeBinding)
	}{
		{name: "export missing webdav enabled", edit: func(binding *resources.NamespaceVolumeBinding) { delete(binding.ExportPolicy, "webdav_enabled") }},
		{name: "export max session below minimum", edit: func(binding *resources.NamespaceVolumeBinding) { binding.ExportPolicy["max_session_seconds"] = 30 }},
		{name: "lifecycle missing purge role", edit: func(binding *resources.NamespaceVolumeBinding) {
			delete(binding.LifecyclePolicy, "purge_requires_lifecycle_admin")
		}},
		{name: "mount missing external control root requirement", edit: func(binding *resources.NamespaceVolumeBinding) {
			delete(binding.MountPolicy, "workload_mount_requires_external_control_root")
		}},
		{name: "template wrong cross namespace type", edit: func(binding *resources.NamespaceVolumeBinding) {
			binding.TemplatePolicy["cross_namespace_clone_enabled"] = "false"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			binding := bindingFixture()
			tt.edit(&binding)

			err := st.PutNamespaceVolumeBinding(context.Background(), binding)
			if err == nil {
				t.Fatal("PutNamespaceVolumeBinding succeeded, want validation error")
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestCreateGetAndListReposPersistImmutableIdentityAndLifecycleMetadata(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	repo := repoFixture()

	if err := st.CreateRepo(context.Background(), repo); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO repos",
		"repo_id", "namespace_id", "volume_id", "jvs_repo_id", "repo_kind", "status",
		"control_volume_subdir", "payload_volume_subdir", "lifecycle_status",
	)
	for _, forbidden := range []string{"ON CONFLICT", "credential", "secret"} {
		if strings.Contains(strings.ToLower(exec.query), strings.ToLower(forbidden)) {
			t.Fatalf("create repo query contains %q: %s", forbidden, exec.query)
		}
	}
	if exec.args[0] != "repo_alpha01" || exec.args[1] != "ns_alpha01" || exec.args[2] != "vol_shared01" || exec.args[3] != "jvs-alpha" {
		t.Fatalf("repo identity args = %#v", exec.args[:4])
	}

	exec.row = fakeRow{values: repoRowValues(repo)}
	got, err := st.GetRepo(context.Background(), "repo_alpha01")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if got.ID != repo.ID || got.VolumeID != repo.VolumeID || got.JVSRepoID != repo.JVSRepoID || got.PayloadVolumeSubdir != repo.PayloadVolumeSubdir {
		t.Fatalf("repo = %#v, want recorded identity", got)
	}

	exec.row = fakeRow{values: repoRowValues(repo)}
	got, err = st.GetRepoInNamespace(context.Background(), "ns_alpha01", "repo_alpha01")
	if err != nil {
		t.Fatalf("GetRepoInNamespace: %v", err)
	}
	assertSQLContainsInOrder(t, exec.query, "FROM repos", "WHERE namespace_id = $1", "AND repo_id = $2")
	if got.ID != repo.ID || got.NamespaceID != repo.NamespaceID || exec.args[0] != "ns_alpha01" || exec.args[1] != "repo_alpha01" {
		t.Fatalf("repo/args = %#v/%#v, want namespace-scoped repo read", got, exec.args)
	}

	exec.rows = fakeRows{rows: []fakeRow{{values: repoRowValues(repo)}}}
	repos, err := st.ListReposByNamespace(context.Background(), "ns_alpha01")
	if err != nil {
		t.Fatalf("ListReposByNamespace: %v", err)
	}
	assertSQLContainsInOrder(t, exec.query, "FROM repos", "WHERE namespace_id = $1", "ORDER BY created_at, repo_id")
	if len(repos) != 1 || repos[0].ID != "repo_alpha01" || !exec.rows.closed {
		t.Fatalf("repos = %#v closed=%v", repos, exec.rows.closed)
	}
}

func TestCreateRepoRejectsNonCanonicalPathIdentityBeforeSQL(t *testing.T) {
	tests := []struct {
		name string
		edit func(*resources.Repo)
	}{
		{name: "repo path namespace mismatch", edit: func(repo *resources.Repo) {
			repo.ControlVolumeSubdir = "afscp/namespaces/ns_other01/repos/repo_alpha01/control"
		}},
		{name: "template kind with repo path", edit: func(repo *resources.Repo) {
			repo.ID = "tmpl_base01"
			repo.Kind = resources.RepoKindTemplate
			repo.ControlVolumeSubdir = "afscp/namespaces/ns_alpha01/repos/tmpl_base01/control"
			repo.PayloadVolumeSubdir = "afscp/namespaces/ns_alpha01/repos/tmpl_base01/payload"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			repo := repoFixture()
			tt.edit(&repo)

			err := st.CreateRepo(context.Background(), repo)
			if err == nil {
				t.Fatal("CreateRepo succeeded, want validation error")
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestCreateRepoAcceptsTemplateKindWithCanonicalTemplatePath(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	repo := repoFixture()
	repo.ID = "tmpl_base01"
	repo.Kind = resources.RepoKindTemplate
	repo.JVSRepoID = "jvs-template-alpha"
	repo.ControlVolumeSubdir = "afscp/namespaces/ns_alpha01/templates/tmpl_base01/control"
	repo.PayloadVolumeSubdir = "afscp/namespaces/ns_alpha01/templates/tmpl_base01/payload"

	if err := st.CreateRepo(context.Background(), repo); err != nil {
		t.Fatalf("CreateRepo template kind: %v", err)
	}
	if exec.args[0] != "tmpl_base01" || exec.args[4] != string(resources.RepoKindTemplate) {
		t.Fatalf("template args = %#v", exec.args[:5])
	}
}

func TestUpdateRepoLifecycleOnlyMutatesMetadataState(t *testing.T) {
	updatedAt := time.Date(2026, 5, 5, 13, 0, 0, 0, time.UTC)
	retention := updatedAt.Add(24 * time.Hour)
	repo := repoFixture()
	repo.Status = resources.RepoStatusTombstoned
	repo.Lifecycle = resources.RepoLifecycle{
		Status:                   resources.RepoStatusTombstoned,
		RetentionExpiresAt:       &retention,
		LastLifecycleOperationID: "op_delete01",
		PreDeleteStatus:          resources.RepoStatusActive,
	}
	repo.UpdatedAt = updatedAt
	exec := &fakeExecutor{row: fakeRow{values: repoRowValues(repo)}, rowsAffected: 1}
	st := &Store{exec: exec, clock: func() time.Time { return updatedAt }}

	got, err := st.UpdateRepoLifecycle(context.Background(), "repo_alpha01", repo.Lifecycle)
	if err != nil {
		t.Fatalf("UpdateRepoLifecycle: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"UPDATE repos SET",
		"status = $1",
		"lifecycle_status = $2",
		"retention_expires_at = $3",
		"last_lifecycle_operation_id = $4",
		"pre_delete_status = $5",
		"updated_at = $6",
		"WHERE repo_id = $7",
		"RETURNING",
	)
	updateClause := strings.Split(exec.query, " WHERE ")[0]
	for _, immutable := range []string{"repo_id =", "namespace_id =", "volume_id =", "jvs_repo_id =", "control_volume_subdir =", "payload_volume_subdir ="} {
		if strings.Contains(updateClause, immutable) {
			t.Fatalf("UpdateRepoLifecycle mutates immutable identity %q: %s", immutable, exec.query)
		}
	}
	for _, forbidden := range []string{"webdav", "mount", "delete from", "truncate"} {
		if strings.Contains(strings.ToLower(exec.query), forbidden) {
			t.Fatalf("UpdateRepoLifecycle query contains external side-effect term %q: %s", forbidden, exec.query)
		}
	}
	if got.ID != "repo_alpha01" || got.VolumeID != "vol_shared01" || got.Status != resources.RepoStatusTombstoned {
		t.Fatalf("repo = %#v, want tombstoned with immutable identity", got)
	}
}

func TestListReposByNamespaceRejectsInvalidNamespaceBeforeSQL(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}

	_, err := st.ListReposByNamespace(context.Background(), "repo_wrong")
	if err == nil {
		t.Fatal("ListReposByNamespace succeeded, want namespace id validation error")
	}
	if exec.query != "" {
		t.Fatalf("query = %q, want no SQL", exec.query)
	}
}

func TestUpdateRepoLifecycleRejectsInvalidRepoIDAndLifecycleBeforeSQL(t *testing.T) {
	invalidRepoIDLifecycle := resources.RepoLifecycle{Status: resources.RepoStatusActive}
	now := time.Date(2026, 5, 5, 13, 30, 0, 0, time.UTC)
	invalidLifecycle := resources.RepoLifecycle{
		Status:             resources.RepoStatusTombstoned,
		RetentionExpiresAt: &now,
	}

	tests := []struct {
		name      string
		repoID    string
		lifecycle resources.RepoLifecycle
	}{
		{name: "bad repo id", repoID: "bad_wrong", lifecycle: invalidRepoIDLifecycle},
		{name: "bad lifecycle", repoID: "repo_alpha01", lifecycle: invalidLifecycle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.UpdateRepoLifecycle(context.Background(), tt.repoID, tt.lifecycle)
			if err == nil {
				t.Fatal("UpdateRepoLifecycle succeeded, want validation error")
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestUpdateRepoLifecycleAcceptsTemplateIDBoundary(t *testing.T) {
	updatedAt := time.Date(2026, 5, 5, 13, 45, 0, 0, time.UTC)
	repo := repoFixture()
	repo.ID = "tmpl_base01"
	repo.Kind = resources.RepoKindTemplate
	repo.JVSRepoID = "jvs-template-alpha"
	repo.ControlVolumeSubdir = "afscp/namespaces/ns_alpha01/templates/tmpl_base01/control"
	repo.PayloadVolumeSubdir = "afscp/namespaces/ns_alpha01/templates/tmpl_base01/payload"
	exec := &fakeExecutor{row: fakeRow{values: repoRowValues(repo)}, rowsAffected: 1}
	st := &Store{exec: exec, clock: func() time.Time { return updatedAt }}

	got, err := st.UpdateRepoLifecycle(context.Background(), "tmpl_base01", resources.RepoLifecycle{Status: resources.RepoStatusActive})
	if err != nil {
		t.Fatalf("UpdateRepoLifecycle template id: %v", err)
	}
	if exec.args[6] != "tmpl_base01" {
		t.Fatalf("repo id arg = %#v, want template id", exec.args[6])
	}
	if got.ID != "tmpl_base01" || got.Kind != resources.RepoKindTemplate {
		t.Fatalf("updated template = %#v", got)
	}
}

func TestGetResourceRecordsReturnSQLNoRows(t *testing.T) {
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	if _, err := st.GetVolume(context.Background(), "vol_missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetVolume error = %v, want sql.ErrNoRows", err)
	}
	if _, err := st.GetNamespace(context.Background(), "ns_missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetNamespace error = %v, want sql.ErrNoRows", err)
	}
	if _, err := st.GetNamespaceVolumeBinding(context.Background(), "ns_missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetNamespaceVolumeBinding error = %v, want sql.ErrNoRows", err)
	}
	if _, err := st.GetRepo(context.Background(), "repo_missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetRepo error = %v, want sql.ErrNoRows", err)
	}
}

func volumeFixture() resources.Volume {
	now := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)
	return resources.Volume{
		ID:             "vol_shared01",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
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

func namespaceFixture() resources.Namespace {
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	return resources.Namespace{
		ID:        "ns_alpha01",
		Status:    resources.NamespaceStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func bindingFixture() resources.NamespaceVolumeBinding {
	now := time.Date(2026, 5, 5, 10, 30, 0, 0, time.UTC)
	return resources.NamespaceVolumeBinding{
		NamespaceID:       "ns_alpha01",
		DefaultVolumeID:   "vol_shared01",
		AllowedCallers:    []resources.AllowedCaller{{CallerService: "product-caller", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleOperationInspector}}},
		QuotaBytesDefault: 4096,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            resources.NamespaceStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func repoFixture() resources.Repo {
	now := time.Date(2026, 5, 5, 11, 0, 0, 0, time.UTC)
	return resources.Repo{
		ID:                  "repo_alpha01",
		NamespaceID:         "ns_alpha01",
		VolumeID:            "vol_shared01",
		JVSRepoID:           "jvs-alpha",
		Kind:                resources.RepoKindRepo,
		Status:              resources.RepoStatusActive,
		ControlVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_alpha01/repos/repo_alpha01/payload",
		Lifecycle:           resources.RepoLifecycle{Status: resources.RepoStatusActive},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func volumeRowValues(volume resources.Volume) []any {
	return []any{
		volume.ID,
		string(volume.Backend),
		string(volume.IsolationClass),
		string(volume.Status),
		mustMarshalJSONForTest(volume.Capabilities),
		volume.CreatedAt,
		volume.UpdatedAt,
	}
}

func namespaceRowValues(namespace resources.Namespace) []any {
	return []any{
		namespace.ID,
		string(namespace.Status),
		nullableArgString(namespace.DisabledReason),
		timePtrValue(namespace.DisabledAt),
		namespace.CreatedAt,
		namespace.UpdatedAt,
	}
}

func bindingRowValues(binding resources.NamespaceVolumeBinding) []any {
	return []any{
		binding.NamespaceID,
		binding.DefaultVolumeID,
		mustMarshalJSONForTest(binding.AllowedCallers),
		binding.QuotaBytesDefault,
		mustMarshalJSONForTest(binding.ExportPolicy),
		mustMarshalJSONForTest(binding.LifecyclePolicy),
		mustMarshalJSONForTest(binding.MountPolicy),
		mustMarshalJSONForTest(binding.TemplatePolicy),
		string(binding.Status),
		binding.CreatedAt,
		binding.UpdatedAt,
	}
}

func repoRowValues(repo resources.Repo) []any {
	return []any{
		repo.ID,
		repo.NamespaceID,
		repo.VolumeID,
		repo.JVSRepoID,
		string(repo.Kind),
		string(repo.Status),
		repo.ControlVolumeSubdir,
		repo.PayloadVolumeSubdir,
		string(repo.Lifecycle.Status),
		timePtrValue(repo.Lifecycle.RetentionExpiresAt),
		nullableArgString(repo.Lifecycle.LastLifecycleOperationID),
		nullableArgString(string(repo.Lifecycle.PreDeleteStatus)),
		repo.CreatedAt,
		repo.UpdatedAt,
	}
}

func mustJSONSlice(t *testing.T, value any) []any {
	t.Helper()
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		t.Fatalf("value %T is not json bytes/string", value)
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal json %s: %v", raw, err)
	}
	return out
}
