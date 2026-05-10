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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func (store *Store) UpdateRestorePreviewPreflightWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestorePreviewPreflightRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	args = append(args, restorePreviewStoredPredicateArgs(record)...)
	row := store.exec.QueryRowContext(ctx, restorePreviewPreflightUpdateWithLeaseSQL(), args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("restore preview preflight update", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func (store *Store) CommitRestorePreviewSucceededWithLease(ctx context.Context, plan restoreplan.Plan, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestorePreviewSuccessRecord(record, plan); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	if err := validateRestorePreviewAuditEvent(record, event, audit.OutcomeSucceeded); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, restorePreviewStoredPredicateArgs(record)...)
	args = append(args, restorePlanInsertArgs(plan)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restorePreviewSuccessCommitWithLeaseSQL(), args...)
	gotPlan, gotOperation, err := scanRestorePlanAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restoreplan.Plan{}, operations.OperationRecord{}, operationLeaseUnavailable("restore preview success commit", record.ID, err)
		}
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	return gotPlan, gotOperation, nil
}

func (store *Store) CommitRestorePreviewFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestorePreviewFailureRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateRestorePreviewAuditEvent(record, event, audit.OutcomeFailed); err != nil {
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
	args := append(operationArgs, restorePreviewStoredPredicateArgs(record)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restorePreviewFailureCommitWithLeaseSQL(), args...)
	got, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("restore preview failure commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return got, nil
}

func validateRestorePreviewPreflightRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestorePreview {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview")
	}
	if record.State != operations.OperationStateRunning {
		return operationLeaseInvalidRequest("operation_state", "restore preview preflight requires running operation update")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle {
		return operationLeaseInvalidRequest("phase", "restore preview preflight requires preflight idle phase")
	}
	if !restorePreviewPreflightMarkerCaptured(record) {
		return operationLeaseInvalidRequest("verification_result", "restore preview preflight requires safe idle marker")
	}
	if restorePreviewContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore preview must not persist raw commands")
	}
	return validateRestorePreviewRecordResource(record, false)
}

func validateRestorePreviewSuccessRecord(record operations.OperationRecord, plan restoreplan.Plan) error {
	if record.Type != operations.OperationRestorePreview {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "restore preview success requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewCommitted {
		return operationLeaseInvalidRequest("phase", "restore preview success requires committed terminal phase")
	}
	if !restorePreviewPreflightMarkerCaptured(record) {
		return operationLeaseInvalidRequest("verification_result", "restore preview success requires captured preflight marker")
	}
	if restorePreviewContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore preview must not persist raw commands")
	}
	if err := validateRestorePreviewRecordResource(record, false); err != nil {
		return err
	}
	if plan.Status != restoreplan.StatusPending {
		return operationLeaseInvalidRequest("restore_plan", "restore preview success requires pending restore plan")
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	planID, sourceSavePointID := restorePreviewSummaryIDs(record)
	if plan.ID != planID || plan.SourceSavePointID != sourceSavePointID || plan.NamespaceID != record.NamespaceID || plan.RepoID != record.RepoID || plan.PreviewOperationID != record.ID {
		return operationLeaseInvalidRequest("restore_plan", "restore preview plan must match operation result")
	}
	return nil
}

func validateRestorePreviewFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestorePreview {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "restore preview failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewValidate && record.Phase != operations.OperationPhaseRestorePreviewPreflightIdle {
		return operationLeaseInvalidRequest("phase", "restore preview failure must stay in validate or preflight phase")
	}
	if restorePreviewContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore preview must not persist raw commands")
	}
	return validateRestorePreviewRecordResource(record, true)
}

func validateRestorePreviewRecordResource(record operations.OperationRecord, requireError bool) error {
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "restore preview requires target repo resource")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return operationLeaseInvalidRequest("caller", "restore preview requires caller context")
	}
	if requireError && record.Error == nil {
		return operationLeaseInvalidRequest("error", "restore preview failure requires operation error")
	}
	return nil
}

