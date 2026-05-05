package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func (store *Store) CommitRepoCreateSucceededWithLease(ctx context.Context, repo resources.Repo, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, fenceID string) (resources.Repo, operations.OperationRecord, error) {
	if err := repo.Validate(); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	record := sanitized.Record()
	if err := validateRepoCreateSuccessRecord(repo, record); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := validateRepoCreateAuditEvent(repo, record, event, audit.OutcomeSucceeded); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	fenceID = strings.TrimSpace(fenceID)
	if fenceID == "" {
		return resources.Repo{}, operations.OperationRecord{}, operationLeaseInvalidRequest("fence_id", "repo create success requires target fence id")
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, repoCreateStoredPredicateArgs(record)...)
	args = append(args, repoArgs(repo)...)
	args = append(args, fenceID)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)

	row := store.exec.QueryRowContext(ctx, repoCreateSuccessCommitWithLeaseSQL(), args...)
	gotRepo, gotOperation, err := scanRepoAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.Repo{}, operations.OperationRecord{}, operationLeaseUnavailable("repo create success commit", record.ID, err)
		}
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	return gotRepo, gotOperation, nil
}

func (store *Store) CommitRepoCreateFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event, releaseFenceID string) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRepoCreateFailureRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateRepoCreateFailureAuditEvent(record, event); err != nil {
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
	args := append(operationArgs, repoCreateFailureStoredPredicateArgs(record)...)
	args = append(args, strings.TrimSpace(releaseFenceID))
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)

	row := store.exec.QueryRowContext(ctx, repoCreateFailureCommitWithLeaseSQL(), args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("repo create failure commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func validateRepoCreateSuccessRecord(repo resources.Repo, record operations.OperationRecord) error {
	if record.Type != operations.OperationRepoCreate {
		return operationLeaseInvalidRequest("operation_type", "operation record must be repo_create")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "repo create success requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseRepoCreateCommitted {
		return operationLeaseInvalidRequest("phase", "repo create success requires committed terminal phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || record.NamespaceID != repo.NamespaceID {
		return operationLeaseInvalidRequest("namespace_id", "operation namespace must match repo metadata")
	}
	if strings.TrimSpace(record.RepoID) == "" || record.RepoID != repo.ID {
		return operationLeaseInvalidRequest("repo_id", "operation repo id must match repo metadata")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != repo.ID {
		return operationLeaseInvalidRequest("resource", "operation resource must match repo metadata")
	}
	if repo.Kind != resources.RepoKindRepo || repo.Status != resources.RepoStatusActive || repo.Lifecycle.Status != resources.RepoStatusActive {
		return operationLeaseInvalidRequest("repo_status", "repo create success requires active ordinary repo metadata")
	}
	if repo.Lifecycle.LastLifecycleOperationID != record.ID {
		return operationLeaseInvalidRequest("last_lifecycle_operation_id", "repo lifecycle must reference repo create operation")
	}
	return nil
}

func validateRepoCreateFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRepoCreate {
		return operationLeaseInvalidRequest("operation_type", "operation record must be repo_create")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "repo create failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseRepoCreateValidate {
		return operationLeaseInvalidRequest("phase", "repo create failure stays in validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" {
		return operationLeaseInvalidRequest("resource", "repo create failure requires namespace and repo ids")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "operation resource must match target repo")
	}
	if record.Error == nil {
		return operationLeaseInvalidRequest("error", "repo create failure requires sanitized operation error")
	}
	return nil
}

func validateRepoCreateAuditEvent(repo resources.Repo, record operations.OperationRecord, event audit.Event, outcome audit.Outcome) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeRepoCreate || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event_type", "repo create audit event must match operation outcome")
	}
	if event.Resource.Type != "repo" || event.Resource.ID != repo.ID || event.Resource.NamespaceID != repo.NamespaceID {
		return auditOutboxInvalidRequest("resource", "repo create audit resource must match repo metadata")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "repo create audit caller context must match operation")
	}
	return nil
}

