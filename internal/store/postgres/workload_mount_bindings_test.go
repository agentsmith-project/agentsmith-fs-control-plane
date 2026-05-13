package postgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

const workloadMountPlanPayloadSubdirForTest = "afscp/namespaces/ns_123/repos/repo_123/payload"

func TestWorkloadMountPlanStoreContractBehaviorMatrix(t *testing.T) {
	// This is a store-level contract/behavior matrix, not a real Postgres
	// integration test: the repo currently has migrations but no reusable
	// migration-driven Postgres test harness in this package.
	fragments := workloadMountPlanSQLFragmentsForTest(t)

	tests := []struct {
		name         string
		row          fakeRow
		secretRefs   map[string]workloadmount.SecretRef
		wantPlan     workloadmount.Plan
		wantErr      error
		sqlContracts map[string][]string
		sqlForbidden map[string][]string
	}{
		{
			name:       "active issued binding returns plan with configured runtime secret",
			row:        fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_payload01", "repo_123", workloadMountPlanPayloadSubdirForTest, "/mnt/repo", true, true)},
			secretRefs: workloadMountPlanSecretRefsForTest(),
			wantPlan: workloadmount.Plan{
				MountBindingID:      "wmb_123",
				VolumeID:            "vol_payload01",
				PayloadVolumeSubdir: workloadMountPlanPayloadSubdirForTest,
				MountPath:           "/mnt/repo",
				ReadOnly:            true,
				SecretRef:           workloadMountPlanSecretRefForTest(),
				SecurityPolicy:      workloadmount.SecurityPolicy{RunAsNonRoot: true, AllowPrivileged: true, JVSControlOutsidePayload: true},
			},
			sqlContracts: map[string][]string{
				"candidate":      {"namespace_id = $1", "mount_binding_id = $2", "status IN ('issued','pending','active','releasing')"},
				"activeBinding":  {"b.status IN ('issued','pending','active')", "nvb.status = 'active'", "workload_mount_enabled", "workload_mount_requires_external_control_root"},
				"activeRepo":     {"r.repo_kind = 'repo'", "r.status = 'active'", "r.lifecycle_status = 'active'"},
				"activeVolume":   {"v.status = 'active'", "workload_mount", "jvs_external_control_root"},
				"issuanceTrack":  {"b.mount_binding_id, b.volume_id, r.repo_id, r.payload_volume_subdir", "allow_privileged_workload", "EXISTS (SELECT 1 FROM active_namespace)", "EXISTS (SELECT 1 FROM active_volume)", "NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)"},
				"teardownTrack":  {"b.mount_binding_id, b.volume_id, r.repo_id, r.payload_volume_subdir", "false AS allow_privileged_workload"},
				"fullPlanSelect": {"SELECT * FROM issuance_track UNION ALL SELECT * FROM teardown_track"},
			},
		},
		{
			name:    "disabled namespace with active binding is denied by issuance namespace gate",
			row:     fakeRow{err: sql.ErrNoRows},
			wantErr: sql.ErrNoRows,
			sqlContracts: map[string][]string{
				"activeNamespace":   {"ns.status = 'active'", "b.status IN ('issued','pending','active')"},
				"teardownNamespace": {"ns.status IN ('active','disabled')", "b.status = 'releasing'"},
				"issuanceTrack":     {"b.status IN ('issued','pending','active')", "EXISTS (SELECT 1 FROM active_namespace)"},
			},
		},
		{
			name:       "disabled namespace with releasing binding returns unprivileged teardown plan",
			row:        fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_payload01", "repo_123", workloadMountPlanPayloadSubdirForTest, "/mnt/repo", false, false)},
			secretRefs: workloadMountPlanSecretRefsForTest(),
			wantPlan: workloadmount.Plan{
				MountBindingID:      "wmb_123",
				VolumeID:            "vol_payload01",
				PayloadVolumeSubdir: workloadMountPlanPayloadSubdirForTest,
				MountPath:           "/mnt/repo",
				ReadOnly:            false,
				SecretRef:           workloadMountPlanSecretRefForTest(),
				SecurityPolicy:      workloadmount.SecurityPolicy{RunAsNonRoot: true, AllowPrivileged: false, JVSControlOutsidePayload: true},
			},
			sqlContracts: map[string][]string{
				"teardownNamespace": {"ns.status IN ('active','disabled')", "b.status = 'releasing'"},
				"repoIdentity":      {"b.status = 'releasing'", "r.repo_kind = 'repo'"},
				"teardownTrack":     {"false AS allow_privileged_workload", "b.status = 'releasing'", "EXISTS (SELECT 1 FROM teardown_namespace)"},
			},
			sqlForbidden: map[string][]string{
				"teardownTrack": {"active_binding", "active_volume", "held_lifecycle_fence", "mount_policy", "workload_mount_enabled", "jvs_external_control_root"},
			},
		},
		{
			name:    "held lifecycle fence with active binding is denied by issuance fence",
			row:     fakeRow{err: sql.ErrNoRows},
			wantErr: sql.ErrNoRows,
			sqlContracts: map[string][]string{
				"heldLifecycleFence": {"fence_kind = 'lifecycle'", "status IN ('active','expired','recovery_required')", "released_at IS NULL", "recovered_at IS NULL"},
				"issuanceTrack":      {"b.status IN ('issued','pending','active')", "NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)"},
			},
		},
		{
			name:       "held lifecycle fence with releasing binding returns unprivileged teardown plan",
			row:        fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_payload01", "repo_123", workloadMountPlanPayloadSubdirForTest, "/mnt/repo", false, false)},
			secretRefs: workloadMountPlanSecretRefsForTest(),
			wantPlan: workloadmount.Plan{
				MountBindingID:      "wmb_123",
				VolumeID:            "vol_payload01",
				PayloadVolumeSubdir: workloadMountPlanPayloadSubdirForTest,
				MountPath:           "/mnt/repo",
				ReadOnly:            false,
				SecretRef:           workloadMountPlanSecretRefForTest(),
				SecurityPolicy:      workloadmount.SecurityPolicy{RunAsNonRoot: true, AllowPrivileged: false, JVSControlOutsidePayload: true},
			},
			sqlContracts: map[string][]string{
				"repoIdentity":  {"b.status = 'releasing'", "r.repo_kind = 'repo'"},
				"teardownTrack": {"false AS allow_privileged_workload", "b.status = 'releasing'", "EXISTS (SELECT 1 FROM teardown_namespace)"},
			},
			sqlForbidden: map[string][]string{
				"teardownTrack": {"held_lifecycle_fence", "active_repo", "active_volume", "active_binding"},
			},
		},
		{
			name:    "terminal binding statuses are not plan candidates",
			row:     fakeRow{err: sql.ErrNoRows},
			wantErr: sql.ErrNoRows,
			sqlContracts: map[string][]string{
				"candidate": {"status IN ('issued','pending','active','releasing')"},
			},
			sqlForbidden: map[string][]string{
				"candidate": {"released", "revoked", "expired", "failed"},
			},
		},
		{
			name:    "template repo identity is denied for issuance and teardown",
			row:     fakeRow{err: sql.ErrNoRows},
			wantErr: sql.ErrNoRows,
			sqlContracts: map[string][]string{
				"activeRepo":    {"r.repo_kind = 'repo'", "r.status = 'active'", "r.lifecycle_status = 'active'"},
				"repoIdentity":  {"r.repo_kind = 'repo'", "r.volume_id = b.volume_id"},
				"teardownTrack": {"FROM candidate_binding b, repo_identity r"},
			},
		},
		{
			name:    "missing jvs external control root volume capability is denied for issuance",
			row:     fakeRow{err: sql.ErrNoRows},
			wantErr: sql.ErrNoRows,
			sqlContracts: map[string][]string{
				"activeVolume":  {"COALESCE((v.capabilities->>'workload_mount')::boolean, false) = true", "COALESCE((v.capabilities->>'jvs_external_control_root')::boolean, false) = true"},
				"issuanceTrack": {"EXISTS (SELECT 1 FROM active_volume)"},
			},
		},
		{
			name:    "disabled workload mount policy is denied for issuance",
			row:     fakeRow{err: sql.ErrNoRows},
			wantErr: sql.ErrNoRows,
			sqlContracts: map[string][]string{
				"activeBinding": {"COALESCE((nvb.mount_policy->>'workload_mount_enabled')::boolean, false) = true", "COALESCE((nvb.mount_policy->>'workload_mount_requires_external_control_root')::boolean, false) = true"},
				"issuanceTrack": {"FROM candidate_binding b, active_repo r, active_binding nvb"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: tt.row}
			store := &Store{exec: exec, workloadMountRuntimeSecretRefs: tt.secretRefs}

			got, err := store.GetOrchestratorMountPlan(context.Background(), "ns_123", "wmb_123")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("GetOrchestratorMountPlan err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("GetOrchestratorMountPlan: %v", err)
			} else if got != tt.wantPlan {
				t.Fatalf("plan = %#v, want %#v", got, tt.wantPlan)
			}

			if exec.queryRowCalls != 1 || exec.execCalls != 0 || exec.queryCalls != 0 {
				t.Fatalf("calls queryRow/exec/query = %d/%d/%d, want 1/0/0", exec.queryRowCalls, exec.execCalls, exec.queryCalls)
			}
			if len(exec.args) != 2 || exec.args[0] != "ns_123" || exec.args[1] != "wmb_123" {
				t.Fatalf("args = %#v, want namespace and mount binding ids", exec.args)
			}
			if exec.query != workloadMountPlanSelectSQL() {
				t.Fatalf("query = %s, want workload mount plan SQL", exec.query)
			}

			for fragmentName, wantParts := range tt.sqlContracts {
				fragment, ok := fragments[fragmentName]
				if !ok {
					t.Fatalf("unknown SQL fragment %q", fragmentName)
				}
				for _, want := range wantParts {
					if !strings.Contains(fragment, want) {
						t.Fatalf("contract %q missing %q in fragment %s", fragmentName, want, fragment)
					}
				}
			}
			for fragmentName, forbiddenParts := range tt.sqlForbidden {
				fragment, ok := fragments[fragmentName]
				if !ok {
					t.Fatalf("unknown SQL fragment %q", fragmentName)
				}
				for _, forbidden := range forbiddenParts {
					if strings.Contains(fragment, forbidden) {
						t.Fatalf("contract %q contains forbidden %q in fragment %s", fragmentName, forbidden, fragment)
					}
				}
			}
		})
	}
}