func validateRestorePreviewAuditEvent(record operations.OperationRecord, event audit.Event, outcome audit.Outcome) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeRestorePreview || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event_type", "restore preview audit event must match operation outcome")
	}
	if event.Resource.Type != "repo" || event.Resource.ID != record.RepoID || event.Resource.NamespaceID != record.NamespaceID {
		return auditOutboxInvalidRequest("resource", "restore preview audit resource must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "restore preview audit caller context must match operation")
	}
	return nil
}

func restorePreviewStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.NamespaceID, record.RepoID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID}
}

func restorePreviewPreflightUpdateWithLeaseSQL() string {
	return operationLeaseFencedUpdateBaseSQL() +
		"AND operation_type = 'restore_preview' AND phase = 'validate_restore_preview' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"RETURNING " + operationReturningColumnsSQL()
}

func restorePreviewSuccessCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_preview' AND phase = 'restore_preview_preflight_idle' AND verification_result->>'preflight_recovery_status_captured' = 'true' AND verification_result->>'preflight_restore_state' = 'idle' AND verification_result->>'preflight_blocking' = 'false' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_restore_plan AS (" +
		"INSERT INTO restore_plans (" + strings.Join(restorePlanColumns, ", ") + ") SELECT " + placeholders(20, len(restorePlanColumns)) + " FROM updated_operation RETURNING " + strings.Join(restorePlanColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(20+len(restorePlanColumns), len(auditOutboxColumns)) + " FROM updated_operation, inserted_restore_plan RETURNING audit_event_id" +
		") SELECT " + strings.Join(restorePlanColumns, ", ") + ", " + strings.Join(operationSelectColumns, ", ") + " FROM inserted_restore_plan, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restorePreviewFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_preview' AND phase IN ('validate_restore_preview','restore_preview_preflight_idle') " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(20, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restorePreviewPreflightMarkerCaptured(record operations.OperationRecord) bool {
	verification, ok := record.VerificationResult.(map[string]any)
	if !ok {
		return false
	}
	captured, _ := verification["preflight_recovery_status_captured"].(bool)
	state, _ := verification["preflight_restore_state"].(string)
	blocking, _ := verification["preflight_blocking"].(bool)
	return captured && state == "idle" && !blocking
}

func restorePreviewSummaryIDs(record operations.OperationRecord) (string, string) {
	for _, value := range []any{record.JVSJSONOutput, record.VerificationResult} {
		mapped, ok := value.(map[string]any)
		if !ok {
			continue
		}
		planID, _ := mapped["restore_plan_id"].(string)
		sourceSavePointID, _ := mapped["source_save_point_id"].(string)
		if strings.TrimSpace(planID) != "" && strings.TrimSpace(sourceSavePointID) != "" {
			return strings.TrimSpace(planID), strings.TrimSpace(sourceSavePointID)
		}
	}
	return "", ""
}

func restorePreviewContainsForbiddenCommand(record operations.OperationRecord) bool {
	return containsForbiddenRestorePreviewCommand(record.InputSummary) ||
		containsForbiddenRestorePreviewCommand(record.JVSJSONOutput) ||
		containsForbiddenRestorePreviewCommand(record.VerificationResult)
}

func containsForbiddenRestorePreviewCommand(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, value := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "run_command" || normalized == "recommended_next_command" {
				return true
			}
			if containsForbiddenRestorePreviewCommand(value) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsForbiddenRestorePreviewCommand(item) {
				return true
			}
		}
	}
	return false
}

func scanRestorePlanAndOperation(row rowScanner) (restoreplan.Plan, operations.OperationRecord, error) {
	var plan restoreplan.Plan
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte

	if err := scanRestorePlanPrefix(
		row,
		&plan,
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
	); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}

	if err := plan.Validate(); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
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
		return restoreplan.Plan{}, operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return restoreplan.Plan{}, operations.OperationRecord{}, fmt.Errorf("unmarshal error_json: %w", err)
		}
		record.Error = &opErr
	}

	return plan, record.Sanitized(), nil
}
