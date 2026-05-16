package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestRepoLifecycleRecoverySQLScopedToSupportedTypesAndValidatePhase(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{rows: fakeRows{}}
	st := &Store{exec: exec}

	_, _ = st.ListRepoLifecycleOperationsForRecovery(context.Background(), now, 5)

	assertSQLContainsInOrder(t, exec.query,
		"FROM operations",
		"operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned')",
		"phase = 'validate_repo_lifecycle'",
		"ORDER BY created_at, operation_id LIMIT $2",
	)
	if strings.Contains(exec.query, "repo_purge") {
		t.Fatalf("repo lifecycle SQL includes unsupported operation: %s", exec.query)
	}
}

func TestAcquireRepoLifecycleOperationLeaseScopesBeforeMutation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	record := operationFixture(now)
	record.Type = operations.OperationRepoArchive
	record.Phase = operations.OperationPhaseRepoLifecycleValidate
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.AcquireRepoLifecycleOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now})
	if err != nil {
		t.Fatalf("AcquireRepoLifecycleOperationLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"WHERE operation_id = $1",
		"operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned')",
		"phase = 'validate_repo_lifecycle'",
		"earlier_jvs_mutation AS",
		"o.operation_id <> e.operation_id",
		"o.operation_type IN ('save_point_create', 'restore', 'template_create', 'template_clone')",
		"o.operation_state NOT IN ('succeeded','failed','cancelled')",
		"updated_operation AS",
		"UPDATE operations SET",
		"$5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"RETURNING",
	)
	if strings.Contains(exec.query, "operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned', 'repo_purge')") {
		t.Fatalf("repo lifecycle acquire SQL included repo_purge in lifecycle scope: %s", exec.query)
	}
	if strings.Contains(exec.query, "restore_plans") || strings.Contains(exec.query, "active_restore_plan") {
		t.Fatalf("repo lifecycle acquire SQL must not inspect restore plans: %s", exec.query)
	}
}

func TestAcquireRepoLifecycleOperationLeaseFinalizeCancellationReleasesSameOperationFence(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	for _, typ := range []operations.OperationType{operations.OperationRepoArchive, operations.OperationRepoDelete, operations.OperationRepoRestoreTombstoned} {
		t.Run(string(typ), func(t *testing.T) {
			record := operationFixture(now)
			record.Type = typ
			record.Phase = operations.OperationPhaseRepoLifecycleValidate
			record.State = operations.OperationStateCancelled
			record.FinishedAt = &now
			exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
			st := &Store{exec: exec}

			_, err := st.AcquireRepoLifecycleOperationLease(context.Background(), record.ID, operations.LeaseRequest{Owner: "worker-a", Duration: time.Minute, Now: now, CancelPolicy: operations.LeaseCancelPolicyFinalize})
			if err != nil {
				t.Fatalf("AcquireRepoLifecycleOperationLease finalize: %v", err)
			}

			assertSQLContainsInOrder(t, exec.query,
				"WITH eligible_operation AS",
				"operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned')",
				"phase = 'validate_repo_lifecycle'",
				"operation_state = 'cancel_requested'",
				"held_fence AS",
				"holder_operation_id = $1",
				"FOR UPDATE",
				"released_fence AS",
				"UPDATE repo_fences SET status = 'released'",
				"earlier_jvs_mutation AS",
				"updated_operation AS",
				"$5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
				"SELECT",
				"FROM updated_operation",
			)
			if strings.Contains(exec.query, "restore_plans") || strings.Contains(exec.query, "active_restore_plan") {
				t.Fatalf("repo lifecycle cancellation SQL must not inspect restore plans: %s", exec.query)
			}
		})
	}
}