func TestWorkloadMountPlanFailsClosedOnScannedIdentityMismatch(t *testing.T) {
	tests := []struct {
		name string
		row  fakeRow
	}{
		{
			name: "mismatched mount binding id",
			row:  fakeRow{values: workloadMountPlanRowForTest("wmb_other01", "vol_payload01", "repo_123", workloadMountPlanPayloadSubdirForTest, "/mnt/repo", true, false)},
		},
		{
			name: "bad volume id",
			row:  fakeRow{values: workloadMountPlanRowForTest("wmb_123", "payload01", "repo_123", workloadMountPlanPayloadSubdirForTest, "/mnt/repo", true, false)},
		},
		{
			name: "non canonical payload subdir",
			row:  fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_payload01", "repo_123", "payload/repo-alpha", "/mnt/repo", true, false)},
		},
		{
			name: "other repo payload subdir",
			row:  fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_payload01", "repo_123", "afscp/namespaces/ns_123/repos/repo_other01/payload", "/mnt/repo", true, false)},
		},
		{
			name: "bad mount path",
			row:  fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_payload01", "repo_123", workloadMountPlanPayloadSubdirForTest, "relative/path", true, false)},
		},
		{
			name: "missing runtime secret mapping",
			row:  fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_unmapped", "repo_123", workloadMountPlanPayloadSubdirForTest, "/mnt/repo", true, false)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: tt.row}
			store := &Store{exec: exec, workloadMountRuntimeSecretRefs: workloadMountPlanSecretRefsForTest()}

			got, err := store.GetOrchestratorMountPlan(context.Background(), "ns_123", "wmb_123")
			if err == nil {
				t.Fatalf("GetOrchestratorMountPlan err = nil, want fail-closed error with plan %#v", got)
			}
			if !reflect.DeepEqual(got, workloadmount.Plan{}) {
				t.Fatalf("plan = %#v, want zero plan on fail-closed error", got)
			}
			if exec.queryRowCalls != 1 || len(exec.args) != 2 || exec.args[0] != "ns_123" || exec.args[1] != "wmb_123" {
				t.Fatalf("query calls/args = %d/%#v, want scoped plan query", exec.queryRowCalls, exec.args)
			}
		})
	}
}

