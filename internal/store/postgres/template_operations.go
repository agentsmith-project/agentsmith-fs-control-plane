package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func (store *Store) MarkTemplateCreateWriterFencedWithLease(ctx context.Context, fence fences.Fence, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateTemplateCreateWriterFencedRecord(record, fence); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	args = append(args, record.NamespaceID, record.RepoID, record.TemplateID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID, record.SessionFenceID)
	args = append(args, repoFenceInsertArgs(fence)...)
	row := store.exec.QueryRowContext(ctx, templateCreateWriterFencedMarkWithLeaseSQL(), args...)
	gotFence, gotOperation, err := scanRepoFenceAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fences.Fence{}, operations.OperationRecord{}, operationLeaseUnavailable("template create writer fenced mark", record.ID, err)
		}
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	return gotFence, gotOperation, nil
}

func (store *Store) CommitTemplateCreateSucceededWithLease(ctx context.Context, template resources.Repo, sourceRepoID, sourceSavePointID, cloneHistoryMode string, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateTemplateCreateSuccessRecord(template, record, sourceRepoID, sourceSavePointID, cloneHistoryMode); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := validateTemplateAuditEvent(template, record, event, audit.EventTypeTemplateCreate, audit.OutcomeSucceeded); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	return store.commitTemplateRepoSuccess(ctx, template, record, owner, now, event, "template create success commit", templateCreateSuccessCommitWithLeaseSQL(), true, sourceRepoID, sourceSavePointID, cloneHistoryMode)
}

func (store *Store) CommitTemplateCreateFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateTemplateFailureRecord(record, operations.OperationTemplateCreate, operations.OperationPhaseTemplateCreateValidate); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateTemplateFailureAuditEvent(record, event, audit.EventTypeTemplateCreate); err != nil {
		return operations.OperationRecord{}, err
	}
	return store.commitTemplateFailure(ctx, record, owner, now, event, "template create failure commit")
}

func (store *Store) CommitTemplateCloneSucceededWithLease(ctx context.Context, repo resources.Repo, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Repo, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateTemplateCloneSuccessRecord(repo, record); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := validateTemplateAuditEvent(repo, record, event, audit.EventTypeTemplateClone, audit.OutcomeSucceeded); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	return store.commitTemplateRepoSuccess(ctx, repo, record, owner, now, event, "template clone success commit", templateCloneSuccessCommitWithLeaseSQL(), false, "", "", "")
}

func (store *Store) CommitTemplateCloneFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateTemplateFailureRecord(record, operations.OperationTemplateClone, operations.OperationPhaseTemplateCloneValidate); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateTemplateFailureAuditEvent(record, event, audit.EventTypeTemplateClone); err != nil {
		return operations.OperationRecord{}, err
	}
	return store.commitTemplateFailure(ctx, record, owner, now, event, "template clone failure commit")
}

func (store *Store) commitTemplateRepoSuccess(ctx context.Context, repo resources.Repo, record operations.OperationRecord, owner string, now time.Time, event audit.Event, label, query string, withProvenance bool, sourceRepoID, sourceSavePointID, cloneHistoryMode string) (resources.Repo, operations.OperationRecord, error) {
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, record.NamespaceID, record.RepoID, record.TemplateID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID)
	args = append(args, repoArgs(repo)...)
	if withProvenance {
		args = append(args, sourceRepoID, sourceSavePointID, cloneHistoryMode, record.SessionFenceID)
	}
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, query, args...)
	gotRepo, gotOperation, err := scanRepoAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.Repo{}, operations.OperationRecord{}, operationLeaseUnavailable(label, record.ID, err)
		}
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	return gotRepo, gotOperation, nil
}