func validateRepoCreateFailureAuditEvent(record operations.OperationRecord, event audit.Event) error {
	repo := resources.Repo{ID: record.RepoID, NamespaceID: record.NamespaceID}
	return validateRepoCreateAuditEvent(repo, record, event, audit.OutcomeFailed)
}

func repoCreateStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.NamespaceID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID}
}

func repoCreateFailureStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.NamespaceID, record.RepoID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID}
}

func repoCreateSuccessCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations " +
		"WHERE operation_id = $12 " +
		"AND operation_state = 'running' " +
		"AND lease_owner = $13 " +
		"AND lease_expires_at IS NOT NULL " +
		"AND lease_expires_at > $11 " +
		"AND operation_type = 'repo_create' " +
		"AND phase = 'validate_repo_create' " +
		"AND namespace_id = $14 " +
		"AND repo_id = $19 " +
		"AND resource_type = 'repo' " +
		"AND resource_id = $19 " +
		"AND caller_service = $15 " +
		"AND correlation_id = $16 " +
		"AND authorized_actor_type = $17 " +
		"AND authorized_actor_id = $18 " +
		"FOR UPDATE" +
		"), active_namespace AS (" +
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'" +
		"), active_binding AS (" +
		"SELECT namespace_id, default_volume_id FROM namespace_volume_bindings WHERE namespace_id = $14 AND status = 'active'" +
		"), active_volume AS (" +
		"SELECT volume_id FROM volumes, active_binding WHERE volume_id = $21 AND volume_id = active_binding.default_volume_id AND status = 'active' AND capabilities->>'jvs_external_control_root' = 'true'" +
		"), held_fence AS (" +
		"SELECT fence_id FROM repo_fences WHERE repo_id = $19 AND fence_id = $33 AND fence_kind = 'lifecycle' AND holder_operation_id = $12 AND status = 'active' AND released_at IS NULL AND recovered_at IS NULL FOR UPDATE" +
		"), inserted_repo AS (" +
		"INSERT INTO repos (" + strings.Join(repoColumns, ", ") + ") " +
		"SELECT " + placeholders(19, len(repoColumns)) + " FROM eligible_operation, active_namespace, active_binding, active_volume, held_fence " +
		"WHERE NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $19) " +
		"RETURNING " + strings.Join(repoColumns, ", ") +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, inserted_repo " +
		"WHERE operations.operation_id = eligible_operation.operation_id " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), released_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM updated_operation, held_fence " +
		"WHERE repo_fences.repo_id = $19 AND repo_fences.fence_id = held_fence.fence_id " +
		"RETURNING repo_fences.fence_id" +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(34, len(auditOutboxColumns)) + " FROM updated_operation, inserted_repo, released_fence " +
		"RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("inserted_repo", repoColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) +
		" FROM inserted_repo, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func repoCreateFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations " +
		"WHERE operation_id = $12 " +
		"AND operation_state = 'running' " +
		"AND lease_owner = $13 " +
		"AND lease_expires_at IS NOT NULL " +
		"AND lease_expires_at > $11 " +
		"AND operation_type = 'repo_create' " +
		"AND phase = 'validate_repo_create' " +
		"AND namespace_id = $14 " +
		"AND repo_id = $15 " +
		"AND resource_type = 'repo' " +
		"AND resource_id = $15 " +
		"AND caller_service = $16 " +
		"AND correlation_id = $17 " +
		"AND authorized_actor_type = $18 " +
		"AND authorized_actor_id = $19 " +
		"FOR UPDATE" +
		"), released_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM eligible_operation " +
		"WHERE $20 <> '' AND repo_fences.repo_id = $15 AND repo_fences.fence_id = $20 AND repo_fences.fence_kind = 'lifecycle' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL " +
		"RETURNING repo_fences.fence_id" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation " +
		"WHERE operations.operation_id = eligible_operation.operation_id " +
		"AND ($20 = '' OR EXISTS (SELECT 1 FROM released_fence)) " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(21, len(auditOutboxColumns)) + " FROM updated_operation " +
		"RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func operationLeaseFencedUpdateSetSQL() string {
	return "UPDATE operations SET " +
		"operation_state = $1, " +
		"phase = $2, " +
		"lease_owner = CASE WHEN $1 = 'running' THEN lease_owner ELSE NULL END, " +
		"lease_expires_at = CASE WHEN $1 = 'running' THEN lease_expires_at ELSE NULL END, " +
		"external_resource_ids = $3, " +
		"input_summary = $4, " +
		"jvs_json_output = $5, " +
		"verification_result = $6, " +
		"compensation_status = $7, " +
		"error_json = $8, " +
		"started_at = COALESCE(started_at, $9, $11), " +
		"finished_at = CASE WHEN $1 IN ('succeeded', 'failed', 'cancelled') THEN COALESCE($10, $11) ELSE NULL END, " +
		"updated_at = $11 "
}