func TestWorkloadMountPlanFailsClosedOnInvalidRuntimeSecretMapping(t *testing.T) {
	tests := []struct {
		name       string
		secretRefs map[string]workloadmount.SecretRef
	}{
		{
			name:       "bad volume id in mapping",
			secretRefs: map[string]workloadmount.SecretRef{"payload01": workloadMountPlanSecretRefForTest()},
		},
		{
			name:       "bad secret ref in mapping",
			secretRefs: map[string]workloadmount.SecretRef{"vol_payload01": {Namespace: "Runtime-Secret-Namespace", Name: "runtime-secret-volume"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{row: fakeRow{values: workloadMountPlanRowForTest("wmb_123", "vol_payload01", "repo_123", workloadMountPlanPayloadSubdirForTest, "/mnt/repo", true, false)}}
			store := &Store{exec: exec, workloadMountRuntimeSecretRefs: tt.secretRefs}

			got, err := store.GetOrchestratorMountPlan(context.Background(), "ns_123", "wmb_123")
			if err == nil {
				t.Fatalf("GetOrchestratorMountPlan err = nil, want fail-closed error with plan %#v", got)
			}
			if !reflect.DeepEqual(got, workloadmount.Plan{}) {
				t.Fatalf("plan = %#v, want zero plan on fail-closed error", got)
			}
		})
	}
}

func TestWorkloadMountCreateCommitSQLHasDurableAdmissionGates(t *testing.T) {
	sql := workloadMountBindingCreateCommitSQL()

	assertSQLContainsInOrder(t, sql,
		"active_namespace AS (",
		"FROM namespaces",
		"namespace_id = $15",
		"status = 'active'",
		"active_binding AS (",
		"FROM namespace_volume_bindings",
		"namespace_id = $15",
		"status = 'active'",
		"workload_mount_enabled",
		"workload_mount_requires_external_control_root",
		"active_repo AS (",
		"FROM repos",
		"namespace_id = $15",
		"repo_id = $16",
		"volume_id = $17",
		"repo_kind = 'repo'",
		"status = 'active'",
		"lifecycle_status = 'active'",
		"FOR UPDATE",
		"active_volume AS (",
		"FROM volumes",
		"volume_id = $17",
		"status = 'active'",
		"workload_mount",
		"jvs_external_control_root",
		"held_lifecycle_fence AS (",
		"repo_fences.repo_id = active_repo.repo_id",
		"fence_kind = 'lifecycle'",
		"status IN ('active','expired','recovery_required')",
		"released_at IS NULL",
		"recovered_at IS NULL",
		"held_writer_fence AS (",
		"repo_fences.repo_id = active_repo.repo_id",
		"fence_kind = 'writer_session'",
		"status IN ('active','expired','recovery_required')",
		"released_at IS NULL",
		"recovered_at IS NULL",
		"updated_operation AS (",
		"namespace_id = $15",
		"repo_id = $16",
		"resource_type = 'workload_mount_binding'",
		"resource_id = $14",
		"input_summary @> jsonb_build_object",
		"'mount_binding_id', $14::text",
		"'namespace_id', $15::text",
		"'repo_id', $16::text",
		"'volume_id', $17::text",
		"'mount_path', $18::text",
		"'read_only', $19::boolean",
		"'lease_seconds', $21::integer",
		"EXISTS (SELECT 1 FROM active_namespace)",
		"EXISTS (SELECT 1 FROM active_binding)",
		"EXISTS (SELECT 1 FROM active_repo)",
		"EXISTS (SELECT 1 FROM active_volume)",
		"NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)",
		"($19 = true OR NOT EXISTS (SELECT 1 FROM held_writer_fence))",
	)
}

func TestWorkloadMountPlanAllowsDisabledNamespaceOnlyForReleaseTrack(t *testing.T) {
	sql := workloadMountPlanSelectSQL()

	assertSQLContainsInOrder(t, sql,
		"candidate_binding AS (",
		"FROM workload_mount_bindings",
		"namespace_id = $1",
		"mount_binding_id = $2",
		"status IN ('issued','pending','active','releasing')",
		"active_namespace AS (",
		"FROM namespaces ns, candidate_binding b",
		"ns.namespace_id = b.namespace_id",
		"ns.status = 'active'",
		"b.status IN ('issued','pending','active')",
		"teardown_namespace AS (",
		"FROM namespaces ns, candidate_binding b",
		"ns.namespace_id = b.namespace_id",
		"ns.status IN ('active','disabled')",
		"b.status = 'releasing'",
	)
}

func TestWorkloadMountPlanSQLHasDurableAdmissionGates(t *testing.T) {
	sql := workloadMountPlanSelectSQL()
	if strings.Contains(sql, "writer_session") || strings.Contains(sql, "held_writer_fence") {
		t.Fatalf("plan SQL must not add writer fence issuance gate: %s", sql)
	}
	if strings.Contains(sql, "FOR SHARE") || strings.Contains(sql, "FOR UPDATE") {
		t.Fatalf("plan SQL must not lock rows: %s", sql)
	}

	assertSQLContainsInOrder(t, sql,
		"candidate_binding AS (",
		"FROM workload_mount_bindings",
		"namespace_id = $1",
		"mount_binding_id = $2",
		"status IN ('issued','pending','active','releasing')",
		"active_namespace AS (",
		"FROM namespaces ns, candidate_binding b",
		"ns.namespace_id = b.namespace_id",
		"ns.status = 'active'",
		"b.status IN ('issued','pending','active')",
		"teardown_namespace AS (",
		"FROM namespaces ns, candidate_binding b",
		"ns.namespace_id = b.namespace_id",
		"ns.status IN ('active','disabled')",
		"b.status = 'releasing'",
		"active_binding AS (",
		"FROM namespace_volume_bindings nvb, candidate_binding b",
		"nvb.namespace_id = b.namespace_id",
		"b.status IN ('issued','pending','active')",
		"nvb.status = 'active'",
		"workload_mount_enabled",
		"workload_mount_requires_external_control_root",
		"active_repo AS (",
		"FROM repos r, candidate_binding b",
		"r.namespace_id = b.namespace_id",
		"r.repo_id = b.repo_id",
		"r.volume_id = b.volume_id",
		"b.status IN ('issued','pending','active')",
		"r.repo_kind = 'repo'",
		"r.status = 'active'",
		"r.lifecycle_status = 'active'",
		"repo_identity AS (",
		"FROM repos r, candidate_binding b",
		"r.namespace_id = b.namespace_id",
		"r.repo_id = b.repo_id",
		"r.volume_id = b.volume_id",
		"b.status = 'releasing'",
		"r.repo_kind = 'repo'",
		"active_volume AS (",
		"FROM volumes v, candidate_binding b",
		"v.volume_id = b.volume_id",
		"b.status IN ('issued','pending','active')",
		"v.status = 'active'",
		"workload_mount",
		"jvs_external_control_root",
		"held_lifecycle_fence AS (",
		"repo_fences.repo_id = active_repo.repo_id",
		"fence_kind = 'lifecycle'",
		"status IN ('active','expired','recovery_required')",
		"released_at IS NULL",
		"recovered_at IS NULL",
		"issuance_track AS (",
		"SELECT b.mount_binding_id, b.volume_id, r.repo_id, r.payload_volume_subdir",
		"allow_privileged_workload",
		"FROM candidate_binding b, active_repo r, active_binding nvb",
		"b.status IN ('issued','pending','active')",
		"b.lease_expires_at > now()",
		"EXISTS (SELECT 1 FROM active_namespace)",
		"EXISTS (SELECT 1 FROM active_volume)",
		"NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)",
		"teardown_track AS (",
		"SELECT b.mount_binding_id, b.volume_id, r.repo_id, r.payload_volume_subdir",
		"false AS allow_privileged_workload",
		"FROM candidate_binding b, repo_identity r",
		"b.status = 'releasing'",
		"EXISTS (SELECT 1 FROM teardown_namespace)",
		"SELECT * FROM issuance_track UNION ALL SELECT * FROM teardown_track",
	)
}

func TestWorkloadMountPlanTeardownTrackDoesNotDependOnIssuanceGates(t *testing.T) {
	sql := workloadMountPlanSelectSQL()
	teardown := sqlBetween(t, sql, "teardown_track AS (", ") SELECT * FROM issuance_track")

	assertSQLContainsInOrder(t, teardown,
		"false AS allow_privileged_workload",
		"FROM candidate_binding b, repo_identity r",
		"b.status = 'releasing'",
		"EXISTS (SELECT 1 FROM teardown_namespace)",
	)

	for _, forbidden := range []string{
		"active_binding",
		"active_volume",
		"active_repo",
		"held_lifecycle_fence",
		"mount_policy",
		"workload_mount_enabled",
		"workload_mount_requires_external_control_root",
		"lifecycle_status",
		"capabilities",
		"lease_expires_at > now()",
	} {
		if strings.Contains(teardown, forbidden) {
			t.Fatalf("teardown track must not depend on %q: %s", forbidden, teardown)
		}
	}
}

func TestWorkloadMountPlanTeardownRepoIdentityDoesNotRequireActiveLifecycle(t *testing.T) {
	sql := workloadMountPlanSelectSQL()
	repoIdentity := sqlBetween(t, sql, "repo_identity AS (", "), active_volume AS (")

	assertSQLContainsInOrder(t, repoIdentity,
		"FROM repos r, candidate_binding b",
		"r.namespace_id = b.namespace_id",
		"r.repo_id = b.repo_id",
		"r.volume_id = b.volume_id",
		"b.status = 'releasing'",
		"r.repo_kind = 'repo'",
	)

	for _, forbidden := range []string{
		"r.status = 'active'",
		"lifecycle_status = 'active'",
		"active_binding",
		"active_volume",
		"held_lifecycle_fence",
		"mount_policy",
		"capabilities",
	} {
		if strings.Contains(repoIdentity, forbidden) {
			t.Fatalf("teardown repo identity must not depend on %q: %s", forbidden, repoIdentity)
		}
	}
}

func TestWorkloadMountPlanTrackSemanticsContract(t *testing.T) {
	sql := workloadMountPlanSelectSQL()
	issuance := sqlBetween(t, sql, "issuance_track AS (", "), teardown_track AS (")
	teardown := sqlBetween(t, sql, "teardown_track AS (", ") SELECT * FROM issuance_track")
	teardownNamespace := sqlBetween(t, sql, "teardown_namespace AS (", "), active_binding AS (")
	repoIdentity := sqlBetween(t, sql, "repo_identity AS (", "), active_volume AS (")

	tests := []struct {
		name      string
		track     string
		want      []string
		forbidden []string
		contract  string
	}{
		{
			name:  "active binding behind lifecycle fence is denied",
			track: issuance,
			want: []string{
				"b.status IN ('issued','pending','active')",
				"b.lease_expires_at > now()",
				"NOT EXISTS (SELECT 1 FROM held_lifecycle_fence)",
			},
			contract: "active and pending issuance must stop when lease is stale or a lifecycle fence is held",
		},
		{
			name:  "releasing binding behind lifecycle fence or stale lease still receives teardown plan",
			track: teardown,
			want: []string{
				"b.status = 'releasing'",
				"false AS allow_privileged_workload",
			},
			forbidden: []string{
				"held_lifecycle_fence",
				"lease_expires_at > now()",
			},
			contract: "teardown plans bypass issuance freshness and lifecycle fence gates and force unprivileged workload policy",
		},
		{
			name:  "releasing binding in disabled namespace still receives teardown plan",
			track: teardownNamespace,
			want: []string{
				"ns.status IN ('active','disabled')",
				"b.status = 'releasing'",
			},
			contract: "disabled namespaces remain eligible only for releasing teardown",
		},
		{
			name:  "releasing binding with disabled namespace policy still receives teardown plan",
			track: teardown,
			want: []string{
				"b.status = 'releasing'",
				"EXISTS (SELECT 1 FROM teardown_namespace)",
			},
			forbidden: []string{
				"active_binding",
				"mount_policy",
				"workload_mount_enabled",
			},
			contract: "namespace mount policy is an issuance gate, not a teardown gate",
		},
		{
			name:  "releasing binding for inactive repo lifecycle still receives teardown plan",
			track: repoIdentity,
			want: []string{
				"b.status = 'releasing'",
				"r.repo_kind = 'repo'",
			},
			forbidden: []string{
				"r.status = 'active'",
				"r.lifecycle_status = 'active'",
			},
			contract: "repo lifecycle status is an issuance gate, while teardown requires only repo identity",
		},
		{
			name:  "releasing binding with inactive volume still receives teardown plan",
			track: teardown,
			want: []string{
				"b.status = 'releasing'",
				"FROM candidate_binding b, repo_identity r",
			},
			forbidden: []string{
				"active_volume",
				"capabilities",
				"jvs_external_control_root",
			},
			contract: "volume status and capability checks are issuance gates, not teardown gates",
		},
		{
			name:  "releasing binding with template repo identity is denied",
			track: repoIdentity,
			want: []string{
				"r.repo_kind = 'repo'",
			},
			contract: "teardown still rejects non-repo identities",
		},
		{
			name:  "releasing binding with mismatched repo volume identity is denied",
			track: repoIdentity,
			want: []string{
				"r.volume_id = b.volume_id",
			},
			contract: "teardown still binds the repo and binding to the same volume identity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, want := range tt.want {
				if !strings.Contains(tt.track, want) {
					t.Fatalf("%s: missing %q in SQL fragment %q", tt.contract, want, tt.track)
				}
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(tt.track, forbidden) {
					t.Fatalf("%s: forbidden %q in SQL fragment %q", tt.contract, forbidden, tt.track)
				}
			}
		})
	}
}

