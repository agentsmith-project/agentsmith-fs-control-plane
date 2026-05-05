package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsRepoRecoveryInspectionReader(t *testing.T) {
	var _ store.RepoRecoveryInspectionReader = (*Store)(nil)
}

func TestRepoRecoveryInspectionReaderGetsRepoMetadataByIDReadOnly(t *testing.T) {
	repo := repoFixture()
	exec := &fakeExecutor{
		row: fakeRow{values: repoRowValues(repo)},
	}
	st := &Store{exec: exec}

	got, err := st.GetRepo(context.Background(), "repo_alpha01")
	if err != nil {
		t.Fatalf("GetRepo through RepoRecoveryInspectionReader: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"repo_id", "namespace_id", "volume_id", "jvs_repo_id", "repo_kind", "status",
		"FROM repos",
		"WHERE repo_id = $1",
	)
	assertReadOnlyInspectionSQL(t, exec.query)
	if len(exec.args) != 1 || exec.args[0] != "repo_alpha01" {
		t.Fatalf("args = %#v, want repo_alpha01", exec.args)
	}
	if got.ID != "repo_alpha01" || got.NamespaceID != "ns_alpha01" || got.Status != resources.RepoStatusActive {
		t.Fatalf("repo = %#v, want active repo_alpha01 metadata", got)
	}
}

func TestListReposForRecoveryInspectionReadsOnlyCandidateLifecycleStatuses(t *testing.T) {
	archiving := repoFixture()
	archiving.Status = resources.RepoStatusArchiving
	archiving.Lifecycle = resources.RepoLifecycle{
		Status:                   resources.RepoStatusArchiving,
		LastLifecycleOperationID: "op_archive01",
	}
	deleting := repoFixture()
	retention := deleting.CreatedAt.Add(24 * time.Hour)
	deleting.Status = resources.RepoStatusDeleting
	deleting.Lifecycle = resources.RepoLifecycle{
		Status:                   resources.RepoStatusDeleting,
		RetentionExpiresAt:       &retention,
		LastLifecycleOperationID: "op_delete01",
		PreDeleteStatus:          resources.RepoStatusActive,
	}
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{
			{values: repoRowValues(archiving)},
			{values: repoRowValues(deleting)},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.ListReposForRecoveryInspection(context.Background())
	if err != nil {
		t.Fatalf("ListReposForRecoveryInspection: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"repo_id", "namespace_id", "volume_id", "jvs_repo_id", "repo_kind", "status",
		"FROM repos",
		"WHERE lifecycle_status IN",
		"ORDER BY updated_at, repo_id",
	)
	assertReadOnlyInspectionSQL(t, exec.query)
	wantStatuses := []resources.RepoStatus{
		resources.RepoStatusArchiving,
		resources.RepoStatusRestoringArchived,
		resources.RepoStatusDeleting,
		resources.RepoStatusRestoringTombstoned,
		resources.RepoStatusPurging,
		resources.RepoStatusOperatorInterventionRequired,
	}
	if len(exec.args) != len(wantStatuses) {
		t.Fatalf("args = %#v, want %d candidate statuses", exec.args, len(wantStatuses))
	}
	for idx, status := range wantStatuses {
		if exec.args[idx] != string(status) {
			t.Fatalf("candidate arg %d = %#v, want %s", idx+1, exec.args[idx], status)
		}
	}
	for _, forbidden := range []resources.RepoStatus{resources.RepoStatusActive, resources.RepoStatusArchived, resources.RepoStatusTombstoned, resources.RepoStatusPurged} {
		for _, arg := range exec.args {
			if arg == string(forbidden) {
				t.Fatalf("candidate args include stable status %s: %#v", forbidden, exec.args)
			}
		}
	}
	if !exec.rows.closed {
		t.Fatal("rows were not closed")
	}
	if len(got) != 2 || got[0].Status != resources.RepoStatusArchiving || got[1].Status != resources.RepoStatusDeleting {
		t.Fatalf("repos = %#v, want scanned candidate repos", got)
	}
}

func TestListAllHeldRepoFencesReadsOnlyAcrossRepos(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{
			{values: repoFenceRowValues(repoFenceFixture(now, now.Add(30*time.Minute)))},
			{values: repoFenceRowValues(fences.Fence{
				ID:                "fence_beta",
				RepoID:            "repo_beta",
				Kind:              fences.KindLifecycle,
				HolderOperationID: "op_beta",
				Status:            fences.StatusRecoveryRequired,
				ExpiresAt:         now.Add(15 * time.Minute),
				CreatedAt:         now.Add(time.Minute),
				UpdatedAt:         now.Add(time.Minute),
			})},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.ListAllHeldRepoFences(context.Background())
	if err != nil {
		t.Fatalf("ListAllHeldRepoFences: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"fence_id", "repo_id", "fence_kind", "holder_operation_id", "status", "expires_at",
		"FROM repo_fences",
		"WHERE released_at IS NULL",
		"ORDER BY repo_id, created_at, fence_id",
	)
	assertReadOnlyInspectionSQL(t, exec.query)
	if len(exec.args) != 0 {
		t.Fatalf("args = %#v, want none", exec.args)
	}
	if !exec.rows.closed {
		t.Fatal("rows were not closed")
	}
	if len(got) != 2 || got[0].RepoID != "repo_alpha" || got[1].RepoID != "repo_beta" {
		t.Fatalf("fences = %#v, want all held repo fences", got)
	}
}

func TestRepoRecoveryInspectionReadersPropagateRowsErrCloseErrAndScanValidation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	validRepo := repoFixture()
	validFence := repoFenceFixture(now, now.Add(30*time.Minute))

	tests := []struct {
		name string
		call func(*Store) error
		rows fakeRows
	}{
		{
			name: "repo rows err",
			call: func(st *Store) error {
				_, err := st.ListReposForRecoveryInspection(context.Background())
				return err
			},
			rows: fakeRows{
				rows: []fakeRow{{values: repoRowValues(validRepo)}},
				err:  errors.New("repo rows failed"),
			},
		},
		{
			name: "repo close err",
			call: func(st *Store) error {
				_, err := st.ListReposForRecoveryInspection(context.Background())
				return err
			},
			rows: fakeRows{
				rows:     []fakeRow{{values: repoRowValues(validRepo)}},
				closeErr: errors.New("repo close failed"),
			},
		},
		{
			name: "repo scan validation err",
			call: func(st *Store) error {
				_, err := st.ListReposForRecoveryInspection(context.Background())
				return err
			},
			rows: fakeRows{
				rows: []fakeRow{{values: repoRowValues(resources.Repo{ID: "repo_alpha01"})}},
			},
		},
		{
			name: "fence rows err",
			call: func(st *Store) error {
				_, err := st.ListAllHeldRepoFences(context.Background())
				return err
			},
			rows: fakeRows{
				rows: []fakeRow{{values: repoFenceRowValues(validFence)}},
				err:  errors.New("fence rows failed"),
			},
		},
		{
			name: "fence close err",
			call: func(st *Store) error {
				_, err := st.ListAllHeldRepoFences(context.Background())
				return err
			},
			rows: fakeRows{
				rows:     []fakeRow{{values: repoFenceRowValues(validFence)}},
				closeErr: errors.New("fence close failed"),
			},
		},
		{
			name: "fence scan validation err",
			call: func(st *Store) error {
				_, err := st.ListAllHeldRepoFences(context.Background())
				return err
			},
			rows: fakeRows{
				rows: []fakeRow{{values: repoFenceRowValues(fences.Fence{ID: "fence_alpha"})}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{rows: tt.rows}
			st := &Store{exec: exec}

			err := tt.call(st)
			if err == nil {
				t.Fatal("inspection reader succeeded, want error")
			}
			if !exec.rows.closed {
				t.Fatal("rows were not closed after error")
			}
		})
	}
}

func assertReadOnlyInspectionSQL(t *testing.T, query string) {
	t.Helper()
	lower := strings.ToLower(query)
	for _, forbidden := range []string{
		"insert ", "update ", "delete ", "truncate ", "returning",
		"from jvs", "join jvs", "webdav", "mount", "storage", "audit_outbox",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("inspection query contains forbidden term %q: %s", forbidden, query)
		}
	}
}