func scanRepoAndOperation(row rowScanner) (resources.Repo, operations.OperationRecord, error) {
	var repo resources.Repo
	var kind, status, lifecycleStatus string
	var retentionExpiresAt sql.NullTime
	var lastLifecycleOperationID, preDeleteStatus sql.NullString
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	dest := []any{
		&repo.ID, &repo.NamespaceID, &repo.VolumeID, &repo.JVSRepoID, &kind, &status, &repo.ControlVolumeSubdir, &repo.PayloadVolumeSubdir,
		&lifecycleStatus, &retentionExpiresAt, &lastLifecycleOperationID, &preDeleteStatus, &repo.CreatedAt, &repo.UpdatedAt,
		&record.ID, &operationType, &operationState, &record.Phase, &record.Attempt, &leaseOwner, &leaseExpiresAt,
		&record.IdempotencyScope, &record.IdempotencyKey, &requestHash, &record.CorrelationID, &record.CallerService,
		&record.AuthorizedActor.Type, &record.AuthorizedActor.ID, &record.Resource.Type, &record.Resource.ID, &record.NamespaceID,
		&repoID, &templateID, &exportID, &mountBindingID, &sessionFenceID, &externalResourceIDsJSON, &inputSummaryJSON,
		&jvsJSONOutputJSON, &verificationResultJSON, &compensationStatus, &errorJSON, &record.CreatedAt, &startedAt, &finishedAt,
	}
	if err := row.Scan(dest...); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	repo.Kind = resources.RepoKind(kind)
	repo.Status = resources.RepoStatus(status)
	repo.Lifecycle = resources.RepoLifecycle{Status: resources.RepoStatus(lifecycleStatus), RetentionExpiresAt: nullTimePtr(retentionExpiresAt), LastLifecycleOperationID: nullStringValue(lastLifecycleOperationID), PreDeleteStatus: resources.RepoStatus(nullStringValue(preDeleteStatus))}
	if err := repo.Validate(); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	record.Type = operations.OperationType(operationType)
	record.State = operations.OperationState(operationState)
	record.RequestHash = operations.RequestHash(requestHash)
	record.LeaseOwner = nullStringValue(leaseOwner)
	record.LeaseExpiresAt = nullTimePtr(leaseExpiresAt)
	record.RepoID = nullStringValue(repoID)
	record.TemplateID = nullStringValue(templateID)
	record.ExportID = nullStringValue(exportID)
	record.MountBindingID = nullStringValue(mountBindingID)
	record.SessionFenceID = nullStringValue(sessionFenceID)
	record.CompensationStatus = nullStringValue(compensationStatus)
	record.StartedAt = nullTimePtr(startedAt)
	record.FinishedAt = nullTimePtr(finishedAt)
	if err := unmarshalObject(externalResourceIDsJSON, &record.ExternalResourceIDs); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return resources.Repo{}, operations.OperationRecord{}, err
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return resources.Repo{}, operations.OperationRecord{}, err
		}
		record.Error = &opErr
	}
	return repo, record.Sanitized(), nil
}
