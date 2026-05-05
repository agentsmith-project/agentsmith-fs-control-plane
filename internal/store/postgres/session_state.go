package postgres

import (
	"context"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

var exportSessionColumns = []string{
	"export_id",
	"namespace_id",
	"repo_id",
	"access_mode",
	"status",
	"expires_at",
	"created_at",
	"updated_at",
}

var workloadMountBindingColumns = []string{
	"mount_binding_id",
	"namespace_id",
	"repo_id",
	"read_only",
	"status",
	"lease_expires_at",
	"created_at",
	"updated_at",
}

func (store *Store) ListExportSessionsByRepo(ctx context.Context, repoID string) (sessions []sessionstate.ExportSession, err error) {
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		return nil, err
	}
	rows, err := store.exec.QueryContext(ctx, exportSessionSelectSQL()+" WHERE repo_id = $1 ORDER BY created_at, export_id", repoID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		session, err := scanExportSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (store *Store) ListWorkloadMountBindingsByRepo(ctx context.Context, repoID string) (mounts []sessionstate.WorkloadMountBinding, err error) {
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		return nil, err
	}
	rows, err := store.exec.QueryContext(ctx, workloadMountBindingSelectSQL()+" WHERE repo_id = $1 ORDER BY created_at, mount_binding_id", repoID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		mount, err := scanWorkloadMountBinding(rows)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, mount)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return mounts, nil
}

func exportSessionSelectSQL() string {
	return "SELECT " + strings.Join(exportSessionColumns, ", ") + " FROM export_sessions"
}

func workloadMountBindingSelectSQL() string {
	return "SELECT " + strings.Join(workloadMountBindingColumns, ", ") + " FROM workload_mount_bindings"
}

func scanExportSession(row rowScanner) (sessionstate.ExportSession, error) {
	var session sessionstate.ExportSession
	var mode, status string
	if err := row.Scan(
		&session.ID,
		&session.NamespaceID,
		&session.RepoID,
		&mode,
		&status,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	); err != nil {
		return sessionstate.ExportSession{}, err
	}
	session.Mode = sessionstate.AccessMode(mode)
	session.Status = sessionstate.ExportStatus(status)
	return session, nil
}

func scanWorkloadMountBinding(row rowScanner) (sessionstate.WorkloadMountBinding, error) {
	var mount sessionstate.WorkloadMountBinding
	var status string
	if err := row.Scan(
		&mount.ID,
		&mount.NamespaceID,
		&mount.RepoID,
		&mount.ReadOnly,
		&status,
		&mount.LeaseExpiresAt,
		&mount.CreatedAt,
		&mount.UpdatedAt,
	); err != nil {
		return sessionstate.WorkloadMountBinding{}, err
	}
	mount.Status = sessionstate.MountStatus(status)
	return mount, nil
}
