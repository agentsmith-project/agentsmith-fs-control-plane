package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restorereconcile"
)

func TestRestoreReconciliationMigrationDefinesRunAndObservationTables(t *testing.T) {
	body, err := os.ReadFile("../../../migrations/0004_restore_reconciliation.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	text := strings.ToLower(string(body))
	for _, want := range []string{
		"create table if not exists restore_reconciliation_runs",
		"create table if not exists restore_reconciliation_targets",
		"create table if not exists restore_reconciliation_observations",
		"mode",
		"reconciling",
		"blocked_operator_intervention",
		"completed",
		"expected_storage_generation",
		"expected_snapshot_id",
		"expected_tombstone_marker",
		"expected_purge_marker",
		"observed_snapshot_id",
		"observed_tombstone_marker",
		"observed_purge_marker",
		"evidence_ref",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("migration missing %q:\n%s", want, text)
		}
	}
}

func TestCommitRestoreReconciliationMismatchMarksRepoOperatorInterventionAndAudits(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{row: fakeRow{values: repoRowValues(restoreReconciliationRepo(resources.RepoStatusOperatorInterventionRequired))}}
	st := &Store{exec: exec}
	err := st.CommitRestoreReconciliationMismatch(context.Background(), restorereconcile.MismatchCommit{
		RunID:       "rrun_123",
		RepoID:      "repo_123",
		NamespaceID: "ns_123",
		Reason:      restorereconcile.ReasonMetadataStorageMismatch,
		EvidenceRef: "restore-reconciliation://run/rrun_123/repo/repo_123",
		Audit:       restoreReconciliationAudit("evt_restore_reconcile", "repo_123", now),
		Now:         now,
	})
	if err != nil {
		t.Fatalf("CommitRestoreReconciliationMismatch: %v", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_run AS",
		"SELECT run_id FROM restore_reconciliation_runs",
		"WHERE run_id = $1 AND mode = 'reconciling'",
		"FOR UPDATE",
		"inserted_observation AS",
		"INSERT INTO restore_reconciliation_observations",
		"FROM eligible_run",
		"UPDATE repos SET status = 'operator_intervention_required'",
		"FROM inserted_observation, eligible_run",
		"UPDATE restore_reconciliation_runs SET mode = 'blocked_operator_intervention'",
		"FROM eligible_run",
		"INSERT INTO audit_outbox",
	)
}

func TestCommitRestoreReconciliationMismatchRequiresEligibleReconcilingRunBeforeSideEffects(t *testing.T) {
	exec := &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}
	st := &Store{exec: exec}
	err := st.CommitRestoreReconciliationMismatch(context.Background(), restorereconcile.MismatchCommit{
		RunID:       "rrun_completed",
		RepoID:      "repo_123",
		NamespaceID: "ns_123",
		Reason:      restorereconcile.ReasonMetadataStorageMismatch,
		EvidenceRef: "restore-reconciliation://run/rrun_completed/repo/repo_123",
		Audit:       restoreReconciliationAudit("evt_restore_reconcile", "repo_123", time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)),
		Now:         time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitRestoreReconciliationMismatch error = %v, want fail-closed no rows", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_run AS",
		"WHERE run_id = $1 AND mode = 'reconciling'",
		"inserted_observation AS",
		"FROM eligible_run",
		"updated_repo AS",
		"FROM inserted_observation, eligible_run",
		"affected_repo AS",
		"inserted_audit AS",
		"FROM affected_repo, blocked_run",
	)
}

func TestRestoreReconciliationPurgedStoragePresentDoesNotResurrect(t *testing.T) {
	sql := restoreReconciliationMismatchSQL()
	for _, want := range []string{
		"purged_repo AS",
		"repos.status = 'purged'",
		"affected_repo AS",
		"operator_intervention_required",
		"observed_storage_present",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("restore reconciliation SQL missing %q:\n%s", want, sql)
		}
	}
	if strings.Contains(strings.ToLower(sql), "status = 'active'") {
		t.Fatalf("restore reconciliation mismatch SQL must not resurrect purged repos:\n%s", sql)
	}
	if strings.Contains(sql, "AND (repos.status <> 'purged' OR (repos.status = 'purged'") {
		t.Fatalf("restore reconciliation mismatch SQL must not update purged repos to intervention:\n%s", sql)
	}
	assertSQLContainsInOrder(t, sql,
		"updated_repo AS (",
		"repos.status <> 'purged'",
		"purged_repo AS (",
		"repos.status = 'purged'",
		"affected_repo AS (",
		"SELECT * FROM updated_repo UNION ALL SELECT * FROM purged_repo",
		"INSERT INTO audit_outbox",
		"FROM affected_repo, blocked_run",
	)
}

