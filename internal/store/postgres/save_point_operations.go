package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func (store *Store) CommitSavePointCreateSucceededWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateSavePointCreateSuccessRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateSavePointCreateAuditEvent(record, event, audit.OutcomeSucceeded); err != nil {
		return operations.OperationRecord{}, err
	}
	return store.commitSavePointCreateTerminalWithLease(ctx, record, owner, now, event, savePointCreateSuccessCommitWithLeaseSQL())
}

func (store *Store) CommitSavePointCreateFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateSavePointCreateFailureRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateSavePointCreateAuditEvent(record, event, audit.OutcomeFailed); err != nil {
		return operations.OperationRecord{}, err
	}
	return store.commitSavePointCreateTerminalWithLease(ctx, record, owner, now, event, savePointCreateFailureCommitWithLeaseSQL())
}

func (store *Store) MarkSavePointCreateWriterDrainPendingWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateSavePointCreateWriterDrainPendingRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	args := append(operationArgs, savePointCreateStoredPredicateArgs(record)...)
	row := store.exec.QueryRowContext(ctx, savePointCreateWriterDrainPendingWithLeaseSQL(), args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("save point create writer drain pending update", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func (store *Store) commitSavePointCreateTerminalWithLease(ctx context.Context, record operations.OperationRecord, owner string, now time.Time, event audit.Event, query string) (operations.OperationRecord, error) {
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	args := append(operationArgs, savePointCreateStoredPredicateArgs(record)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, query, args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("save point create terminal commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func validateSavePointCreateSuccessRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationSavePointCreate {
		return operationLeaseInvalidRequest("operation_type", "operation record must be save_point_create")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "save point create success requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseSavePointCreateCommitted {
		return operationLeaseInvalidRequest("phase", "save point create success requires committed terminal phase")
	}
	savePointID := strings.TrimSpace(record.ExternalResourceIDs["save_point_id"])
	if err := operations.ValidateSavePointID(savePointID); err != nil {
		return operationLeaseInvalidRequest("external_resource_ids", "save point create success requires save point id resource")
	}
	if outputID, ok := savePointCreateEvidenceID(record.JVSJSONOutput); !ok || outputID != savePointID {
		return operationLeaseInvalidRequest("jvs_json_output", "save point create success requires matching save point id evidence")
	}
	verificationID, ok := savePointCreateEvidenceID(record.VerificationResult)
	if !ok {
		return operationLeaseInvalidRequest("verification_result", "save point create success requires matching save point id verification")
	}
	if verificationID != savePointID {
		return operationLeaseInvalidRequest("verification_result", "save point create success requires matching save point id verification")
	}
	return validateSavePointCreateRecordResource(record, false)
}

func validateSavePointCreateFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationSavePointCreate {
		return operationLeaseInvalidRequest("operation_type", "operation record must be save_point_create")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "save point create failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseSavePointCreateValidate && record.Phase != operations.OperationPhaseSavePointCreatePrepared {
		return operationLeaseInvalidRequest("phase", "save point create failure must stay in validate or prepared phase")
	}
	return validateSavePointCreateRecordResource(record, true)
}

func validateSavePointCreateWriterDrainPendingRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationSavePointCreate {
		return operationLeaseInvalidRequest("operation_type", "operation record must be save_point_create")
	}
	if record.State != operations.OperationStateRunning {
		return operationLeaseInvalidRequest("operation_state", "save point writer drain pending update requires running operation")
	}
	if record.Phase != operations.OperationPhaseSavePointCreateValidate {
		return operationLeaseInvalidRequest("phase", "save point writer drain pending update must stay in validate phase")
	}
	if record.FinishedAt != nil {
		return operationLeaseInvalidRequest("finished_at", "save point writer drain pending update must not finish operation")
	}
	if record.Error == nil || record.Error.Code != "SAVE_POINT_WRITER_DRAIN_PENDING" || !record.Error.Retryable {
		return operationLeaseInvalidRequest("error", "save point writer drain pending update requires retryable writer drain error")
	}
	return validateSavePointCreateRecordResource(record, true)
}

func validateSavePointCreateRecordResource(record operations.OperationRecord, requireError bool) error {
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "save point create requires target repo resource")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return operationLeaseInvalidRequest("caller", "save point create requires caller context")
	}
	if requireError && record.Error == nil {
		return operationLeaseInvalidRequest("error", "save point create failure requires operation error")
	}
	return nil
}

func validateSavePointCreateAuditEvent(record operations.OperationRecord, event audit.Event, outcome audit.Outcome) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeSavePointCreate || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event_type", "save point create audit event must match operation outcome")
	}
	if event.Resource.Type != "repo" || event.Resource.ID != record.RepoID || event.Resource.NamespaceID != record.NamespaceID {
		return auditOutboxInvalidRequest("resource", "save point create audit resource must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "save point create audit caller context must match operation")
	}
	return nil
}

func savePointCreateEvidenceID(value any) (string, bool) {
	var raw string
	switch typed := value.(type) {
	case map[string]any:
		raw, _ = typed["save_point_id"].(string)
	case map[string]string:
		raw = typed["save_point_id"]
	default:
		return "", false
	}
	savePointID := strings.TrimSpace(raw)
	if err := operations.ValidateSavePointID(savePointID); err != nil {
		return "", false
	}
	return savePointID, true
}

func savePointCreateStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.NamespaceID, record.RepoID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID}
}

func savePointCreateSuccessCommitWithLeaseSQL() string {
	return savePointCreateTerminalCommitWithLeaseSQL("phase = 'validate_save_point_create'")
}

func savePointCreateFailureCommitWithLeaseSQL() string {
	return savePointCreateTerminalCommitWithLeaseSQL("phase IN ('validate_save_point_create','save_point_create_prepared')")
}

func savePointCreateWriterDrainPendingWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'save_point_create' AND phase = 'validate_save_point_create' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), updated_operation AS (" +
		"UPDATE operations SET " +
		"operation_state = $1, " +
		"phase = $2, " +
		"lease_owner = operations.lease_owner, " +
		"lease_expires_at = $11, " +
		"external_resource_ids = $3, " +
		"input_summary = $4, " +
		"jvs_json_output = $5, " +
		"verification_result = $6, " +
		"compensation_status = $7, " +
		"error_json = $8, " +
		"started_at = COALESCE(operations.started_at, $9, $11), " +
		"finished_at = NULL, " +
		"updated_at = $11 " +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation"
}

func savePointCreateTerminalCommitWithLeaseSQL(storedPhasePredicate string) string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'save_point_create' AND " + storedPhasePredicate + " " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(20, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}