func TestWorkloadMountUpdateCommitSQLBindsOperationAndBindingBoundary(t *testing.T) {
	for _, sql := range []string{
		workloadMountBindingStatusCommitSQL(),
		workloadMountBindingHeartbeatCommitSQL(),
		workloadMountBindingReleaseCommitSQL(),
		workloadMountBindingRevokeCommitSQL(),
	} {
		assertSQLContainsInOrder(t, sql,
			"updated_operation AS (",
			"mount_binding_id = $14",
			"resource_type = 'workload_mount_binding'",
			"resource_id = $14",
			"UPDATE workload_mount_bindings SET",
			"FROM updated_operation",
			"workload_mount_bindings.mount_binding_id = $14",
			"workload_mount_bindings.namespace_id = updated_operation.namespace_id",
			"workload_mount_bindings.repo_id = updated_operation.repo_id",
		)
	}
}

func TestWorkloadMountUpdateCommitSQLQualifiesBindingLeaseSources(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "status",
			sql:  workloadMountBindingStatusCommitSQL(),
			want: []string{
				"WHEN workload_mount_bindings.status IN ('released','revoked','expired','failed') THEN workload_mount_bindings.lease_expires_at",
				"WHEN workload_mount_bindings.status = 'releasing' AND $15 IN ('pending','active') THEN workload_mount_bindings.lease_expires_at",
				"ELSE workload_mount_bindings.lease_expires_at END",
			},
		},
		{
			name: "heartbeat",
			sql:  workloadMountBindingHeartbeatCommitSQL(),
			want: []string{
				"last_heartbeat_at = CASE WHEN workload_mount_bindings.status IN ('released','revoked','expired','failed','releasing') THEN workload_mount_bindings.last_heartbeat_at ELSE $15 END",
				"lease_expires_at = CASE WHEN workload_mount_bindings.status IN ('released','revoked','expired','failed','releasing') THEN workload_mount_bindings.lease_expires_at ELSE $15 + (workload_mount_bindings.lease_seconds || ' seconds')::interval END",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updatedBinding := sqlBetween(t, tt.sql, "), updated_binding AS (", "), inserted_audit AS (")
			for _, want := range tt.want {
				if !strings.Contains(updatedBinding, want) {
					t.Fatalf("workload mount %s commit SQL missing qualified binding source %q in %s", tt.name, want, updatedBinding)
				}
			}
			for _, forbidden := range []string{
				"THEN lease_expires_at",
				"ELSE lease_expires_at END",
				"(lease_seconds || ' seconds')",
			} {
				if strings.Contains(updatedBinding, forbidden) {
					t.Fatalf("workload mount %s commit SQL has ambiguous binding source %q in %s", tt.name, forbidden, updatedBinding)
				}
			}
		})
	}
}

