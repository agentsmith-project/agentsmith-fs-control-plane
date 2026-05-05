package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsRepoFenceContracts(t *testing.T) {
	var _ store.RepoFenceReader = (*Store)(nil)
	var _ store.RepoFenceWriter = (*Store)(nil)
	var _ store.RepoFenceStore = (*Store)(nil)
}

func TestCreateRepoFenceInsertsRepoFenceWithExpiresAt(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	createdAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(30 * time.Minute)
	fence := repoFenceFixture(createdAt, expiresAt)

	if err := st.CreateRepoFence(context.Background(), fence); err != nil {
		t.Fatalf("CreateRepoFence: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO repo_fences",
		"fence_id", "repo_id", "fence_kind", "holder_operation_id", "status", "expires_at",
		"released_at", "recovery_operation_id", "recovery_reason", "recovery_started_at", "recovered_at",
		"created_at", "updated_at",
	)
	if len(exec.args) != len(repoFenceColumns) {
		t.Fatalf("arg count = %d, want %d: %#v", len(exec.args), len(repoFenceColumns), exec.args)
	}
	wantPrefix := []any{
		"fence_alpha",
		"repo_alpha",
		string(fences.KindWriterSession),
		"op_alpha",
		string(fences.StatusActive),
		expiresAt,
	}
	for idx, want := range wantPrefix {
		if exec.args[idx] != want {
			t.Fatalf("arg %d = %#v, want %#v", idx+1, exec.args[idx], want)
		}
	}
	if exec.args[6] != nil || exec.args[7] != nil || exec.args[9] != nil || exec.args[10] != nil {
		t.Fatalf("nullable release/recovery args = %#v", exec.args[6:11])
	}
	if exec.args[11] != createdAt || exec.args[12] != createdAt {
		t.Fatalf("created/updated args = %#v/%#v, want created_at", exec.args[11], exec.args[12])
	}
}

func TestCreateRepoFenceRejectsMissingDurableTimestampsBeforeInsert(t *testing.T) {
	createdAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(30 * time.Minute)

	tests := []struct {
		name  string
		fence fences.Fence
	}{
		{name: "missing expires_at", fence: repoFenceFixture(createdAt, time.Time{})},
		{name: "missing created_at", fence: func() fences.Fence {
			fence := repoFenceFixture(createdAt, expiresAt)
			fence.CreatedAt = time.Time{}
			return fence
		}()},
		{name: "missing updated_at", fence: func() fences.Fence {
			fence := repoFenceFixture(createdAt, expiresAt)
			fence.UpdatedAt = time.Time{}
			return fence
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			err := st.CreateRepoFence(context.Background(), tt.fence)
			if err == nil {
				t.Fatal("CreateRepoFence succeeded, want validation error")
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL for invalid fence", exec.query)
			}
		})
	}
}

func TestListHeldRepoFencesReadsOnlyUnreleasedRepoFences(t *testing.T) {
	createdAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(30 * time.Minute)
	secondCreatedAt := createdAt.Add(time.Minute)
	secondExpiresAt := secondCreatedAt.Add(30 * time.Minute)
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{
			{values: repoFenceRowValues(repoFenceFixture(createdAt, expiresAt))},
			{values: repoFenceRowValues(fences.Fence{
				ID:                "fence_beta",
				RepoID:            "repo_alpha",
				Kind:              fences.KindLifecycle,
				HolderOperationID: "op_beta",
				Status:            fences.StatusExpired,
				ExpiresAt:         secondExpiresAt,
				CreatedAt:         secondCreatedAt,
				UpdatedAt:         secondCreatedAt,
			})},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.ListHeldRepoFences(context.Background(), "repo_alpha")
	if err != nil {
		t.Fatalf("ListHeldRepoFences: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"fence_id", "repo_id", "fence_kind", "holder_operation_id", "status", "expires_at",
		"FROM repo_fences",
		"WHERE repo_id = $1",
		"released_at IS NULL",
		"ORDER BY created_at, fence_id",
	)
	if strings.Contains(strings.ToLower(exec.query), "json") {
		t.Fatalf("query = %s, must not use JSON aggregation", exec.query)
	}
	for _, forbidden := range []string{"operations", "audit_outbox", "jvs", "webdav", "mount", "storage"} {
		if strings.Contains(strings.ToLower(exec.query), forbidden) {
			t.Fatalf("query = %s, must not touch %q", exec.query, forbidden)
		}
	}
	if len(exec.args) != 1 || exec.args[0] != "repo_alpha" {
		t.Fatalf("args = %#v, want repo_alpha", exec.args)
	}
	if !exec.rows.closed {
		t.Fatal("rows were not closed")
	}
	if len(got) != 2 {
		t.Fatalf("fence count = %d, want 2", len(got))
	}
	if got[0].ID != "fence_alpha" || got[0].RepoID != "repo_alpha" || got[0].Kind != fences.KindWriterSession || got[0].Status != fences.StatusActive {
		t.Fatalf("fence = %#v, want active writer fence_alpha", got[0])
	}
	if !got[0].ExpiresAt.Equal(expiresAt) || !got[0].CreatedAt.Equal(createdAt) || !got[0].UpdatedAt.Equal(createdAt) {
		t.Fatalf("times = expires %v created %v updated %v", got[0].ExpiresAt, got[0].CreatedAt, got[0].UpdatedAt)
	}
	if got[1].ID != "fence_beta" || got[1].Kind != fences.KindLifecycle || got[1].Status != fences.StatusExpired {
		t.Fatalf("second fence = %#v, want expired lifecycle fence_beta", got[1])
	}
	if !got[1].ExpiresAt.Equal(secondExpiresAt) {
		t.Fatalf("second expires_at = %v, want %v", got[1].ExpiresAt, secondExpiresAt)
	}
}

func TestListHeldRepoFencesPropagatesRowsErrAndCloseErr(t *testing.T) {
	createdAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(30 * time.Minute)

	tests := []struct {
		name string
		rows fakeRows
	}{
		{
			name: "rows err",
			rows: fakeRows{
				rows: []fakeRow{{values: repoFenceRowValues(repoFenceFixture(createdAt, expiresAt))}},
				err:  errors.New("rows failed"),
			},
		},
		{
			name: "close err",
			rows: fakeRows{
				rows:     []fakeRow{{values: repoFenceRowValues(repoFenceFixture(createdAt, expiresAt))}},
				closeErr: errors.New("close failed"),
			},
		},
		{
			name: "scan err still closes",
			rows: fakeRows{
				rows: []fakeRow{{err: errors.New("scan failed")}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{rows: tt.rows}
			st := &Store{exec: exec}

			_, err := st.ListHeldRepoFences(context.Background(), "repo_alpha")
			if err == nil {
				t.Fatal("ListHeldRepoFences succeeded, want error")
			}
			if !exec.rows.closed {
				t.Fatal("rows were not closed after error")
			}
		})
	}
}

func TestListHeldRepoFencesRejectsRowsWithMissingDurableTimestamps(t *testing.T) {
	createdAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(30 * time.Minute)

	tests := []struct {
		name  string
		fence fences.Fence
	}{
		{name: "missing created_at", fence: func() fences.Fence {
			fence := repoFenceFixture(createdAt, expiresAt)
			fence.CreatedAt = time.Time{}
			return fence
		}()},
		{name: "missing updated_at", fence: func() fences.Fence {
			fence := repoFenceFixture(createdAt, expiresAt)
			fence.UpdatedAt = time.Time{}
			return fence
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{
				rows: fakeRows{rows: []fakeRow{{values: repoFenceRowValues(tt.fence)}}},
			}
			st := &Store{exec: exec}

			_, err := st.ListHeldRepoFences(context.Background(), "repo_alpha")
			if err == nil {
				t.Fatal("ListHeldRepoFences succeeded, want validation error")
			}
			if !exec.rows.closed {
				t.Fatal("rows were not closed after validation error")
			}
		})
	}
}

func TestReleaseRepoFenceMarksOnlyActiveHeldFenceReleased(t *testing.T) {
	releasedAt := time.Date(2026, 5, 5, 13, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{rowsAffected: 1}
	st := &Store{exec: exec, clock: func() time.Time { return releasedAt }}

	if err := st.ReleaseRepoFence(context.Background(), "repo_alpha", "fence_alpha"); err != nil {
		t.Fatalf("ReleaseRepoFence: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"UPDATE repo_fences SET",
		"status = $1",
		"released_at = $2",
		"updated_at = $3",
		"WHERE repo_id = $4",
		"fence_id = $5",
		"status = $6",
		"released_at IS NULL",
		"recovered_at IS NULL",
	)
	if strings.Contains(strings.ToLower(strings.Split(exec.query, " WHERE ")[0]), "expires_at") {
		t.Fatalf("release query mutates expires_at: %s", exec.query)
	}
	wantArgs := []any{string(fences.StatusReleased), releasedAt, releasedAt, "repo_alpha", "fence_alpha", string(fences.StatusActive)}
	for idx, want := range wantArgs {
		if exec.args[idx] != want {
			t.Fatalf("arg %d = %#v, want %#v", idx+1, exec.args[idx], want)
		}
	}
}

func TestReleaseRepoFenceNoRowsWrapsSQLNoRows(t *testing.T) {
	exec := &fakeExecutor{rowsAffected: 0}
	st := &Store{exec: exec, clock: func() time.Time {
		return time.Date(2026, 5, 5, 13, 0, 0, 0, time.UTC)
	}}

	err := st.ReleaseRepoFence(context.Background(), "repo_alpha", "fence_alpha")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ReleaseRepoFence error = %v, want sql.ErrNoRows", err)
	}
}

func repoFenceFixture(createdAt, expiresAt time.Time) fences.Fence {
	return fences.Fence{
		ID:                "fence_alpha",
		RepoID:            "repo_alpha",
		Kind:              fences.KindWriterSession,
		HolderOperationID: "op_alpha",
		Status:            fences.StatusActive,
		ExpiresAt:         expiresAt,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
}

func repoFenceRowValues(fence fences.Fence) []any {
	return []any{
		fence.ID,
		fence.RepoID,
		string(fence.Kind),
		fence.HolderOperationID,
		string(fence.Status),
		fence.ExpiresAt,
		timePtrValue(fence.ReleasedAt),
		nullableArgString(fence.RecoveryOperationID),
		nullableArgString(fence.RecoveryReason),
		timePtrValue(fence.RecoveryStartedAt),
		timePtrValue(fence.RecoveredAt),
		fence.CreatedAt,
		fence.UpdatedAt,
	}
}

func timePtrValue(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}
