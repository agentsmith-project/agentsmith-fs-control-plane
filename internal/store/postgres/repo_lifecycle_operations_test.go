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
		"o.operation_type IN ('save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone')",
		"o.operation_state NOT IN ('succeeded','failed','cancelled')",
		"active_restore_plan AS",
		"FROM restore_plans p, eligible_operation e",
		"p.repo_id = e.repo_id",
		"p.status IN ('pending', 'consuming', 'discarding', 'operator_intervention_required')",
		"updated_operation AS",
		"UPDATE operations SET",
		"$5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
		"$5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM active_restore_plan)",
		"RETURNING",
	)
	if strings.Contains(exec.query, "operation_type IN ('repo_archive', 'repo_restore_archived', 'repo_delete', 'repo_restore_tombstoned', 'repo_purge')") {
		t.Fatalf("repo lifecycle acquire SQL included repo_purge in lifecycle scope: %s", exec.query)
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
				"active_restore_plan AS",
				"updated_operation AS",
				"$5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)",
				"$5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM active_restore_plan)",
				"SELECT",
				"FROM updated_operation",
			)
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
		"repos.volume_id = active_volume.volume_id",
		"repos.jvs_repo_id = $23",
		"repos.repo_kind = $24",
		"repos.control_volume_subdir = $26",
		"repos.payload_volume_subdir = $27",
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

func TestRepoLifecycleSuccessCommitSQLHasDeleteAndRestoreTombstonedPredicates(t *testing.T) {
	sql := repoLifecycleSuccessCommitWithLeaseSQL()
	assertSQLContainsInOrder(t, sql,
		"eligible_operation.operation_type = 'repo_delete'",
		"repos.status IN ('active','archived')",
		"$25 = 'tombstoned'",
		"$28 = 'tombstoned'",
		"$29 IS NOT NULL",
		"$31 = repos.status",
	)
	assertSQLContainsInOrder(t, sql,
		"eligible_operation.operation_type = 'repo_restore_tombstoned'",
		"repos.status = 'tombstoned'",
		"eligible_operation.created_at < repos.retention_expires_at",
		"eligible_operation.created_at > repos.updated_at",
		"$25 = repos.pre_delete_status",
		"$28 = repos.pre_delete_status",
		"$29 IS NULL",
		"$31 IS NULL",
	)
	if strings.Contains(sql, "repo_purge") {
		t.Fatalf("success SQL includes repo_purge: %s", sql)
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