func TestWorkloadMountUpdateCommitSQLUsesOperationSpecificParameterShape(t *testing.T) {
	heartbeatSQL := workloadMountBindingHeartbeatCommitSQL()
	heartbeatBinding := sqlBetween(t, heartbeatSQL, "), updated_binding AS (", "), inserted_audit AS (")
	assertSQLContainsInOrder(t, heartbeatBinding,
		"last_heartbeat_at = CASE",
		"ELSE $15 END",
		"lease_expires_at = CASE",
		"ELSE $15 + (workload_mount_bindings.lease_seconds || ' seconds')::interval END",
	)
	for _, forbidden := range []string{"$16", "$17", "$18"} {
		if strings.Contains(heartbeatBinding, forbidden) {
			t.Fatalf("heartbeat binding update uses non-heartbeat parameter %s in %s", forbidden, heartbeatBinding)
		}
	}
	assertSQLContainsInOrder(t, heartbeatSQL,
		"), inserted_audit AS (",
		"SELECT "+placeholders(16, len(auditOutboxColumns)),
	)

	for _, tt := range []struct {
		name string
		sql  string
	}{
		{name: "release", sql: workloadMountBindingReleaseCommitSQL()},
		{name: "revoke", sql: workloadMountBindingRevokeCommitSQL()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			updatedBinding := sqlBetween(t, tt.sql, "), updated_binding AS (", "), inserted_audit AS (")
			assertSQLContainsInOrder(t, updatedBinding,
				"last_observed_at = $16",
				"status_reason = CASE",
				"ELSE $15 END",
			)
			for _, forbidden := range []string{"$17", "$18"} {
				if strings.Contains(updatedBinding, forbidden) {
					t.Fatalf("%s binding update uses non-%s parameter %s in %s", tt.name, tt.name, forbidden, updatedBinding)
				}
			}
			assertSQLContainsInOrder(t, tt.sql,
				"), inserted_audit AS (",
				"SELECT "+placeholders(17, len(auditOutboxColumns)),
			)
		})
	}
}