func TestRepoLifecycleSuccessCommitSQLIsAtomicBoundaryWithSessionGuardAndFenceRelease(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo, record := repoLifecycleCommitFixtures(now, operations.OperationRepoArchive, resources.RepoStatusArchived)
	exec := &fakeExecutor{row: fakeRow{values: append(repoRowValues(repo), operationRowValues(record)...)}}
	st := &Store{exec: exec}

	_, _, err := st.CommitRepoLifecycleSucceededWithLease(context.Background(), repo, record.SanitizedForPersistence(), "worker-a", now, repoLifecycleAudit(record, audit.EventTypeRepoArchive, audit.OutcomeSucceeded, now), "fence_alpha")
	if err != nil {
		t.Fatalf("CommitRepoLifecycleSucceededWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned')",
		"phase = 'validate_repo_lifecycle'",
		"active_namespace AS",
		"status = 'active'",
		"active_binding AS",
		"namespace_volume_bindings",
		"active_volume AS",
		"capabilities->>'jvs_external_control_root' = 'true'",
		"held_fence AS",
		"FOR UPDATE",
		"no_sessions AS",
		"export_sessions",
		"workload_mount_bindings",
		"updated_repo AS",
		"FROM eligible_operation, active_namespace, active_binding, active_volume, held_fence, no_sessions",
		"repos.repo_id = $20",
		"repos.repo_id = $15",
		"repos.namespace_id = $21",
		"repos.namespace_id = $14",
		"repos.volume_id = active_volume.volume_id",
		"repos.jvs_repo_id = $23",
		"repos.repo_kind = $24",
		"repos.control_volume_subdir = $26",
		"repos.payload_volume_subdir = $27",
		"repos.created_at = $32",
		"updated_operation AS",
		"released_fence AS",
		"inserted_audit AS",
	)
	if strings.Contains(exec.query, "CommitOperationWithLease") || strings.Contains(exec.query, "UpdateRepoLifecycle") {
		t.Fatalf("success SQL composed generic operation/update path: %s", exec.query)
	}
	for _, forbidden := range []string{"SET volume_id", "SET jvs_repo_id", "SET kind", "SET control_volume_subdir", "SET payload_volume_subdir"} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("success SQL rewrites immutable repo identity %q: %s", forbidden, exec.query)
		}
	}
}

func TestRepoLifecycleSuccessCommitSQLUsesAllRepoArgs(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	assertSQLContainsInOrder(t, sql,
		"updated_repo AS",
		"repos.repo_id = $20",
		"repos.repo_id = $15",
		"repos.namespace_id = $21",
		"repos.namespace_id = $14",
	)
	assertSQLContainsAll(t, sql,
		"repos.volume_id = $22",
		"repos.jvs_repo_id = $23",
		"repos.repo_kind = $24",
		"status = $25",
		"repos.control_volume_subdir = $26",
		"repos.payload_volume_subdir = $27",
		"lifecycle_status = $28",
		"$29::timestamptz",
		"last_lifecycle_operation_id = $30",
		"$31::text",
		"repos.created_at = $32",
		"updated_at = $33",
	)
}

func TestRepoLifecycleCommitSQLQualifiesOperationReturningColumns(t *testing.T) {
	successSQL := repoLifecycleSuccessCommitWithLeaseSQL()
	assertSQLContainsInOrder(t, successSQL,
		"), updated_operation AS (",
		"FROM eligible_operation, updated_repo",
		"RETURNING "+operationReturningColumnsSQL(),
		"), released_fence AS (",
	)
	if strings.Contains(successSQL, "RETURNING operation_id") {
		t.Fatalf("repo lifecycle success commit uses ambiguous operation RETURNING columns: %s", successSQL)
	}

	failureSQL := repoLifecycleFailureCommitWithLeaseSQL()
	assertSQLContainsInOrder(t, failureSQL,
		"), updated_operation AS (",
		"FROM eligible_operation",
		"RETURNING "+operationReturningColumnsSQL(),
		"), inserted_audit AS (",
	)
	if strings.Contains(failureSQL, "RETURNING operation_id") {
		t.Fatalf("repo lifecycle failure commit uses ambiguous operation RETURNING columns: %s", failureSQL)
	}
}

