package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestRepoPurgeRecoverySQLIsPurgeOnlyAndDoesNotFinalizeCancel(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{rows: fakeRows{}}
	st := &Store{exec: exec}

	_, _ = st.ListRepoPurgeOperationsForRecovery(context.Background(), now, 5)

	assertSQLContainsInOrder(t, exec.query,
		"FROM operations",
		"operation_type = 'repo_purge'",
		"phase = 'validate_repo_lifecycle'",
		"ORDER BY created_at, operation_id LIMIT $2",
	)
	if strings.Contains(exec.query, "cancel_requested") || strings.Contains(exec.query, "repo_archive") {
		t.Fatalf("repo purge list SQL includes unsafe scope/cancel: %s", exec.query)
	}
}

func TestAcquireRepoPurgeOperationLeaseScopesBeforeMutationAndRejectsCancelFinalize(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now)
	record.Type = operations.OperationRepoPurge
	record.Phase = operations.OperationPhaseRepoLifecycleValidate
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRepoPurgeOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireRepoPurgeOperationLease: %v", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"WHERE operation_id = $1",
		"operation_type = 'repo_purge'",
		"phase = 'validate_repo_lifecycle'",
		"AND $5 = ''",
		"earlier_jvs_mutation AS",
		"o.operation_id <> e.operation_id",
		"o.operation_type IN ('save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone')",
		"o.operation_state NOT IN ('succeeded','failed','cancelled')",
		"active_restore_plan AS",
		"FROM restore_plans p, eligible_operation e",
		"p.repo_id = e.repo_id",
		"p.status IN ('pending', 'consuming', 'discarding', 'operator_intervention_required')",
		"updated_operation AS",
		"UPDATE operations SET",
		"NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"NOT EXISTS (SELECT 1 FROM active_restore_plan)",
		"RETURNING",
	)
	for _, forbidden := range []string{"repo_fences", "finalize_cancellation", "earlier_repo_lifecycle"} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("purge acquire SQL includes unsupported fragment %q: %s", forbidden, exec.query)
		}
	}
}

func TestRepoPurgeSuccessCommitSQLIsDedicatedAtomicBoundary(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo, record := repoLifecycleCommitFixtures(now, operations.OperationRepoPurge, resources.RepoStatusPurged)
	repo.Lifecycle.PreDeleteStatus = resources.RepoStatusActive
	exec := &fakeExecutor{row: fakeRow{values: append(repoRowValues(repo), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.CommitRepoPurgeSucceededWithLease(context.Background(), repo, record.SanitizedForPersistence(), "worker-a", now, repoLifecycleAudit(record, audit.EventTypeRepoPurge, audit.OutcomeSucceeded, now), "fence_purge")
	if err != nil {
		t.Fatalf("CommitRepoPurgeSucceededWithLease: %v", err)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type = 'repo_purge'",
		"held_fence AS",
		"no_sessions AS",
		"no_earlier_lifecycle AS",
		"earlier.created_at < (SELECT created_at FROM eligible_operation)",
		"earlier.created_at = (SELECT created_at FROM eligible_operation)",
		"earlier.operation_id < $12",
		"updated_repo AS",
		"repos.status = 'tombstoned'",
		"eligible_operation.created_at > repos.updated_at",
		"$25 = 'purged'",
		"$28 = 'purged'",
		"$29 IS NULL",
		"$31 = repos.pre_delete_status",
		"updated_operation AS",
		"released_fence AS",
		"inserted_audit AS",
	)
	for _, forbidden := range []string{"active_namespace AS", "active_binding AS", "active_volume AS", "capabilities->>'jvs_external_control_root'"} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("purge success SQL has volatile active metadata gate %q: %s", forbidden, exec.query)
		}
	}
	for _, forbidden := range []string{"SET volume_id", "SET jvs_repo_id", "SET repo_kind", "SET control_volume_subdir", "SET payload_volume_subdir"} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("purge success SQL rewrites immutable identity %q: %s", forbidden, exec.query)
		}
	}
}

func TestRepoPurgeSuccessCommitSQLRequiresConfirmedUnmountedMountEvidence(t *testing.T) {
	sql := repoPurgeSuccessCommitWithLeaseSQL()
	noSessions := sqlBetween(t, sql, "), no_sessions AS (", "), no_earlier_lifecycle AS (")

	assertSQLContainsInOrder(t, noSessions,
		"workload_mount_bindings",
		"status NOT IN ('released','revoked','expired','failed')",
		"confirmed_unmounted_at IS NULL",
	)
	if strings.Contains(noSessions, "AND NOT EXISTS (SELECT 1 FROM workload_mount_bindings WHERE repo_id = $15 AND status NOT IN ('released','revoked','expired','failed'))") {
		t.Fatalf("purge no_sessions allows terminal mounts without confirmed non-accessing evidence: %s", noSessions)
	}
	if strings.Contains(noSessions, "unable_to_write_at") {
		t.Fatalf("purge no_sessions must not accept unable_to_write_at as non-accessing evidence: %s", noSessions)
	}
}

func TestRepoPurgeValidatorsRejectNonPurgeTypes(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo, record := repoLifecycleCommitFixtures(now, operations.OperationRepoArchive, resources.RepoStatusArchived)
	if err := validateRepoPurgeSuccessRecord(repo, record); err == nil {
		t.Fatal("validateRepoPurgeSuccessRecord accepted non-purge operation")
	}
	record.State = operations.OperationStateOperatorInterventionRequired
	record.Phase = operations.OperationPhaseRepoLifecycleValidate
	record.Error = &operations.OperationError{Code: "FAILED", Message: "failed", CorrelationID: record.CorrelationID, OperationID: record.ID}
	if err := validateRepoPurgeFailureRecord(record); err == nil {
		t.Fatal("validateRepoPurgeFailureRecord accepted non-purge operation")
	}
}

var _ store.RepoPurgeOperationRecoveryStore = (*Store)(nil)
