package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsRepoSessionStateReader(t *testing.T) {
	var _ store.RepoSessionStateReader = (*Store)(nil)
}

func TestListExportSessionsByRepoReadsSafeSessionFields(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{
			{values: []any{"export_alpha", "ns_123", "repo_123", "read_write", "active", now.Add(time.Hour), 3, 1, now, now, now.Add(time.Minute), nil, nil, "active writers", now, now.Add(time.Minute)}},
			{values: []any{"export_beta", "ns_123", "repo_123", "read_only", "revoked", now.Add(-time.Hour), 0, 0, now, now, now.Add(time.Minute), now, now, "terminal", now.Add(time.Minute), now.Add(2 * time.Minute)}},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.ListExportSessionsByRepo(context.Background(), "repo_123")
	if err != nil {
		t.Fatalf("ListExportSessionsByRepo: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"SELECT export_id, namespace_id, repo_id, access_mode, status, expires_at, active_request_count, active_write_count, last_observed_at, last_gateway_heartbeat_at, gateway_heartbeat_expires_at, write_drained_at, terminal_observed_at, status_reason, created_at, updated_at FROM export_sessions",
		"WHERE repo_id = $1",
		"ORDER BY created_at, export_id",
	)
	assertNoSensitiveSessionSQL(t, exec.query)
	if len(exec.args) != 1 || exec.args[0] != "repo_123" {
		t.Fatalf("args = %#v, want repo_123", exec.args)
	}
	if len(got) != 2 {
		t.Fatalf("session count = %d, want 2", len(got))
	}
	if got[0].ID != "export_alpha" || got[0].NamespaceID != "ns_123" || got[0].RepoID != "repo_123" ||
		got[0].Mode != sessionstate.AccessModeReadWrite || got[0].Status != sessionstate.ExportStatusActive ||
		!got[0].ExpiresAt.Equal(now.Add(time.Hour)) || got[0].ActiveRequestCount != 3 || got[0].ActiveWriteCount != 1 ||
		got[0].LastObservedAt == nil || !got[0].LastObservedAt.Equal(now) || got[0].StatusReason != "active writers" {
		t.Fatalf("first export session = %#v", got[0])
	}
	if got[1].Mode != sessionstate.AccessModeReadOnly || got[1].Status != sessionstate.ExportStatusRevoked ||
		got[1].TerminalObservedAt == nil || !got[1].TerminalObservedAt.Equal(now) {
		t.Fatalf("second export session = %#v", got[1])
	}
}

func TestListWorkloadMountBindingsByRepoReadsSafeSessionFields(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{
			{values: []any{"wmb_alpha", "ns_123", "repo_123", false, "active", now.Add(time.Hour), now, now.Add(time.Minute)}},
			{values: []any{"wmb_beta", "ns_123", "repo_123", true, "released", now.Add(-time.Hour), now.Add(time.Minute), now.Add(2 * time.Minute)}},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.ListWorkloadMountBindingsByRepo(context.Background(), "repo_123")
	if err != nil {
		t.Fatalf("ListWorkloadMountBindingsByRepo: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"SELECT mount_binding_id, namespace_id, repo_id, read_only, status, lease_expires_at, created_at, updated_at FROM workload_mount_bindings",
		"WHERE repo_id = $1",
		"ORDER BY created_at, mount_binding_id",
	)
	assertNoSensitiveSessionSQL(t, exec.query)
	if len(exec.args) != 1 || exec.args[0] != "repo_123" {
		t.Fatalf("args = %#v, want repo_123", exec.args)
	}
	if len(got) != 2 {
		t.Fatalf("mount count = %d, want 2", len(got))
	}
	if got[0].ID != "wmb_alpha" || got[0].NamespaceID != "ns_123" || got[0].RepoID != "repo_123" ||
		got[0].ReadOnly || got[0].Status != sessionstate.MountStatusActive ||
		!got[0].LeaseExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("first mount binding = %#v", got[0])
	}
	if !got[1].ReadOnly || got[1].Status != sessionstate.MountStatusReleased {
		t.Fatalf("second mount binding = %#v", got[1])
	}
}

func TestRepoSessionStateReadersRejectInvalidRepoIDBeforeSQL(t *testing.T) {
	for _, call := range []struct {
		name string
		run  func(*Store) error
	}{
		{name: "exports", run: func(st *Store) error {
			_, err := st.ListExportSessionsByRepo(context.Background(), "repo_bad/slash")
			return err
		}},
		{name: "mounts", run: func(st *Store) error {
			_, err := st.ListWorkloadMountBindingsByRepo(context.Background(), "repo_bad/slash")
			return err
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			err := call.run(st)

			if err == nil {
				t.Fatal("reader succeeded, want invalid repo error")
			}
			if exec.queryCalls != 0 || exec.query != "" {
				t.Fatalf("query calls/query = %d/%q, want no SQL", exec.queryCalls, exec.query)
			}
		})
	}
}

func TestRepoSessionStateReadersPropagateQueryScanRowsAndCloseErrors(t *testing.T) {
	queryErr := errors.New("query failed")
	scanErr := errors.New("scan failed")
	rowsErr := errors.New("rows failed")
	closeErr := errors.New("close failed")

	tests := []struct {
		name string
		exec fakeExecutor
		run  func(*Store) error
		want error
	}{
		{name: "export query error", exec: fakeExecutor{err: queryErr}, run: func(st *Store) error {
			_, err := st.ListExportSessionsByRepo(context.Background(), "repo_123")
			return err
		}, want: queryErr},
		{name: "export scan error", exec: fakeExecutor{rows: fakeRows{rows: []fakeRow{{err: scanErr}}}}, run: func(st *Store) error {
			_, err := st.ListExportSessionsByRepo(context.Background(), "repo_123")
			return err
		}, want: scanErr},
		{name: "mount rows error", exec: fakeExecutor{rows: fakeRows{err: rowsErr}}, run: func(st *Store) error {
			_, err := st.ListWorkloadMountBindingsByRepo(context.Background(), "repo_123")
			return err
		}, want: rowsErr},
		{name: "mount close error", exec: fakeExecutor{rows: fakeRows{closeErr: closeErr}}, run: func(st *Store) error {
			_, err := st.ListWorkloadMountBindingsByRepo(context.Background(), "repo_123")
			return err
		}, want: closeErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := tt.exec
			st := &Store{exec: &exec}

			err := tt.run(st)

			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func assertNoSensitiveSessionSQL(t *testing.T, query string) {
	t.Helper()
	lower := strings.ToLower(query)
	for _, forbidden := range []string{"credential", "secret", "token", "password", "raw_path", "mount_plan", "storage_secret"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("session query contains sensitive term %q: %s", forbidden, query)
		}
	}
}