func TestRepoLifecycleSuccessCommitSQLHasDeleteAndRestoreTombstonedPredicates(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	assertSQLContainsInOrder(t, sql,
		"eligible_operation.operation_type = 'repo_delete'",
		"repos.status IN ('active','archived')",
		"$25 = 'tombstoned'",
		"$28 = 'tombstoned'",
		"$29::timestamptz IS NOT NULL",
		"$31::text = repos.status",
	)
	assertSQLContainsInOrder(t, sql,
		"eligible_operation.operation_type = 'repo_restore_tombstoned'",
		"repos.status = 'tombstoned'",
		"eligible_operation.created_at < repos.retention_expires_at",
		"eligible_operation.created_at > repos.updated_at",
		"$25 = repos.pre_delete_status",
		"$28 = repos.pre_delete_status",
		"$29::timestamptz IS NULL",
		"$31::text IS NULL",
	)
	if strings.Contains(sql, "repo_purge") {
		t.Fatalf("success SQL includes repo_purge: %s", sql)
	}
}

func TestRepoLifecycleSuccessCommitSQLCastsRetentionParameter(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	assertSQLContainsAll(t, sql,
		"retention_expires_at = $29::timestamptz",
		"$29::timestamptz IS NOT NULL",
		"$29::timestamptz IS NULL",
		"pre_delete_status = $31::text",
		"$31::text = repos.status",
		"$31::text IS NULL",
	)
	for _, forbidden := range []string{
		"retention_expires_at = $29,",
		"pre_delete_status = $31,",
		"$29 IS NOT NULL",
		"$29 IS NULL",
		"$31 = repos.status",
		"$31 IS NULL",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("repo lifecycle success SQL leaves nullable lifecycle parameter untyped via %q: %s", forbidden, sql)
		}
	}
}

func TestRepoLifecycleSuccessCommitSQLQualifiesUpdateReturningColumns(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	updatedRepo := sqlBetween(t, sql, "updated_repo AS (", "), updated_operation AS (")
	updatedOperation := sqlBetween(t, sql, "updated_operation AS (", "), released_fence AS (")

	assertSQLContainsAll(t, updatedRepo,
		"RETURNING "+prefixedColumns("repos", repoColumns),
	)
	assertSQLContainsAll(t, updatedOperation,
		"RETURNING "+operationReturningColumnsSQL(),
	)
	for _, forbidden := range []string{
		"RETURNING " + strings.Join(repoColumns, ", "),
		"RETURNING " + strings.Join(operationSelectColumns, ", "),
	} {
		if strings.Contains(updatedRepo, forbidden) || strings.Contains(updatedOperation, forbidden) {
			t.Fatalf("lifecycle success SQL uses ambiguous RETURNING columns %q: %s", forbidden, sql)
		}
	}
}

func TestRepoLifecycleSuccessCommitSQLQualifiesOperationLeasePreservation(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	updatedOperation := sqlBetween(t, sql, "updated_operation AS (", "), released_fence AS (")

	assertSQLContainsAll(t, updatedOperation,
		"lease_owner = CASE WHEN $1 = 'running' THEN operations.lease_owner ELSE NULL END",
		"lease_expires_at = CASE WHEN $1 = 'running' THEN operations.lease_expires_at ELSE NULL END",
		"started_at = COALESCE(operations.started_at, $9, $11)",
	)
	for _, forbidden := range []string{
		"THEN lease_owner ELSE NULL END",
		"THEN lease_expires_at ELSE NULL END",
		"COALESCE(started_at, $9, $11)",
	} {
		if strings.Contains(updatedOperation, forbidden) {
			t.Fatalf("repo lifecycle success operation update preserves lease with ambiguous source column %q: %s", forbidden, updatedOperation)
		}
	}
}