func TestWorkloadMountStatusCommitSQLPreservesRevokeIntent(t *testing.T) {
	sql := workloadMountBindingStatusCommitSQL()

	assertSQLContainsInOrder(t, sql,
		"status = CASE",
		"WHEN workload_mount_bindings.status IN ('released','revoked','expired','failed') THEN workload_mount_bindings.status",
		"WHEN workload_mount_bindings.status = 'releasing' AND $15 IN ('pending','active') THEN workload_mount_bindings.status",
		"ELSE $15 END",
		"last_observed_at = CASE",
		"WHEN workload_mount_bindings.status = 'releasing' AND $15 IN ('pending','active') THEN workload_mount_bindings.last_observed_at",
		"lease_expires_at = CASE",
		"WHEN workload_mount_bindings.status = 'releasing' AND $15 IN ('pending','active') THEN workload_mount_bindings.lease_expires_at",
	)
}

func TestWorkloadMountStatusCommitSQLContractTreatsReleasedRevokedAsEvidenceOnly(t *testing.T) {
	sql := workloadMountBindingStatusCommitSQL()
	terminalObserved := sqlBetween(t, sql, "terminal_observed_at = ", ", confirmed_unmounted_at = ")
	confirmedUnmounted := sqlBetween(t, sql, "confirmed_unmounted_at = ", ", unable_to_write_at = ")
	unableToWrite := sqlBetween(t, sql, "unable_to_write_at = ", ", status_reason = ")

	assertSQLContainsInOrder(t, terminalObserved,
		"$15 IN ('released','revoked','expired','failed')",
		"workload_mount_bindings.status NOT IN ('released','revoked','expired','failed')",
		"THEN $17",
		"ELSE workload_mount_bindings.terminal_observed_at END",
	)
	assertSQLContainsInOrder(t, confirmedUnmounted,
		"$15 IN ('released','revoked')",
		"workload_mount_bindings.status NOT IN ('released','revoked','expired','failed')",
		"THEN $17",
		"ELSE workload_mount_bindings.confirmed_unmounted_at END",
	)
	assertSQLContainsInOrder(t, unableToWrite,
		"$15 IN ('released','revoked')",
		"workload_mount_bindings.status NOT IN ('released','revoked','expired','failed')",
		"THEN $17",
		"ELSE workload_mount_bindings.unable_to_write_at END",
	)
	for name, fragment := range map[string]string{"confirmed_unmounted_at": confirmedUnmounted, "unable_to_write_at": unableToWrite} {
		if strings.Contains(fragment, "$15 IN ('released','revoked','expired','failed')") {
			t.Fatalf("%s clause must not write evidence for bare expired/failed status: %s", name, fragment)
		}
	}
}

