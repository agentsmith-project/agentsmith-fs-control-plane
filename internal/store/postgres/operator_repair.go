package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operatorrepair"
)

func (store *Store) ReadOperationForRepair(ctx context.Context, operationID string) (operations.OperationRecord, error) {
	return store.GetOperation(ctx, operationID)
}

func (store *Store) CommitOperatorRepairFailed(ctx context.Context, request operatorrepair.CommitRequest) (operations.OperationRecord, error) {
	if err := operatorrepair.ValidateEligibleOperation(request.Before); err != nil {
		return operations.OperationRecord{}, err
	}
	if request.OperationID != request.Before.ID || request.OperationID != request.After.ID {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("operation_id", "repair operation id must match before and after records")
	}
	if request.After.State != operations.OperationStateFailed {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("operation_state", "operator repair after state must be failed")
	}
	if err := operations.ValidateTransition(request.Before.State, request.After.State); err != nil {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("operation_state", err.Error())
	}
	if strings.TrimSpace(request.Event.OperationID) == "" || request.Event.OperationID != request.OperationID {
		return operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "audit operation id must match operation repair")
	}
	if request.Now.IsZero() {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("now", "operator repair commit time must be set")
	}

	updateArgs, err := operatorRepairUpdateArgs(request.After.SanitizedForPersistence().Record(), request.Now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(request.Event, request.Now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	args := append(updateArgs, auditOutboxInsertArgs(outboxRecord)...)
	args = append(args, request.Before.Phase)
	row := store.exec.QueryRowContext(ctx, operatorRepairCommitSQL(), args...)
	updated, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("operator_repair", request.OperationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return updated, nil
}

func operatorRepairUpdateArgs(record operations.OperationRecord, now time.Time) ([]any, error) {
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
		externalResourceIDs,
		inputSummary,
		jvsJSONOutput,
		verificationResult,
		nullableStringArg(record.CompensationStatus),
		errorJSON,
		timePtrArg(record.StartedAt),
		timePtrArg(record.FinishedAt),
		now.UTC(),
		record.ID,
	}, nil
}

func operatorRepairCommitSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 " +
		"AND operation_state = 'operator_intervention_required' " +
		"AND error_json->>'code' = 'OPERATION_RECOVERY_REQUIRED' " +
		"AND (error_json->'details'->>'reason' IN ('unsupported_operation_recovery','worker_recovery_disabled') OR error_json->'details'->>'reason' ILIKE '%disabled%' OR error_json->'details'->>'reason' ILIKE '%unsupported%') " +
		"AND phase = $24 AND LOWER(phase) NOT LIKE '%writer_fenced%' AND LOWER(phase) NOT LIKE '%consuming%' AND LOWER(phase) NOT LIKE '%discarding%' AND LOWER(phase) NOT LIKE '%committed%' " +
		"AND lease_owner IS NULL AND lease_expires_at IS NULL AND session_fence_id IS NULL " +
		"FOR UPDATE" +
		"), updated_operation AS (" +
		"UPDATE operations SET operation_state = $1, phase = $2, lease_owner = NULL, lease_expires_at = NULL, external_resource_ids = $3, input_summary = $4, jvs_json_output = $5, verification_result = $6, compensation_status = $7, error_json = $8, started_at = COALESCE(started_at, $9, $11), finished_at = COALESCE($10, $11), updated_at = $11 " +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id AND $1 = 'failed' " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(13, len(auditOutboxColumns)) + " FROM updated_operation " +
		"RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}