func (store *Store) commitTemplateFailure(ctx context.Context, record operations.OperationRecord, owner string, now time.Time, event audit.Event, label string) (operations.OperationRecord, error) {
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	args := append(operationArgs, record.NamespaceID, record.RepoID, record.TemplateID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID, record.SessionFenceID)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, templateFailureCommitWithLeaseSQL(), args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable(label, record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func validateTemplateCreateWriterFencedRecord(record operations.OperationRecord, fence fences.Fence) error {
	if record.Type != operations.OperationTemplateCreate || record.State != operations.OperationStateRunning || record.Phase != operations.OperationPhaseTemplateCreateWriterFenced {
		return operationLeaseInvalidRequest("operation", "template create writer-fenced mark requires running writer-fenced operation")
	}
	if strings.TrimSpace(record.SessionFenceID) == "" || record.SessionFenceID != fence.ID {
		return operationLeaseInvalidRequest("session_fence_id", "template create writer-fenced mark requires matching session fence id")
	}
	if err := fences.ValidateFence(fence); err != nil {
		return err
	}
	if fence.Kind != fences.KindWriterSession || fence.Status != fences.StatusActive || fence.RepoID != record.RepoID || fence.HolderOperationID != record.ID {
		return operationLeaseInvalidRequest("session_fence_id", "template create writer fence must be active and owned by the operation")
	}
	if record.Resource.Type != "repo_template" || record.Resource.ID != record.TemplateID {
		return operationLeaseInvalidRequest("resource", "template create resource must match template")
	}
	return nil
}

func validateTemplateCreateSuccessRecord(template resources.Repo, record operations.OperationRecord, sourceRepoID, sourceSavePointID, cloneHistoryMode string) error {
	if err := template.Validate(); err != nil {
		return err
	}
	if record.Type != operations.OperationTemplateCreate || record.State != operations.OperationStateSucceeded || record.Phase != operations.OperationPhaseTemplateCreateCommitted {
		return operationLeaseInvalidRequest("operation", "template create success requires succeeded committed operation")
	}
	if strings.TrimSpace(record.SessionFenceID) == "" {
		return operationLeaseInvalidRequest("session_fence_id", "template create success requires source writer fence id")
	}
	if template.Kind != resources.RepoKindTemplate || template.ID != record.TemplateID || template.NamespaceID != record.NamespaceID {
		return operationLeaseInvalidRequest("template", "template create success metadata must match operation")
	}
	if strings.TrimSpace(sourceRepoID) == "" || sourceRepoID != record.RepoID || strings.TrimSpace(sourceSavePointID) == "" || cloneHistoryMode != "main" {
		return operationLeaseInvalidRequest("provenance", "template create success requires source provenance")
	}
	if record.Resource.Type != "repo_template" || record.Resource.ID != record.TemplateID {
		return operationLeaseInvalidRequest("resource", "template create resource must match template")
	}
	return nil
}

func validateTemplateCloneSuccessRecord(repo resources.Repo, record operations.OperationRecord) error {
	if err := repo.Validate(); err != nil {
		return err
	}
	if record.Type != operations.OperationTemplateClone || record.State != operations.OperationStateSucceeded || record.Phase != operations.OperationPhaseTemplateCloneCommitted {
		return operationLeaseInvalidRequest("operation", "template clone success requires succeeded committed operation")
	}
	if repo.Kind != resources.RepoKindRepo || repo.ID != record.RepoID || repo.NamespaceID != record.NamespaceID || strings.TrimSpace(record.TemplateID) == "" {
		return operationLeaseInvalidRequest("repo", "template clone success metadata must match operation")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "template clone resource must match repo")
	}
	return nil
}

func validateTemplateFailureRecord(record operations.OperationRecord, typ operations.OperationType, phase string) error {
	if record.Type != typ {
		return operationLeaseInvalidRequest("operation", "template failure operation type or phase mismatch")
	}
	if record.Phase != phase {
		if typ != operations.OperationTemplateCreate || record.Phase != operations.OperationPhaseTemplateCreateWriterFenced {
			return operationLeaseInvalidRequest("operation", "template failure operation type or phase mismatch")
		}
	}
	if record.Phase == operations.OperationPhaseTemplateCreateWriterFenced && strings.TrimSpace(record.SessionFenceID) == "" {
		return operationLeaseInvalidRequest("session_fence_id", "template create post-fence failure requires session fence id")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "template failure requires failed or intervention state")
	}
	if record.Error == nil || strings.TrimSpace(record.NamespaceID) == "" {
		return operationLeaseInvalidRequest("error", "template failure requires operation error")
	}
	return nil
}

func validateTemplateAuditEvent(repo resources.Repo, record operations.OperationRecord, event audit.Event, eventType audit.EventType, outcome audit.Outcome) error {
	if event.OperationID != record.ID || event.Type != eventType || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event", "template audit event must match operation")
	}
	if event.Resource.ID != repo.ID || event.Resource.NamespaceID != repo.NamespaceID || event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID {
		return auditOutboxInvalidRequest("resource", "template audit event must match resource and caller")
	}
	return nil
}

