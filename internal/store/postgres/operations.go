package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

var operationColumns = []string{
	"operation_id",
	"operation_type",
	"operation_state",
	"phase",
	"attempt",
	"lease_owner",
	"lease_expires_at",
	"idempotency_scope",
	"idempotency_key",
	"request_hash",
	"correlation_id",
	"caller_service",
	"authorized_actor_type",
	"authorized_actor_id",
	"resource_type",
	"resource_id",
	"namespace_id",
	"repo_id",
	"template_id",
	"export_id",
	"mount_binding_id",
	"session_fence_id",
	"external_resource_ids",
	"input_summary",
	"jvs_json_output",
	"verification_result",
	"compensation_status",
	"error_json",
	"created_at",
	"started_at",
	"finished_at",
	"updated_at",
}

var operationSelectColumns = operationColumns[:len(operationColumns)-1]

func (store *Store) CreateOperation(ctx context.Context, sanitized operations.SanitizedOperationRecord) error {
	record := sanitized.Record()
	args, err := operationInsertArgs(record)
	if err != nil {
		return err
	}
	_, err = store.exec.ExecContext(ctx, operationInsertSQL(), args...)
	return err
}

func (store *Store) UpdateOperation(ctx context.Context, sanitized operations.SanitizedOperationRecord) error {
	record := sanitized.Record()
	args, err := operationUpdateArgs(record, store.now())
	if err != nil {
		return err
	}
	result, err := store.exec.ExecContext(ctx, operationUpdateSQL(), args...)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: update operation %q", sql.ErrNoRows, record.ID)
	}
	return nil
}

func (store *Store) GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error) {
	row := store.exec.QueryRowContext(ctx, operationSelectSQL()+" WHERE operation_id = $1", operationID)
	return scanOperation(row)
}

func (store *Store) CreateOrReuseOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()

	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}

	var inserted bool
	row := store.exec.QueryRowContext(ctx, operationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInserted(row, &inserted)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	return operations.IdempotencyResolution{
		Operation: got.Sanitized(),
		Existing:  !inserted,
		Reused:    !inserted,
	}, nil
}

func operationInsertSQL() string {
	return "INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") VALUES (" + placeholders(1, len(operationColumns)) + ")"
}

func operationCreateOrReuseSQL() string {
	return "INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") VALUES (" + placeholders(1, len(operationColumns)) + ") " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") + ", (xmax = 0) AS inserted"
}

func operationSelectSQL() string {
	return "SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM operations"
}

func operationUpdateSQL() string {
	return "UPDATE operations SET " +
		"operation_state = $1, " +
		"phase = $2, " +
		"attempt = $3, " +
		"lease_owner = $4, " +
		"lease_expires_at = $5, " +
		"external_resource_ids = $6, " +
		"input_summary = $7, " +
		"jvs_json_output = $8, " +
		"verification_result = $9, " +
		"compensation_status = $10, " +
		"error_json = $11, " +
		"started_at = $12, " +
		"finished_at = $13, " +
		"updated_at = $14 " +
		"WHERE operation_id = $15"
}

func operationInsertArgs(record operations.OperationRecord) ([]any, error) {
	externalResourceIDs, err := marshalObject(record.ExternalResourceIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal external_resource_ids: %w", err)
	}
	inputSummary, err := marshalObject(record.InputSummary)
	if err != nil {
		return nil, fmt.Errorf("marshal input_summary: %w", err)
	}
	jvsJSONOutput, err := marshalNullableJSON(record.JVSJSONOutput)
	if err != nil {
		return nil, fmt.Errorf("marshal jvs_json_output: %w", err)
	}
	verificationResult, err := marshalNullableJSON(record.VerificationResult)
	if err != nil {
		return nil, fmt.Errorf("marshal verification_result: %w", err)
	}
	errorJSON, err := marshalNullableJSON(record.Error)
	if err != nil {
		return nil, fmt.Errorf("marshal error_json: %w", err)
	}

	return []any{
		record.ID,
		string(record.Type),
		string(record.State),
		record.Phase,
		record.Attempt,
		leaseOwnerArg(record),
		leaseExpiresAtArg(record),
		record.IdempotencyScope,
		record.IdempotencyKey,
		string(record.RequestHash),
		record.CorrelationID,
		record.CallerService,
		record.AuthorizedActor.Type,
		record.AuthorizedActor.ID,
		record.Resource.Type,
		record.Resource.ID,
		record.NamespaceID,
		nullableStringArg(record.RepoID),
		nullableStringArg(record.TemplateID),
		nullableStringArg(record.ExportID),
		nullableStringArg(record.MountBindingID),
		nullableStringArg(record.SessionFenceID),
		externalResourceIDs,
		inputSummary,
		jvsJSONOutput,
		verificationResult,
		nullableStringArg(record.CompensationStatus),
		errorJSON,
		record.CreatedAt,
		timePtrArg(record.StartedAt),
		timePtrArg(record.FinishedAt),
		record.CreatedAt,
	}, nil
}