func TestObserveRestoreReconciliationTargetDerivesObservedMarkersFromCurrentRepoNotExpectedEcho(t *testing.T) {
	sql := restoreReconciliationObserveTargetSQL()
	for _, want := range []string{
		"'status:' || current_repo.status",
		"'repo:' || current_repo.repo_id",
		"CASE WHEN current_repo.status = 'tombstoned'",
		"CASE WHEN current_repo.status = 'purged'",
		"target.expected_storage_generation",
		"target.expected_snapshot_id",
		"target.expected_tombstone_marker",
		"target.expected_purge_marker",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("observe target SQL missing %q:\n%s", want, sql)
		}
	}
	for _, forbidden := range []string{
		"target.expected_storage_generation, target.expected_snapshot_id, target.expected_tombstone_marker, target.expected_purge_marker, 'clean'",
		"SELECT target.run_id, target.repo_id, target.namespace_id, target.expected_repo_status, true, target.expected_storage_generation",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("observe target SQL appears to echo expected marker as observed clean %q:\n%s", forbidden, sql)
		}
	}
}

func TestCompleteRestoreReconciliationRunRequiresAllReposObservedClean(t *testing.T) {
	exec := &fakeExecutor{rowsAffected: 0}
	st := &Store{exec: exec}
	err := st.CompleteRestoreReconciliationRun(context.Background(), "rrun_123", time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CompleteRestoreReconciliationRun error = %v, want fail closed no rows", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE restore_reconciliation_runs",
		"mode = 'completed'",
		"WHERE run_id = $1 AND mode = 'reconciling'",
		"EXISTS (SELECT 1 FROM restore_reconciliation_targets",
		"NOT EXISTS",
		"restore_reconciliation_targets target",
		"LEFT JOIN restore_reconciliation_observations observation",
		"observation.result = 'clean'",
		"observation.evidence_ref <> ''",
		"observation.observed_generation = target.expected_storage_generation",
		"observation.observed_snapshot_id = target.expected_snapshot_id",
		"observation.observed_tombstone_marker = target.expected_tombstone_marker",
		"observation.observed_purge_marker = target.expected_purge_marker",
	)
}

func TestRestoreReconciliationStoreDoesNotTouchCredentialsFencesOrStorageSideEffects(t *testing.T) {
	sql := strings.ToLower(restoreReconciliationMismatchSQL() + " " + restoreReconciliationCompleteSQL())
	for _, forbidden := range []string{
		"credential_hash",
		"credential_salt",
		"repo_fences",
		"session_fence",
		"restore_plans",
		"export_sessions set",
		"workload_mount_bindings",
		"delete ",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("restore reconciliation SQL contains forbidden side effect %q:\n%s", forbidden, sql)
		}
	}
}

func restoreReconciliationRepo(status resources.RepoStatus) resources.Repo {
	now := time.Date(2026, 5, 8, 11, 0, 0, 0, time.UTC)
	return resources.Repo{
		ID:                  "repo_123",
		NamespaceID:         "ns_123",
		VolumeID:            "vol_123",
		JVSRepoID:           "jvs_123",
		Kind:                resources.RepoKindRepo,
		Status:              status,
		ControlVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/control",
		PayloadVolumeSubdir: "afscp/namespaces/ns_123/repos/repo_123/payload",
		Lifecycle:           resources.RepoLifecycle{Status: status},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func restoreReconciliationAudit(eventID, repoID string, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{
		EventID:       eventID,
		Type:          audit.EventTypeRestoreReconciliation,
		Time:          now,
		CallerService: "restore-reconcile-worker",
		AuthorizedActor: audit.Actor{
			Type: "service",
			ID:   "restore-reconcile-worker",
		},
		CorrelationID: "corr_restore_reconcile",
		Resource:      audit.Resource{Type: "repo", ID: repoID, NamespaceID: "ns_123"},
		Outcome:       audit.OutcomeFailed,
		Reason:        "restore_reconciliation_mismatch",
	})
}