func TestRepoLifecycleSuccessCommitSQLRequiresConfirmedUnmountedMountEvidence(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	noSessions := sqlBetween(t, sql, "), no_sessions AS (", "), updated_repo AS (")

	assertSQLContainsInOrder(t, noSessions,
		"workload_mount_bindings",
		"status NOT IN ('released','revoked','expired','failed')",
		"confirmed_unmounted_at IS NULL",
	)
	if strings.Contains(noSessions, "AND NOT EXISTS (SELECT 1 FROM workload_mount_bindings WHERE repo_id = $15 AND status NOT IN ('released','revoked','expired','failed'))") {
		t.Fatalf("lifecycle no_sessions allows terminal mounts without confirmed non-accessing evidence: %s", noSessions)
	}
	if strings.Contains(noSessions, "unable_to_write_at") {
		t.Fatalf("lifecycle no_sessions must not accept unable_to_write_at as non-accessing evidence: %s", noSessions)
	}
}

func TestRepoLifecycleSuccessCommitSQLRequiresExportTerminalEvidence(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	noSessions := sqlBetween(t, sql, "), no_sessions AS (", "), updated_repo AS (")
	exportPredicate := sqlBetween(t, noSessions, "SELECT 1 FROM export_sessions", ") AND NOT EXISTS (SELECT 1 FROM workload_mount_bindings")

	assertSQLContainsInOrder(t, exportPredicate,
		"WHERE repo_id = $15",
		"status NOT IN ('revoked','expired','failed')",
		"terminal_observed_at IS NULL",
	)
	assertSQLContainsAll(t, exportPredicate,
		"active_request_count <> 0",
		"active_write_count <> 0",
		"status = 'failed'",
		"btrim(status_reason) = ''",
	)
}

func TestRepoLifecycleFailureCommitSQLReleaseGate(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	_, record := repoLifecycleCommitFixtures(now, operations.OperationRepoArchive, resources.RepoStatusArchived)
	record.State = operations.OperationStateOperatorInterventionRequired
	record.Phase = operations.OperationPhaseRepoLifecycleValidate
	record.Error = &operations.OperationError{Code: "FAILED", Message: "failed", CorrelationID: record.CorrelationID, OperationID: record.ID}
	exec := &fakeExecutor{row: fakeRow{values: operationRowValues(record)}}
	st := &Store{exec: exec}

	_, err := st.CommitRepoLifecycleFailedWithLease(context.Background(), record.SanitizedForPersistence(), "worker-a", now, repoLifecycleAudit(record, audit.EventTypeRepoArchive, audit.OutcomeFailed, now), "fence_alpha")
	if err != nil {
		t.Fatalf("CommitRepoLifecycleFailedWithLease: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"WITH eligible_operation AS",
		"released_fence AS",
		"updated_operation AS",
		"($20 = '' OR EXISTS (SELECT 1 FROM released_fence))",
		"inserted_audit AS",
	)
}

func TestRepoLifecycleValidatorsAcceptDeleteAndRestoreTombstonedButRejectPurge(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		typ    operations.OperationType
		target resources.RepoStatus
		edit   func(*resources.Repo, *operations.OperationRecord)
	}{
		{name: "delete", typ: operations.OperationRepoDelete, target: resources.RepoStatusTombstoned, edit: func(repo *resources.Repo, _ *operations.OperationRecord) {
			retention := now.Add(7 * 24 * time.Hour)
			repo.Lifecycle.RetentionExpiresAt = &retention
			repo.Lifecycle.PreDeleteStatus = resources.RepoStatusActive
		}},
		{name: "restore tombstoned", typ: operations.OperationRepoRestoreTombstoned, target: resources.RepoStatusActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, record := repoLifecycleCommitFixtures(now, tt.typ, tt.target)
			if tt.edit != nil {
				tt.edit(&repo, &record)
			}
			if err := validateRepoLifecycleSuccessRecord(repo, record); err != nil {
				t.Fatalf("validateRepoLifecycleSuccessRecord: %v", err)
			}
			record.State = operations.OperationStateOperatorInterventionRequired
			record.Phase = operations.OperationPhaseRepoLifecycleValidate
			record.Error = &operations.OperationError{Code: "FAILED", Message: "failed", CorrelationID: record.CorrelationID, OperationID: record.ID}
			if err := validateRepoLifecycleFailureRecord(record); err != nil {
				t.Fatalf("validateRepoLifecycleFailureRecord: %v", err)
			}
		})
	}
	repo, purge := repoLifecycleCommitFixtures(now, operations.OperationRepoPurge, resources.RepoStatusPurged)
	purge.Error = &operations.OperationError{Code: "FAILED", Message: "failed", CorrelationID: purge.CorrelationID, OperationID: purge.ID}
	if err := validateRepoLifecycleSuccessRecord(repo, purge); err == nil {
		t.Fatal("validateRepoLifecycleSuccessRecord accepted repo_purge")
	}
	purge.State = operations.OperationStateFailed
	purge.Phase = operations.OperationPhaseRepoLifecycleValidate
	if err := validateRepoLifecycleFailureRecord(purge); err == nil {
		t.Fatal("validateRepoLifecycleFailureRecord accepted repo_purge")
	}
}