func validateTemplateFailureAuditEvent(record operations.OperationRecord, event audit.Event, eventType audit.EventType) error {
	if event.OperationID != record.ID || event.Type != eventType || event.Outcome != audit.OutcomeFailed {
		return auditOutboxInvalidRequest("event", "template failure audit event must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID {
		return auditOutboxInvalidRequest("caller", "template failure audit caller must match operation")
	}
	return nil
}

func templateCreateSuccessCommitWithLeaseSQL() string {
	insertColumns := append([]string{}, repoColumns...)
	insertColumns = append(insertColumns, "source_repo_id", "source_save_point_id", "clone_history_mode")
	insertValues := placeholders(21, len(repoColumns)) + ", $35, $36, $37"
	auditPlaceholderStart := 39
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'template_create' AND phase = '" + operations.OperationPhaseTemplateCreateWriterFenced + "' AND namespace_id = $14 AND repo_id = $15 AND template_id = $16 " +
		"AND session_fence_id = $38 AND caller_service = $17 AND correlation_id = $18 AND authorized_actor_type = $19 AND authorized_actor_id = $20 FOR UPDATE" +
		"), active_namespace AS (" +
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'" +
		"), active_binding AS (" +
		"SELECT namespace_id, default_volume_id FROM namespace_volume_bindings WHERE namespace_id = $14 AND status = 'active'" +
		"), active_volume AS (" +
		"SELECT volume_id FROM volumes, active_binding WHERE volume_id = $23 AND volume_id = active_binding.default_volume_id AND status = 'active' AND capabilities->>'jvs_external_control_root' = 'true'" +
		"), template_create_prerequisites AS (" +
		"SELECT eligible_operation.operation_id FROM eligible_operation, active_namespace, active_binding, active_volume WHERE NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $21)" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, template_create_prerequisites WHERE repo_fences.repo_id = $15 AND repo_fences.fence_id = $38 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_writer_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM held_writer_fence WHERE repo_fences.fence_id = held_writer_fence.fence_id RETURNING repo_fences.fence_id" +
		"), inserted_repo AS (" +
		"INSERT INTO repos (" + strings.Join(insertColumns, ", ") + ") SELECT " + insertValues + " FROM template_create_prerequisites, released_writer_fence RETURNING " + strings.Join(repoColumns, ", ") +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, inserted_repo, released_writer_fence WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(auditPlaceholderStart, len(auditOutboxColumns)) + " FROM updated_operation, inserted_repo, released_writer_fence RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("inserted_repo", repoColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM inserted_repo, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func templateCloneSuccessCommitWithLeaseSQL() string {
	return templateSuccessCommitWithLeaseSQL("template_clone", operations.OperationPhaseTemplateCloneValidate, false)
}

func templateSuccessCommitWithLeaseSQL(operationType, phase string, withProvenance bool) string {
	insertColumns := append([]string{}, repoColumns...)
	insertValues := placeholders(21, len(repoColumns))
	auditPlaceholderStart := 21 + len(repoColumns)
	if withProvenance {
		insertColumns = append(insertColumns, "source_repo_id", "source_save_point_id", "clone_history_mode")
		insertValues += ", $35, $36, $37"
		auditPlaceholderStart += 4
	}
	common := "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = '" + operationType + "' AND phase = '" + phase + "' AND namespace_id = $14 AND repo_id = $15 AND template_id = $16 " +
		"AND caller_service = $17 AND correlation_id = $18 AND authorized_actor_type = $19 AND authorized_actor_id = $20 FOR UPDATE" +
		"), active_namespace AS (" +
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'" +
		"), active_binding AS (" +
		"SELECT namespace_id, default_volume_id FROM namespace_volume_bindings WHERE namespace_id = $14 AND status = 'active'" +
		"), active_volume AS (" +
		"SELECT volume_id FROM volumes, active_binding WHERE volume_id = $23 AND volume_id = active_binding.default_volume_id AND status = 'active' AND capabilities->>'jvs_external_control_root' = 'true'" +
		"), inserted_repo AS (" +
		"INSERT INTO repos (" + strings.Join(insertColumns, ", ") + ") SELECT " + insertValues + " FROM eligible_operation, active_namespace, active_binding, active_volume WHERE NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $21) RETURNING " + strings.Join(repoColumns, ", ") +
		")"
	if !withProvenance {
		return common + ", updated_operation AS (" +
			operationLeaseFencedUpdateSetSQL() +
			"FROM eligible_operation, inserted_repo WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
			"), inserted_audit AS (" +
			"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(auditPlaceholderStart, len(auditOutboxColumns)) + " FROM updated_operation, inserted_repo RETURNING audit_event_id" +
			") SELECT " + prefixedColumns("inserted_repo", repoColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM inserted_repo, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
	}
	return common + ", held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, eligible_operation, inserted_repo WHERE repo_fences.repo_id = $15 AND repo_fences.fence_id = $38 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_writer_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM held_writer_fence WHERE repo_fences.fence_id = held_writer_fence.fence_id RETURNING repo_fences.fence_id" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, inserted_repo, released_writer_fence WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(auditPlaceholderStart, len(auditOutboxColumns)) + " FROM updated_operation, inserted_repo, released_writer_fence RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("inserted_repo", repoColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM inserted_repo, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func templateFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, operation_type, phase FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND ((operation_type = 'template_create' AND phase IN ('validate_template_create','template_create_writer_fenced')) OR (operation_type = 'template_clone' AND phase = 'validate_template_clone')) AND namespace_id = $14 AND repo_id = $15 AND template_id = $16 " +
		"AND caller_service = $17 AND correlation_id = $18 AND authorized_actor_type = $19 AND authorized_actor_id = $20 FOR UPDATE" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, eligible_operation WHERE eligible_operation.phase = 'template_create_writer_fenced' AND repo_fences.repo_id = $15 AND repo_fences.fence_id = $21 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_writer_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM held_writer_fence WHERE $1 = 'failed' AND repo_fences.fence_id = held_writer_fence.fence_id RETURNING repo_fences.fence_id" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id AND (eligible_operation.phase <> 'template_create_writer_fenced' OR $1 = 'operator_intervention_required' OR EXISTS (SELECT 1 FROM released_writer_fence)) RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(22, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func templateCreateWriterFencedMarkWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'template_create' AND phase = 'validate_template_create' AND namespace_id = $14 AND repo_id = $15 AND template_id = $16 " +
		"AND resource_type = 'repo_template' AND resource_id = $16 AND caller_service = $17 AND correlation_id = $18 AND authorized_actor_type = $19 AND authorized_actor_id = $20 FOR UPDATE" +
		"), locked_repo AS (" +
		"SELECT repo_id FROM repos, eligible_operation WHERE repos.namespace_id = $14 AND repos.repo_id = $15 FOR UPDATE" +
		"), held_lifecycle_fence AS (" +
		"SELECT fence_id FROM repo_fences, locked_repo WHERE repo_fences.repo_id = locked_repo.repo_id AND repo_fences.fence_kind = 'lifecycle' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), active_writer_fence AS (" +
		"SELECT " + prefixedColumns("repo_fences", repoFenceColumns) + " FROM repo_fences, locked_repo WHERE repo_fences.repo_id = locked_repo.repo_id AND repo_fences.fence_id = $21 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), inserted_writer_fence AS (" +
		"INSERT INTO repo_fences (" + strings.Join(repoFenceColumns, ", ") + ") SELECT " + placeholders(22, len(repoFenceColumns)) + " FROM eligible_operation, locked_repo WHERE NOT EXISTS (SELECT 1 FROM active_writer_fence) AND NOT EXISTS (SELECT 1 FROM held_lifecycle_fence) ON CONFLICT (repo_id, fence_kind) WHERE released_at IS NULL DO NOTHING RETURNING " + strings.Join(repoFenceColumns, ", ") +
		"), confirmed_writer_fence AS (" +
		"SELECT " + strings.Join(repoFenceColumns, ", ") + " FROM active_writer_fence UNION ALL SELECT " + strings.Join(repoFenceColumns, ", ") + " FROM inserted_writer_fence LIMIT 1" +
		"), updated_operation AS (" +
		restoreWriterFencedOperationUpdateSetSQL() +
		"FROM eligible_operation, confirmed_writer_fence WHERE operations.operation_id = eligible_operation.operation_id AND confirmed_writer_fence.fence_id = $21 AND NOT EXISTS (SELECT 1 FROM held_lifecycle_fence) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + prefixedColumns("confirmed_writer_fence", repoFenceColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM confirmed_writer_fence, updated_operation"
}