func TestWorkloadMountPlanSQLExcludesTerminalBindings(t *testing.T) {
	sql := workloadMountPlanSelectSQL()

	assertSQLContainsInOrder(t, sql,
		"candidate_binding AS (",
		"FROM workload_mount_bindings",
		"namespace_id = $1",
		"mount_binding_id = $2",
		"status IN ('issued','pending','active','releasing')",
	)
}

func TestWorkloadMountCommitRejectsBadAuditBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	binding := workloadMountBindingFixture(now)

	tests := []struct {
		name  string
		typ   operations.OperationType
		phase string
		event audit.EventType
		call  func(*Store, operations.OperationRecord, audit.Event) error
	}{
		{name: "create", typ: operations.OperationMountBindingCreate, phase: operations.OperationPhaseMountBindingCreateCommitted, event: audit.EventTypeMountBindingCreate, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingCreateWithLease(context.Background(), binding, record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "status", typ: operations.OperationMountBindingStatusUpdate, phase: operations.OperationPhaseMountBindingStatusCommitted, event: audit.EventTypeMountBindingStatusUpdate, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingStatusWithLease(context.Background(), "wmb_123", sessionstate.MountStatusActive, "mounted", now, nil, record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "heartbeat", typ: operations.OperationMountBindingHeartbeat, phase: operations.OperationPhaseMountBindingHeartbeatCommitted, event: audit.EventTypeMountBindingHeartbeat, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingHeartbeatWithLease(context.Background(), "wmb_123", record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "release", typ: operations.OperationMountBindingRelease, phase: operations.OperationPhaseMountBindingReleaseCommitted, event: audit.EventTypeMountBindingRelease, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingReleaseWithLease(context.Background(), "wmb_123", record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
		{name: "revoke", typ: operations.OperationMountBindingRevoke, phase: operations.OperationPhaseMountBindingRevokeCommitted, event: audit.EventTypeMountBindingRevoke, call: func(store *Store, record operations.OperationRecord, event audit.Event) error {
			_, _, err := store.CommitWorkloadMountBindingRevokeWithLease(context.Background(), "wmb_123", record.SanitizedForPersistence(), "worker-a", now, event)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			store := &Store{exec: exec}
			record := workloadMountOperationRecordForTest(now, tt.typ, tt.phase)
			event := workloadMountAuditEventForTest(record, tt.event, now)
			event.OperationID = "op_other"

			err := tt.call(store, record, event)
			if err == nil {
				t.Fatal("commit err = nil, want invalid audit error")
			}
			if exec.queryRowCalls != 0 {
				t.Fatalf("query row calls = %d, want no SQL before audit validation passes", exec.queryRowCalls)
			}
		})
	}
}

func TestWorkloadMountStatusCommitRejectsNonOrchestratorStatusesBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	for _, status := range []sessionstate.MountStatus{sessionstate.MountStatusIssued, sessionstate.MountStatusReleasing} {
		t.Run(string(status), func(t *testing.T) {
			exec := &fakeExecutor{}
			store := &Store{exec: exec}
			record := workloadMountOperationRecordForTest(now, operations.OperationMountBindingStatusUpdate, operations.OperationPhaseMountBindingStatusCommitted)
			event := workloadMountAuditEventForTest(record, audit.EventTypeMountBindingStatusUpdate, now)

			_, _, err := store.CommitWorkloadMountBindingStatusWithLease(context.Background(), "wmb_123", status, "", now, nil, record.SanitizedForPersistence(), "worker-a", now, event)
			if err == nil {
				t.Fatal("CommitWorkloadMountBindingStatusWithLease err = nil, want invalid status")
			}
			if exec.queryRowCalls != 0 {
				t.Fatalf("query row calls = %d, want no SQL before status validation passes", exec.queryRowCalls)
			}
		})
	}
}

func TestWorkloadMountStatusCommitRejectsReasonOverMaxLengthBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{}
	store := &Store{exec: exec}
	record := workloadMountOperationRecordForTest(now, operations.OperationMountBindingStatusUpdate, operations.OperationPhaseMountBindingStatusCommitted)
	event := workloadMountAuditEventForTest(record, audit.EventTypeMountBindingStatusUpdate, now)

	_, _, err := store.CommitWorkloadMountBindingStatusWithLease(context.Background(), "wmb_123", sessionstate.MountStatusActive, strings.Repeat("x", workloadmount.MaxReasonLength+1), now, nil, record.SanitizedForPersistence(), "worker-a", now, event)
	if err == nil {
		t.Fatal("CommitWorkloadMountBindingStatusWithLease err = nil, want invalid reason")
	}
	if exec.queryRowCalls != 0 {
		t.Fatalf("query row calls = %d, want no SQL before reason validation passes", exec.queryRowCalls)
	}
}

func workloadMountPlanRowForTest(mountBindingID, volumeID, repoID, payloadVolumeSubdir, mountPath string, readOnly, allowPrivileged bool) []any {
	return []any{mountBindingID, volumeID, repoID, payloadVolumeSubdir, mountPath, readOnly, allowPrivileged}
}

func workloadMountPlanSecretRefForTest() workloadmount.SecretRef {
	return workloadmount.SecretRef{Namespace: "runtime-secret-namespace", Name: "runtime-secret-volume"}
}

func workloadMountPlanSecretRefsForTest() map[string]workloadmount.SecretRef {
	return map[string]workloadmount.SecretRef{"vol_payload01": workloadMountPlanSecretRefForTest()}
}

func workloadMountPlanSQLFragmentsForTest(t *testing.T) map[string]string {
	t.Helper()
	sql := workloadMountPlanSelectSQL()
	return map[string]string{
		"fullPlanSelect":     sql,
		"candidate":          sqlBetween(t, sql, "candidate_binding AS (", "), active_namespace AS ("),
		"activeNamespace":    sqlBetween(t, sql, "active_namespace AS (", "), teardown_namespace AS ("),
		"teardownNamespace":  sqlBetween(t, sql, "teardown_namespace AS (", "), active_binding AS ("),
		"activeBinding":      sqlBetween(t, sql, "active_binding AS (", "), active_repo AS ("),
		"activeRepo":         sqlBetween(t, sql, "active_repo AS (", "), repo_identity AS ("),
		"repoIdentity":       sqlBetween(t, sql, "repo_identity AS (", "), active_volume AS ("),
		"activeVolume":       sqlBetween(t, sql, "active_volume AS (", "), held_lifecycle_fence AS ("),
		"heldLifecycleFence": sqlBetween(t, sql, "held_lifecycle_fence AS (", "), issuance_track AS ("),
		"issuanceTrack":      sqlBetween(t, sql, "issuance_track AS (", "), teardown_track AS ("),
		"teardownTrack":      sqlBetween(t, sql, "teardown_track AS (", ") SELECT * FROM issuance_track"),
	}
}

func sqlBetween(t *testing.T, sql, start, end string) string {
	t.Helper()
	startIdx := strings.Index(sql, start)
	if startIdx < 0 {
		t.Fatalf("SQL %q missing start marker %q", sql, start)
	}
	startIdx += len(start)
	endIdx := strings.Index(sql[startIdx:], end)
	if endIdx < 0 {
		t.Fatalf("SQL %q missing end marker %q after %q", sql, end, start)
	}
	return sql[startIdx : startIdx+endIdx]
}

func workloadMountBindingFixture(now time.Time) workloadmount.Binding {
	return workloadmount.Binding{
		ID:             "wmb_123",
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		VolumeID:       "vol_123",
		MountPath:      "/mnt/repo",
		ReadOnly:       false,
		Status:         sessionstate.MountStatusIssued,
		LeaseSeconds:   120,
		LeaseExpiresAt: now.Add(120 * time.Second),
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now,
	}
}

func workloadMountOperationRecordForTest(now time.Time, typ operations.OperationType, phase string) operations.OperationRecord {
	finishedAt := now
	leaseExpiresAt := now.Add(5 * time.Minute)
	return operations.OperationRecord{
		ID:               "op_mount",
		Type:             typ,
		State:            operations.OperationStateSucceeded,
		Phase:            phase,
		Attempt:          1,
		LeaseOwner:       "worker-a",
		LeaseExpiresAt:   &leaseExpiresAt,
		IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_123", typ, "idem_mount").String(),
		IdempotencyKey:   "idem_mount",
		RequestHash:      "sha256:mount",
		CorrelationID:    "corr_mount",
		CallerService:    "product-caller",
		AuthorizedActor:  operations.Actor{Type: "service", ID: "product-caller"},
		Resource:         operations.ResourceRef{Type: "workload_mount_binding", ID: "wmb_123"},
		NamespaceID:      "ns_123",
		RepoID:           "repo_123",
		MountBindingID:   "wmb_123",
		InputSummary:     map[string]any{"mount_binding_id": "wmb_123"},
		CreatedAt:        now.Add(-time.Minute),
		FinishedAt:       &finishedAt,
	}
}

func workloadMountAuditEventForTest(record operations.OperationRecord, typ audit.EventType, now time.Time) audit.Event {
	return audit.Event{
		EventID:         "evt_mount",
		Type:            typ,
		Time:            now,
		CallerService:   record.CallerService,
		AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: "workload_mount_binding", ID: record.MountBindingID, NamespaceID: record.NamespaceID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          record.Phase,
		Details:         map[string]any{"mount_binding_id": record.MountBindingID, "repo_id": record.RepoID},
	}
}
