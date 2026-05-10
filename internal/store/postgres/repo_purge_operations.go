package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func (store *Store) CommitRepoPurgeSucceededWithLease(ctx context.Context, repo resources.Repo, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	if err := repo.Validate(); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	record := sanitized.Record()
	if err := validateRepoPurgeSuccessRecord(repo, record); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := validateRepoLifecycleAuditEvent(repo, record, event, audit.OutcomeSucceeded); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if strings.TrimSpace(fenceID) == "" {
		return resources.Repo{}, operations.OperationRecord{}, operationLeaseInvalidRequest("fence_id", "repo purge success requires lifecycle fence id")
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, repoLifecycleStoredPredicateArgs(record)...)
	args = append(args, repoArgs(repo)...)
	args = append(args, strings.TrimSpace(fenceID))
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, repoPurgeSuccessCommitWithLeaseSQL(), args...)
	gotRepo, gotOperation, err := scanRepoAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.Repo{}, operations.OperationRecord{}, operationLeaseUnavailable("repo purge success commit", record.ID, err)
		}
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	return gotRepo, gotOperation, nil
}

func (store *Store) CommitRepoPurgeFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRepoPurgeFailureRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateRepoLifecycleFailureAuditEvent(record, event); err != nil {
		return operations.OperationRecord{}, err
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	args := append(operationArgs, repoLifecycleStoredPredicateArgs(record)...)
	args = append(args, strings.TrimSpace(releaseFenceID))
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, repoPurgeFailureCommitWithLeaseSQL(), args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("repo purge failure commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func validateRepoPurgeSuccessRecord(repo resources.Repo, record operations.OperationRecord) error {
	if record.Type != operations.OperationRepoPurge {
		return operationLeaseInvalidRequest("operation_type", "operation record must be repo_purge")
	}
	if record.State != operations.OperationStateSucceeded || record.Phase != operations.OperationPhaseRepoLifecycleCommitted {
		return operationLeaseInvalidRequest("operation_state", "repo purge success requires succeeded committed operation")
	}
	if record.NamespaceID != repo.NamespaceID || record.RepoID != repo.ID || record.Resource.Type != "repo" || record.Resource.ID != repo.ID {
		return operationLeaseInvalidRequest("resource", "repo purge operation resource must match repo")
	}
	if repo.Kind != resources.RepoKindRepo || repo.Status != resources.RepoStatusPurged || repo.Lifecycle.Status != resources.RepoStatusPurged || repo.Lifecycle.LastLifecycleOperationID != record.ID || repo.Lifecycle.RetentionExpiresAt != nil || (repo.Lifecycle.PreDeleteStatus != resources.RepoStatusActive && repo.Lifecycle.PreDeleteStatus != resources.RepoStatusArchived) {
		return operationLeaseInvalidRequest("repo_lifecycle", "repo purge metadata must match terminal operation")
	}
	return nil
}

func validateRepoPurgeFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRepoPurge {
		return operationLeaseInvalidRequest("operation_type", "operation record must be repo_purge")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "repo purge failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseRepoLifecycleValidate || strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID || record.Error == nil {
		return operationLeaseInvalidRequest("resource", "repo purge failure requires target repo and error")
	}
	return nil
}

func repoPurgeSuccessCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, created_at FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'repo_purge' AND phase = 'validate_repo_lifecycle' AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), held_fence AS (" +
		"SELECT fence_id FROM repo_fences WHERE repo_id = $15 AND fence_id = $34 AND fence_kind = 'lifecycle' AND holder_operation_id = $12 AND status = 'active' AND released_at IS NULL AND recovered_at IS NULL FOR UPDATE" +
		"), no_sessions AS (" +
		"SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM export_sessions WHERE repo_id = $15 AND (status NOT IN ('revoked','expired','failed') OR terminal_observed_at IS NULL OR active_request_count <> 0 OR active_write_count <> 0 OR (status = 'failed' AND btrim(status_reason) = ''))) AND NOT EXISTS (SELECT 1 FROM workload_mount_bindings WHERE repo_id = $15 AND (status NOT IN ('released','revoked','expired','failed') OR confirmed_unmounted_at IS NULL))" +
		"), no_earlier_lifecycle AS (" +
		"SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM operations earlier WHERE earlier.repo_id = $15 AND (earlier.created_at < (SELECT created_at FROM eligible_operation) OR (earlier.created_at = (SELECT created_at FROM eligible_operation) AND earlier.operation_id < $12)) AND earlier.operation_type IN ('repo_archive','repo_restore_archived','repo_delete','repo_restore_tombstoned','repo_purge') AND earlier.operation_state NOT IN ('succeeded','failed','cancelled'))" +
		"), updated_repo AS (" +
		"UPDATE repos SET status = $25, lifecycle_status = $28, retention_expires_at = $29::timestamptz, last_lifecycle_operation_id = $30, pre_delete_status = $31::text, updated_at = $33 " +
		"FROM eligible_operation, held_fence, no_sessions, no_earlier_lifecycle WHERE repos.repo_id = $20 AND repos.repo_id = $15 AND repos.namespace_id = $21 AND repos.namespace_id = $14 AND repos.volume_id = $22 AND repos.jvs_repo_id = $23 AND repos.repo_kind = $24 AND repos.control_volume_subdir = $26 AND repos.payload_volume_subdir = $27 AND repos.created_at = $32 AND repos.status = 'tombstoned' AND eligible_operation.created_at > repos.updated_at AND $25 = 'purged' AND $28 = 'purged' AND $29::timestamptz IS NULL AND $31::text = repos.pre_delete_status RETURNING " + repoReturningColumnsSQL() +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() + "FROM eligible_operation, updated_repo WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), released_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM updated_operation, held_fence WHERE repo_fences.repo_id = $15 AND repo_fences.fence_id = held_fence.fence_id RETURNING repo_fences.fence_id" +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(35, len(auditOutboxColumns)) + " FROM updated_operation, updated_repo, released_fence RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("updated_repo", repoColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM updated_repo, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func repoPurgeFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'repo_purge' AND phase = 'validate_repo_lifecycle' AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() + "FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id AND $20 = '' RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(21, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}
