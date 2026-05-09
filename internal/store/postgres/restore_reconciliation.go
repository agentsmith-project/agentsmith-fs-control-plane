package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restorereconcile"
)

func (store *Store) RestoreReconciliationWriteBlocked(ctx context.Context, namespaceID, repoID string) (bool, error) {
	row := store.exec.QueryRowContext(ctx, restoreReconciliationWriteBlockedSQL(), namespaceID, repoID)
	var blocked bool
	if err := row.Scan(&blocked); err != nil {
		return false, err
	}
	return blocked, nil
}

func (store *Store) ActiveRun(ctx context.Context) (restorereconcile.Run, error) {
	row := store.exec.QueryRowContext(ctx, "SELECT run_id, mode FROM restore_reconciliation_runs WHERE mode IN ('reconciling','blocked_operator_intervention') ORDER BY created_at, run_id LIMIT 1")
	var run restorereconcile.Run
	var mode string
	if err := row.Scan(&run.ID, &mode); err != nil {
		return restorereconcile.Run{}, err
	}
	run.Mode = restorereconcile.Mode(mode)
	return run, nil
}

func (store *Store) ListObservations(ctx context.Context, runID string) (observations []restorereconcile.Observation, err error) {
	rows, err := store.exec.QueryContext(ctx, restoreReconciliationObservationsSQL(), runID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		var observation restorereconcile.Observation
		var expected, result string
		if err := rows.Scan(&observation.RunID, &observation.RepoID, &observation.NamespaceID, &expected, &observation.ObservedStoragePresent, &observation.ObservedGeneration, &observation.ObservedSnapshotID, &observation.ObservedTombstoneMarker, &observation.ObservedPurgeMarker, &result, &observation.Reason, &observation.EvidenceRef); err != nil {
			return nil, err
		}
		observation.ExpectedRepoStatus = restorereconcile.RepoStatus(expected)
		observation.Result = restorereconcile.ObservationResult(result)
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return observations, nil
}

func (store *Store) ListTargets(ctx context.Context, runID string) (targets []restorereconcile.Target, err error) {
	rows, err := store.exec.QueryContext(ctx, restoreReconciliationTargetsSQL(), runID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		var target restorereconcile.Target
		var expected string
		if err := rows.Scan(&target.RunID, &target.RepoID, &target.NamespaceID, &expected, &target.ExpectedStorageGeneration, &target.ExpectedSnapshotID, &target.ExpectedTombstoneMarker, &target.ExpectedPurgeMarker); err != nil {
			return nil, err
		}
		target.ExpectedRepoStatus = restorereconcile.RepoStatus(expected)
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (store *Store) ObserveTarget(ctx context.Context, target restorereconcile.Target) (restorereconcile.Observation, error) {
	row := store.exec.QueryRowContext(ctx, restoreReconciliationObserveTargetSQL(), target.RunID, target.RepoID, target.NamespaceID, string(target.ExpectedRepoStatus), target.ExpectedStorageGeneration, target.ExpectedSnapshotID, target.ExpectedTombstoneMarker, target.ExpectedPurgeMarker, time.Now().UTC())
	var observation restorereconcile.Observation
	var expected, result string
	if err := row.Scan(&observation.RunID, &observation.RepoID, &observation.NamespaceID, &expected, &observation.ObservedStoragePresent, &observation.ObservedGeneration, &observation.ObservedSnapshotID, &observation.ObservedTombstoneMarker, &observation.ObservedPurgeMarker, &result, &observation.Reason, &observation.EvidenceRef); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restorereconcile.Observation{}, restorereconcile.ErrObservationMissing
		}
		return restorereconcile.Observation{}, err
	}
	observation.ExpectedRepoStatus = restorereconcile.RepoStatus(expected)
	observation.Result = restorereconcile.ObservationResult(result)
	return observation, nil
}

func (store *Store) CompleteRun(ctx context.Context, runID string, now time.Time) error {
	return store.CompleteRestoreReconciliationRun(ctx, runID, now)
}

func (store *Store) CompleteRestoreReconciliationRun(ctx context.Context, runID string, now time.Time) error {
	result, err := store.exec.ExecContext(ctx, restoreReconciliationCompleteSQL(), runID, now.UTC())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (store *Store) CommitMismatch(ctx context.Context, request restorereconcile.MismatchCommit) error {
	return store.CommitRestoreReconciliationMismatch(ctx, request)
}

func (store *Store) CommitRestoreReconciliationMismatch(ctx context.Context, request restorereconcile.MismatchCommit) error {
	outboxRecord, err := auditOutboxRecordForRestoreReconciliation(request)
	if err != nil {
		return err
	}
	args := []any{
		request.RunID,
		request.RepoID,
		request.NamespaceID,
		string(restorereconcile.ObservationResultMismatch),
		request.Reason,
		request.EvidenceRef,
		request.Now.UTC(),
	}
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restoreReconciliationMismatchSQL(), args...)
	if _, err := scanRepo(row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	return nil
}

func auditOutboxRecordForRestoreReconciliation(request restorereconcile.MismatchCommit) (audit.OutboxRecord, error) {
	return audit.NewOutboxRecord(request.Audit, request.Now.UTC())
}

func restoreReconciliationWriteBlockedSQL() string {
	return "SELECT EXISTS (SELECT 1 FROM restore_reconciliation_runs WHERE mode IN ('reconciling','blocked_operator_intervention')) FROM (SELECT $1::text AS namespace_id, $2::text AS repo_id) requested"
}

func restoreReconciliationObservationsSQL() string {
	return "SELECT run_id, repo_id, namespace_id, expected_repo_status, observed_storage_present, observed_generation, observed_snapshot_id, observed_tombstone_marker, observed_purge_marker, result, reason, evidence_ref FROM restore_reconciliation_observations WHERE run_id = $1 ORDER BY repo_id"
}

func restoreReconciliationTargetsSQL() string {
	return "SELECT run_id, repo_id, namespace_id, expected_repo_status, expected_storage_generation, expected_snapshot_id, expected_tombstone_marker, expected_purge_marker FROM restore_reconciliation_targets WHERE run_id = $1 ORDER BY repo_id"
}

func restoreReconciliationObserveTargetSQL() string {
	return "WITH target AS (" +
		"SELECT run_id, repo_id, namespace_id, expected_repo_status, expected_storage_generation, expected_snapshot_id, expected_tombstone_marker, expected_purge_marker FROM restore_reconciliation_targets WHERE run_id = $1 AND repo_id = $2 AND namespace_id = $3" +
		"), current_repo AS (" +
		"SELECT repos.repo_id, repos.namespace_id, repos.status FROM repos, target WHERE repos.repo_id = target.repo_id AND repos.namespace_id = target.namespace_id" +
		"), observed_values AS (" +
		"SELECT target.run_id, target.repo_id, target.namespace_id, target.expected_repo_status, true AS observed_storage_present, 'status:' || current_repo.status AS observed_generation, 'repo:' || current_repo.repo_id AS observed_snapshot_id, CASE WHEN current_repo.status = 'tombstoned' THEN 'tombstone:' || current_repo.repo_id ELSE 'none' END AS observed_tombstone_marker, CASE WHEN current_repo.status = 'purged' THEN 'purge:' || current_repo.repo_id ELSE 'none' END AS observed_purge_marker, target.expected_storage_generation, target.expected_snapshot_id, target.expected_tombstone_marker, target.expected_purge_marker, current_repo.status AS current_status FROM target, current_repo" +
		"), scored_observation AS (" +
		"SELECT run_id, repo_id, namespace_id, expected_repo_status, observed_storage_present, observed_generation, observed_snapshot_id, observed_tombstone_marker, observed_purge_marker, CASE WHEN current_status = expected_repo_status AND observed_generation = expected_storage_generation AND observed_snapshot_id = expected_snapshot_id AND observed_tombstone_marker = expected_tombstone_marker AND observed_purge_marker = expected_purge_marker THEN 'clean' ELSE 'mismatch' END AS result, CASE WHEN current_status = expected_repo_status AND observed_generation = expected_storage_generation AND observed_snapshot_id = expected_snapshot_id AND observed_tombstone_marker = expected_tombstone_marker AND observed_purge_marker = expected_purge_marker THEN '' ELSE 'metadata_storage_mismatch' END AS reason FROM observed_values" +
		"), inserted_observation AS (" +
		"INSERT INTO restore_reconciliation_observations (run_id, repo_id, namespace_id, expected_repo_status, observed_storage_present, observed_generation, observed_snapshot_id, observed_tombstone_marker, observed_purge_marker, result, reason, evidence_ref, observed_at, created_at) " +
		"SELECT run_id, repo_id, namespace_id, expected_repo_status, observed_storage_present, observed_generation, observed_snapshot_id, observed_tombstone_marker, observed_purge_marker, result, reason, 'restore-reconciliation://run/' || run_id || '/repo/' || repo_id, $9, $9 FROM scored_observation " +
		"ON CONFLICT (run_id, repo_id) DO UPDATE SET expected_repo_status = EXCLUDED.expected_repo_status, observed_storage_present = EXCLUDED.observed_storage_present, observed_generation = EXCLUDED.observed_generation, observed_snapshot_id = EXCLUDED.observed_snapshot_id, observed_tombstone_marker = EXCLUDED.observed_tombstone_marker, observed_purge_marker = EXCLUDED.observed_purge_marker, result = EXCLUDED.result, reason = EXCLUDED.reason, evidence_ref = EXCLUDED.evidence_ref, observed_at = EXCLUDED.observed_at " +
		"RETURNING run_id, repo_id, namespace_id, expected_repo_status, observed_storage_present, observed_generation, observed_snapshot_id, observed_tombstone_marker, observed_purge_marker, result, reason, evidence_ref" +
		") SELECT run_id, repo_id, namespace_id, expected_repo_status, observed_storage_present, observed_generation, observed_snapshot_id, observed_tombstone_marker, observed_purge_marker, result, reason, evidence_ref FROM inserted_observation"
}

func restoreReconciliationCompleteSQL() string {
	return "UPDATE restore_reconciliation_runs SET mode = 'completed', completed_at = $2, updated_at = $2 WHERE run_id = $1 AND mode = 'reconciling' AND EXISTS (SELECT 1 FROM restore_reconciliation_targets WHERE run_id = $1) AND NOT EXISTS (SELECT 1 FROM restore_reconciliation_targets target LEFT JOIN restore_reconciliation_observations observation ON observation.run_id = target.run_id AND observation.repo_id = target.repo_id WHERE target.run_id = $1 AND COALESCE(observation.result = 'clean' AND observation.evidence_ref <> '' AND observation.observed_generation = target.expected_storage_generation AND observation.observed_snapshot_id = target.expected_snapshot_id AND observation.observed_tombstone_marker = target.expected_tombstone_marker AND observation.observed_purge_marker = target.expected_purge_marker, false) = false)"
}

func restoreReconciliationMismatchSQL() string {
	return "WITH eligible_run AS (" +
		"SELECT run_id FROM restore_reconciliation_runs WHERE run_id = $1 AND mode = 'reconciling' FOR UPDATE" +
		"), inserted_observation AS (" +
		"INSERT INTO restore_reconciliation_observations (run_id, repo_id, namespace_id, expected_repo_status, observed_storage_present, observed_generation, observed_snapshot_id, observed_tombstone_marker, observed_purge_marker, result, reason, evidence_ref, observed_at, created_at) " +
		"SELECT eligible_run.run_id, repos.repo_id, repos.namespace_id, repos.status, true, '', '', '', '', $4, $5, $6, $7, $7 FROM eligible_run, repos WHERE repos.repo_id = $2 AND repos.namespace_id = $3 " +
		"ON CONFLICT (run_id, repo_id) DO UPDATE SET result = EXCLUDED.result, reason = EXCLUDED.reason, evidence_ref = EXCLUDED.evidence_ref, observed_at = EXCLUDED.observed_at " +
		"RETURNING repo_id, namespace_id, observed_storage_present" +
		"), updated_repo AS (" +
		"UPDATE repos SET status = 'operator_intervention_required', lifecycle_status = 'operator_intervention_required', updated_at = $7 FROM inserted_observation, eligible_run WHERE repos.repo_id = inserted_observation.repo_id AND repos.namespace_id = inserted_observation.namespace_id AND repos.status <> 'purged' RETURNING " + strings.Join(repoColumns, ", ") +
		"), purged_repo AS (" +
		"SELECT " + prefixedColumns("repos", repoColumns) + " FROM repos, inserted_observation, eligible_run WHERE repos.repo_id = inserted_observation.repo_id AND repos.namespace_id = inserted_observation.namespace_id AND repos.status = 'purged' AND inserted_observation.observed_storage_present = true" +
		"), affected_repo AS (" +
		"SELECT * FROM updated_repo UNION ALL SELECT * FROM purged_repo" +
		"), blocked_run AS (" +
		"UPDATE restore_reconciliation_runs SET mode = 'blocked_operator_intervention', reason = $5, updated_at = $7 FROM eligible_run WHERE restore_reconciliation_runs.run_id = eligible_run.run_id RETURNING restore_reconciliation_runs.run_id" +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(8, len(auditOutboxColumns)) + " FROM affected_repo, blocked_run RETURNING audit_event_id" +
		") SELECT " + strings.Join(repoColumns, ", ") + " FROM affected_repo WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}