func operationUpdateArgs(record operations.OperationRecord, updatedAt any) ([]any, error) {
	externalResourceIDs, err := marshalObject(record.ExternalResourceIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal external_resource_ids: %w", err)
	}
	inputSummary, err := marshalObject(record.InputSummary)
	if err != nil {
		return nil, fmt.Errorf("marshal input_summary: %w", err)
	}
	jvsJSONOutput, err := marshalNullableJSON(record.JVSJSONOutput)
	if err != nil {
		return nil, fmt.Errorf("marshal jvs_json_output: %w", err)
	}
	verificationResult, err := marshalNullableJSON(record.VerificationResult)
	if err != nil {
		return nil, fmt.Errorf("marshal verification_result: %w", err)
	}
	errorJSON, err := marshalNullableJSON(record.Error)
	if err != nil {
		return nil, fmt.Errorf("marshal error_json: %w", err)
	}

	return []any{
		string(record.State),
		record.Phase,
		record.Attempt,
		leaseOwnerArg(record),
		leaseExpiresAtArg(record),
		externalResourceIDs,
		inputSummary,
		jvsJSONOutput,
		verificationResult,
		nullableStringArg(record.CompensationStatus),
		errorJSON,
		timePtrArg(record.StartedAt),
		timePtrArg(record.FinishedAt),
		updatedAt,
		record.ID,
	}, nil
}

func scanOperation(row rowScanner) (operations.OperationRecord, error) {
	return scanOperationWithInserted(row, nil)
}

func scanOperationWithInserted(row rowScanner, inserted *bool) (operations.OperationRecord, error) {
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	var insertedValue bool

	dest := []any{
		&record.ID,
		&operationType,
		&operationState,
		&record.Phase,
		&record.Attempt,
		&leaseOwner,
		&leaseExpiresAt,
		&record.IdempotencyScope,
		&record.IdempotencyKey,
		&requestHash,
		&record.CorrelationID,
		&record.CallerService,
		&record.AuthorizedActor.Type,
		&record.AuthorizedActor.ID,
		&record.Resource.Type,
		&record.Resource.ID,
		&record.NamespaceID,
		&repoID,
		&templateID,
		&exportID,
		&mountBindingID,
		&sessionFenceID,
		&externalResourceIDsJSON,
		&inputSummaryJSON,
		&jvsJSONOutputJSON,
		&verificationResultJSON,
		&compensationStatus,
		&errorJSON,
		&record.CreatedAt,
		&startedAt,
		&finishedAt,
	}
	if inserted != nil {
		dest = append(dest, &insertedValue)
	}
	if err := row.Scan(dest...); err != nil {
		return operations.OperationRecord{}, err
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
		return operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return operations.OperationRecord{}, fmt.Errorf("unmarshal error_json: %w", err)
		}
		record.Error = &opErr
	}
	if inserted != nil {
		*inserted = insertedValue
	}

	return record.Sanitized(), nil
}

func placeholders(start, count int) string {
	parts := make([]string, count)
	for idx := range parts {
		parts[idx] = fmt.Sprintf("$%d", start+idx)
	}
	return strings.Join(parts, ", ")
}

func marshalObject(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(encoded) == "null" {
		return []byte("{}"), nil
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("value must marshal to a JSON object")
	}
	return encoded, nil
}

func marshalNullableJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func unmarshalObject(data []byte, dest any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	return json.Unmarshal(data, dest)
}

func unmarshalNullableJSON(data []byte, dest *any) error {
	if len(data) == 0 {
		*dest = nil
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*dest = value
	return nil
}

func nullableStringArg(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func leaseOwnerArg(record operations.OperationRecord) any {
	if record.LeaseOwner == "" && record.LeaseExpiresAt == nil {
		return nil
	}
	return record.LeaseOwner
}

func leaseExpiresAtArg(record operations.OperationRecord) any {
	if record.LeaseOwner == "" && record.LeaseExpiresAt == nil {
		return nil
	}
	if record.LeaseExpiresAt == nil {
		return nil
	}
	return *record.LeaseExpiresAt
}

func timePtrArg(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
