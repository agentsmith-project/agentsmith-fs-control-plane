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

func (store *Store) CommitRepoLifecycleSucceededWithLease(ctx context.Context, repo resources.Repo, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	if err := repo.Validate(); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	record := sanitized.Record()
	if err := validateRepoLifecycleSuccessRecord(repo, record); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := validateRepoLifecycleAuditEvent(repo, record, event, audit.OutcomeSucceeded); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if strings.TrimSpace(fenceID) == "" {
		return resources.Repo{}, operations.OperationRecord{}, operationLeaseInvalidRequest("fence_id", "repo lifecycle success requires lifecycle fence id")
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
	row := store.exec.QueryRowContext(ctx, repoLifecycleSuccessCommitWithLeaseSQL(), args...)
	gotRepo, gotOperation, err := scanRepoAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.Repo{}, operations.OperationRecord{}, operationLeaseUnavailable("repo lifecycle success commit", record.ID, err)
		}
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	return gotRepo, gotOperation, nil
}

func (store *Store) CommitRepoLifecycleFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRepoLifecycleFailureRecord(record); err != nil {
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
	row := store.exec.QueryRowContext(ctx, repoLifecycleFailureCommitWithLeaseSQL(), args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("repo lifecycle failure commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func validateRepoLifecycleSuccessRecord(repo resources.Repo, record operations.OperationRecord) error {
	if record.Type != operations.OperationRepoArchive && record.Type != operations.OperationRepoRestoreArchived {
		return operationLeaseInvalidRequest("operation_type", "operation record must be supported repo lifecycle type")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "repo lifecycle success requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseRepoLifecycleCommitted {
		return operationLeaseInvalidRequest("phase", "repo lifecycle success requires committed terminal phase")
	}
	if record.NamespaceID != repo.NamespaceID || record.RepoID != repo.ID || record.Resource.Type != "repo" || record.Resource.ID != repo.ID {
		return operationLeaseInvalidRequest("resource", "repo lifecycle operation resource must match repo")
	}
	if repo.Kind != resources.RepoKindRepo || repo.Status != repo.Lifecycle.Status || repo.Lifecycle.LastLifecycleOperationID != record.ID {
		return operationLeaseInvalidRequest("repo_lifecycle", "repo lifecycle metadata must match terminal operation")
	}
	return nil
}

func validateRepoLifecycleFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRepoArchive && record.Type != operations.OperationRepoRestoreArchived {
		return operationLeaseInvalidRequest("operation_type", "operation record must be supported repo lifecycle type")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "repo lifecycle failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseRepoLifecycleValidate {
		return operationLeaseInvalidRequest("phase", "repo lifecycle failure stays in validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID || record.Error == nil {
		return operationLeaseInvalidRequest("resource", "repo lifecycle failure requires target repo and error")
	}
	return nil
}

func validateRepoLifecycleAuditEvent(repo resources.Repo, record operations.OperationRecord, event audit.Event, outcome audit.Outcome) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	wantType, _ := audit.EventTypeForOperationType(string(record.Type))
	if event.Type != wantType || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event_type", "repo lifecycle audit event must match operation outcome")
	}
	if event.Resource.Type != "repo" || event.Resource.ID != repo.ID || event.Resource.NamespaceID != repo.NamespaceID {
		return auditOutboxInvalidRequest("resource", "repo lifecycle audit resource must match repo metadata")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "repo lifecycle audit caller context must match operation")
	}
	return nil
}

func validateRepoLifecycleFailureAuditEvent(record operations.OperationRecord, event audit.Event) error {
	repo := resources.Repo{ID: record.RepoID, NamespaceID: record.NamespaceID}
	return validateRepoLifecycleAuditEvent(repo, record, event, audit.OutcomeFailed)
}

func repoLifecycleStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.NamespaceID, record.RepoID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID}
}

func repoLifecycleSuccessCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, operation_type FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type IN ('repo_archive', 'repo_restore_archived') AND phase = 'validate_repo_lifecycle' AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), active_namespace AS (" +
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'" +
		"), active_binding AS (" +
		"SELECT namespace_id FROM namespace_volume_bindings WHERE namespace_id = $14 AND status = 'active'" +
		"), active_volume AS (" +
		"SELECT volume_id FROM volumes WHERE volume_id = $22 AND status = 'active' AND capabilities->>'jvs_external_control_root' = 'true'" +
		"), held_fence AS (" +
		"SELECT fence_id FROM repo_fences WHERE repo_id = $15 AND fence_id = $34 AND fence_kind = 'lifecycle' AND holder_operation_id = $12 AND status = 'active' AND released_at IS NULL AND recovered_at IS NULL FOR UPDATE" +
		"), no_sessions AS (" +
		"SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM export_sessions WHERE repo_id = $15 AND status NOT IN ('revoked','expired','failed')) AND NOT EXISTS (SELECT 1 FROM workload_mount_bindings WHERE repo_id = $15 AND status NOT IN ('released','revoked','expired','failed'))" +
		"), updated_repo AS (" +
		"UPDATE repos SET status = $25, lifecycle_status = $28, retention_expires_at = $29, last_lifecycle_operation_id = $30, pre_delete_status = $31, updated_at = $33 " +
		"FROM eligible_operation, active_namespace, active_binding, active_volume, held_fence, no_sessions WHERE repos.repo_id = $15 AND repos.namespace_id = $14 AND repos.volume_id = active_volume.volume_id AND repos.volume_id = $22 AND repos.jvs_repo_id = $23 AND repos.repo_kind = $24 AND repos.control_volume_subdir = $26 AND repos.payload_volume_subdir = $27 AND ((eligible_operation.operation_type = 'repo_archive' AND repos.status = 'active') OR (eligible_operation.operation_type = 'repo_restore_archived' AND repos.status = 'archived')) RETURNING " + strings.Join(repoColumns, ", ") +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() + "FROM eligible_operation, updated_repo WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), released_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM updated_operation, held_fence WHERE repo_fences.repo_id = $15 AND repo_fences.fence_id = held_fence.fence_id RETURNING repo_fences.fence_id" +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(35, len(auditOutboxColumns)) + " FROM updated_operation, updated_repo, released_fence RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("updated_repo", repoColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM updated_repo, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func repoLifecycleFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type IN ('repo_archive', 'repo_restore_archived') AND phase = 'validate_repo_lifecycle' AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), released_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM eligible_operation WHERE $20 <> '' AND repo_fences.repo_id = $15 AND repo_fences.fence_id = $20 AND repo_fences.fence_kind = 'lifecycle' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL RETURNING repo_fences.fence_id" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() + "FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id AND ($20 = '' OR EXISTS (SELECT 1 FROM released_fence)) RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(21, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}