func TestRepoLifecycleCommitNoRowsWrapsLeaseUnavailable(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	repo, record := repoLifecycleCommitFixtures(now, operations.OperationRepoArchive, resources.RepoStatusArchived)
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, _, err := st.CommitRepoLifecycleSucceededWithLease(context.Background(), repo, record.SanitizedForPersistence(), "worker-a", now, repoLifecycleAudit(record, audit.EventTypeRepoArchive, audit.OutcomeSucceeded, now), "fence_alpha")
	if !errors.Is(err, operations.ErrLeaseUnavailable) {
		t.Fatalf("error = %v, want lease unavailable", err)
	}
}

func repoLifecycleCommitFixtures(now time.Time, typ operations.OperationType, target resources.RepoStatus) (resources.Repo, operations.OperationRecord) {
	repo := resources.Repo{ID: "repo_alpha", NamespaceID: "ns_alpha", VolumeID: "vol_alpha", JVSRepoID: "jvs_repo_alpha", Kind: resources.RepoKindRepo, Status: target, ControlVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/control", PayloadVolumeSubdir: "afscp/namespaces/ns_alpha/repos/repo_alpha/payload", Lifecycle: resources.RepoLifecycle{Status: target, LastLifecycleOperationID: "op_lifecycle"}, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	lease := now.Add(time.Minute)
	started := now.Add(-time.Minute)
	record := operations.OperationRecord{ID: "op_lifecycle", Type: typ, State: operations.OperationStateSucceeded, Phase: operations.OperationPhaseRepoLifecycleCommitted, LeaseOwner: "worker-a", LeaseExpiresAt: &lease, Attempt: 1, IdempotencyScope: operations.NewIdempotencyScope("product-caller", "ns_alpha", typ, "idem").String(), IdempotencyKey: "idem", RequestHash: "sha256:lifecycle", CorrelationID: "corr", CallerService: "product-caller", AuthorizedActor: operations.Actor{Type: "user", ID: "user_123"}, Resource: operations.ResourceRef{Type: "repo", ID: "repo_alpha"}, NamespaceID: "ns_alpha", RepoID: "repo_alpha", InputSummary: map[string]any{}, CreatedAt: now.Add(-time.Hour), StartedAt: &started, FinishedAt: &now}
	return repo, record
}

func repoLifecycleAudit(record operations.OperationRecord, typ audit.EventType, outcome audit.Outcome, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{EventID: "evt_lifecycle", Type: typ, Time: now, CallerService: record.CallerService, AuthorizedActor: audit.Actor{Type: record.AuthorizedActor.Type, ID: record.AuthorizedActor.ID}, CorrelationID: record.CorrelationID, OperationID: record.ID, Resource: audit.Resource{Type: "repo", ID: record.RepoID, NamespaceID: record.NamespaceID}, Outcome: outcome, Reason: "repo_lifecycle"})
}

var _ store.RepoLifecycleOperationRecoveryStore = (*Store)(nil)
