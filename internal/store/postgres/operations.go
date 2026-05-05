package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
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

func (store *Store) ListOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list operations for recovery: limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, operationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) AcquireOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	row := store.exec.QueryRowContext(ctx, operationAcquireLeaseSQL(),
		args.operationID,
		args.owner,
		args.expiresAt,
		args.now,
		args.recoveryMode,
		args.cancelPolicy,
	)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) RenewOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	row := store.exec.QueryRowContext(ctx, operationRenewLeaseSQL(),
		args.operationID,
		args.owner,
		args.expiresAt,
		args.now,
	)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("renew", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) UpdateOperationWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	record := sanitized.Record()
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	row := store.exec.QueryRowContext(ctx, operationLeaseFencedUpdateSQL(), args...)
	updated, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("update", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return updated, nil
}

func (store *Store) CommitOperationWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	operationID := strings.TrimSpace(record.ID)
	if strings.TrimSpace(event.OperationID) == "" {
		return operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "missing audit operation id")
	}
	if event.OperationID != operationID {
		return operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}

	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}

	args := append(operationArgs, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, operationCommitWithLeaseSQL(), args...)
	updated, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("commit", operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return updated, nil
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

func operationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required') " +
		"ORDER BY created_at, operation_id LIMIT $2"
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

func operationAcquireLeaseSQL() string {
	validLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL))"
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN NULL ELSE $3 END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 AND (" +
		"(operation_state = 'queued' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'operator_intervention_required' AND $5 = 'explicit_recovery_action' AND " + validLeasePair + ") OR " +
		"(operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + strings.Join(operationSelectColumns, ", ")
}

func operationRenewLeaseSQL() string {
	return "UPDATE operations SET " +
		"lease_expires_at = GREATEST(lease_expires_at, $3), " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 AND operation_state = 'running' AND lease_owner = $2 AND lease_expires_at IS NOT NULL AND lease_expires_at > $4 " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ")
}

func operationLeaseFencedUpdateSQL() string {
	return operationLeaseFencedUpdateBaseSQL() + "RETURNING " + strings.Join(operationSelectColumns, ", ")
}

func operationLeaseFencedUpdateBaseSQL() string {
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
		"updated_at = $11 " +
		"WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 "
}

func operationCommitWithLeaseSQL() string {
	return "WITH updated_operation AS (" +
		operationLeaseFencedUpdateBaseSQL() +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(14, len(auditOutboxColumns)) + " FROM updated_operation " +
		"RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
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

type operationLeaseArgs struct {
	operationID  string
	owner        string
	expiresAt    time.Time
	now          time.Time
	recoveryMode string
	cancelPolicy string
}

func operationLeaseRequestArgs(operationID string, request operations.LeaseRequest) (operationLeaseArgs, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("operation_id", "missing operation id")
	}
	owner := strings.TrimSpace(request.Owner)
	if owner == "" {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("owner", "missing lease owner")
	}
	if request.Duration <= 0 {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("duration", "lease duration must be positive")
	}
	if request.Now.IsZero() {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("now", "lease decision time must be set")
	}
	if !validOperationLeaseRecoveryMode(request.RecoveryMode) {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("recovery_mode", fmt.Sprintf("unknown recovery mode %q", request.RecoveryMode))
	}
	if !validOperationLeaseCancelPolicy(request.CancelPolicy) {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("cancel_policy", fmt.Sprintf("unknown cancel policy %q", request.CancelPolicy))
	}

	now := request.Now.UTC()
	return operationLeaseArgs{
		operationID:  operationID,
		owner:        owner,
		expiresAt:    now.Add(request.Duration),
		now:          now,
		recoveryMode: string(request.RecoveryMode),
		cancelPolicy: string(request.CancelPolicy),
	}, nil
}

func validOperationLeaseRecoveryMode(mode operations.LeaseRecoveryMode) bool {
	switch mode {
	case operations.LeaseRecoveryNone, operations.LeaseRecoveryExplicitAction:
		return true
	default:
		return false
	}
}

func validOperationLeaseCancelPolicy(policy operations.LeaseCancelPolicy) bool {
	switch policy {
	case operations.LeaseCancelPolicyNone, operations.LeaseCancelPolicyFinalize:
		return true
	default:
		return false
	}
}

func operationLeaseFencedUpdateArgs(record operations.OperationRecord, owner string, now time.Time) ([]any, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, operationLeaseInvalidRequest("owner", "missing lease owner")
	}
	operationID := strings.TrimSpace(record.ID)
	if operationID == "" {
		return nil, operationLeaseInvalidRequest("operation_id", "missing operation id")
	}
	if now.IsZero() {
		return nil, operationLeaseInvalidRequest("now", "lease fence time must be set")
	}
	if !validOperationLeaseFencedUpdateState(record.State) {
		return nil, operationLeaseInvalidRequest("operation_state", fmt.Sprintf("%q cannot be written through a lease-fenced worker update", record.State))
	}
	if recordOwner := strings.TrimSpace(record.LeaseOwner); recordOwner != "" && recordOwner != owner {
		return nil, operationLeaseInvalidRequest("lease_owner", "record lease owner differs from fence owner")
	}

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
		operationID,
		owner,
	}, nil
}

func auditOutboxInsertArgs(record audit.OutboxRecord) []any {
	return []any{
		record.EventID,
		string(record.EventType),
		record.EventTime,
		[]byte(record.PayloadJSON),
		string(record.Status),
		record.DeliveryAttempt,
		timePtrArg(record.NextRetryAt),
		record.LastError,
		record.CreatedAt,
		record.UpdatedAt,
		timePtrArg(record.DeliveredAt),
	}
}

func validOperationLeaseFencedUpdateState(state operations.OperationState) bool {
	switch state {
	case operations.OperationStateRunning,
		operations.OperationStateSucceeded,
		operations.OperationStateFailed,
		operations.OperationStateCancelled,
		operations.OperationStateOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

func operationLeaseInvalidRequest(field, reason string) error {
	return &operations.OperationLeaseError{
		Code:   operations.OperationLeaseErrorInvalidRequest,
		Field:  field,
		Reason: reason,
		Cause:  operations.ErrInvalidLeaseRequest,
	}
}

func operationLeaseUnavailable(action, operationID string, err error) error {
	return fmt.Errorf("%w: %s operation lease %q: %w", operations.ErrLeaseUnavailable, action, operationID, err)
}

func scanOperation(row rowScanner) (operations.OperationRecord, error) {
	return scanOperationWithInserted(row, nil)
}

func scanOperations(rows rowsScanner) (records []operations.OperationRecord, err error) {
	defer func() {
		closeErr := rows.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	for rows.Next() {
		record, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
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
