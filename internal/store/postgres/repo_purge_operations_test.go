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
		"UPDATE operations SET",
		"operation_type = 'repo_purge'",
		"phase = 'validate_repo_lifecycle'",
		"AND $5 = ''",
		"RETURNING",
	)
	if strings.Contains(exec.query, "repo_fences") || strings.Contains(exec.query, "finalize_cancellation") {
		t.Fatalf("purge acquire SQL releases fence/finalizes cancel: %s", exec.query)
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
