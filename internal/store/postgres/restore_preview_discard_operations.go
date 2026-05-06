package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func (store *Store) MarkRestorePreviewDiscardingWithLease(ctx context.Context, plan restoreplan.Plan, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestorePreviewDiscardMarkRecord(record, plan); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	args = append(args, restorePreviewStoredPredicateArgs(record)...)
	args = append(args, plan.ID, plan.PreviewOperationID, plan.SourceSavePointID)
	row := store.exec.QueryRowContext(ctx, restorePreviewDiscardMarkWithLeaseSQL(), args...)
	gotPlan, gotOperation, err := scanRestorePlanAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restoreplan.Plan{}, operations.OperationRecord{}, operationLeaseUnavailable("restore preview discard mark discarding", record.ID, err)
		}
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	return gotPlan, gotOperation, nil
}

func (store *Store) CommitRestorePreviewDiscardSucceededWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestorePreviewDiscardSuccessRecord(record); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	if err := validateRestorePreviewDiscardAuditEvent(record, event, audit.OutcomeSucceeded); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	restorePlanID := restorePreviewDiscardRestorePlanID(record)
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, restorePreviewStoredPredicateArgs(record)...)
	args = append(args, restorePlanID)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restorePreviewDiscardSuccessCommitWithLeaseSQL(), args...)
	gotPlan, gotOperation, err := scanRestorePlanAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restoreplan.Plan{}, operations.OperationRecord{}, operationLeaseUnavailable("restore preview discard success commit", record.ID, err)
		}
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	return gotPlan, gotOperation, nil
}

func (store *Store) CommitRestorePreviewDiscardFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestorePreviewDiscardFailureRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateRestorePreviewDiscardAuditEvent(record, event, audit.OutcomeFailed); err != nil {
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
	row := store.exec.QueryRowContext(ctx, restorePreviewDiscardFailureCommitWithLeaseSQL(), args...)
	gotOperation, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("restore preview discard failure commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return gotOperation, nil
}

func validateRestorePreviewDiscardMarkRecord(record operations.OperationRecord, plan restoreplan.Plan) error {
	if record.Type != operations.OperationRestorePreviewDiscard {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview_discard")
	}
	if record.State != operations.OperationStateRunning {
		return operationLeaseInvalidRequest("operation_state", "restore preview discard mark requires running operation update")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewDiscarding {
		return operationLeaseInvalidRequest("phase", "restore preview discard mark requires discarding phase")
	}
	if restorePreviewContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore preview discard must not persist raw commands")
	}
	if err := validateRestorePreviewDiscardRecordResource(record, false); err != nil {
		return err
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if plan.Status != restoreplan.StatusPending {
		return operationLeaseInvalidRequest("restore_plan", "restore preview discard mark requires pending restore plan")
	}
	previewOperationID := restorePreviewDiscardPreviewOperationID(record)
	planID := restorePreviewDiscardRestorePlanID(record)
	if plan.PreviewOperationID != previewOperationID || plan.NamespaceID != record.NamespaceID || plan.RepoID != record.RepoID || plan.ID != planID {
		return operationLeaseInvalidRequest("restore_plan", "restore preview discard plan must match operation input")
	}
	return nil
}

func validateRestorePreviewDiscardSuccessRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestorePreviewDiscard {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview_discard")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "restore preview discard success requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewDiscardCommitted {
		return operationLeaseInvalidRequest("phase", "restore preview discard success requires committed terminal phase")
	}
	if restorePreviewContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore preview discard must not persist raw commands")
	}
	if err := validateRestorePreviewDiscardRecordResource(record, false); err != nil {
		return err
	}
	if restorePreviewDiscardPreviewOperationID(record) == "" || restorePreviewDiscardRestorePlanID(record) == "" {
		return operationLeaseInvalidRequest("restore_plan", "restore preview discard success requires preview and restore plan ids")
	}
	verification, _ := record.VerificationResult.(map[string]any)
	if status, _ := verification["restore_plan_status"].(string); strings.TrimSpace(status) != restoreplan.StatusDiscarded.String() {
		return operationLeaseInvalidRequest("verification_result", "restore preview discard success requires discarded plan verification")
	}
	return nil
}

func validateRestorePreviewDiscardFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestorePreviewDiscard {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview_discard")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "restore preview discard failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewDiscardValidate && record.Phase != operations.OperationPhaseRestorePreviewDiscarding {
		return operationLeaseInvalidRequest("phase", "restore preview discard failure must stay in validate or discarding phase")
	}
	if restorePreviewContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore preview discard must not persist raw commands")
	}
	return validateRestorePreviewDiscardRecordResource(record, true)
}

func validateRestorePreviewDiscardRecordResource(record operations.OperationRecord, requireError bool) error {
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "restore preview discard requires target repo resource")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return operationLeaseInvalidRequest("caller", "restore preview discard requires caller context")
	}
	if strings.TrimSpace(restorePreviewDiscardPreviewOperationID(record)) == "" {
		return operationLeaseInvalidRequest("input_summary", "restore preview discard requires preview operation id")
	}
	if requireError && record.Error == nil {
		return operationLeaseInvalidRequest("error", "restore preview discard failure requires operation error")
	}
	return nil
}

func validateRestorePreviewDiscardAuditEvent(record operations.OperationRecord, event audit.Event, outcome audit.Outcome) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeRestorePreviewDiscard || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event_type", "restore preview discard audit event must match operation outcome")
	}
	if event.Resource.Type != "repo" || event.Resource.ID != record.RepoID || event.Resource.NamespaceID != record.NamespaceID {
		return auditOutboxInvalidRequest("resource", "restore preview discard audit resource must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "restore preview discard audit caller context must match operation")
	}
	return nil
}

func restorePreviewDiscardMarkWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_preview_discard' AND phase = 'validate_restore_preview_discard' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'preview_operation_id' = $21 FOR UPDATE" +
		"), preview_operation AS (" +
		"SELECT o.operation_id FROM operations o, eligible_operation e WHERE o.operation_id = $21 " +
		"AND o.operation_type = 'restore_preview' AND o.operation_state = 'succeeded' AND o.phase = 'restore_preview_committed' " +
		"AND o.namespace_id = $14 AND o.repo_id = $15 AND o.resource_type = 'repo' AND o.resource_id = $15 LIMIT 1" +
		"), pending_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e, preview_operation po WHERE p.restore_plan_id = $20 " +
		"AND p.preview_operation_id = po.operation_id AND p.preview_operation_id = $21 AND p.source_save_point_id = $22 " +
		"AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'pending' FOR UPDATE" +
		"), updated_plan AS (" +
		"UPDATE restore_plans SET status = 'discarding', updated_at = $11 FROM pending_restore_plan WHERE restore_plans.restore_plan_id = pending_restore_plan.restore_plan_id RETURNING " + strings.Join(restorePlanColumns, ", ") +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, updated_plan WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + strings.Join(operationSelectColumns, ", ") +
		") SELECT " + strings.Join(restorePlanColumns, ", ") + ", " + strings.Join(operationSelectColumns, ", ") + " FROM updated_plan, updated_operation"
}

func restorePreviewDiscardSuccessCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_preview_discard' AND phase = 'restore_preview_discarding' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), discarding_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE p.restore_plan_id = $20 " +
		"AND p.preview_operation_id = e.input_summary->>'preview_operation_id' AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'discarding' FOR UPDATE" +
		"), updated_plan AS (" +
		"UPDATE restore_plans SET status = 'discarded', updated_at = $11 FROM discarding_restore_plan WHERE restore_plans.restore_plan_id = discarding_restore_plan.restore_plan_id RETURNING " + strings.Join(restorePlanColumns, ", ") +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, updated_plan WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(21, len(auditOutboxColumns)) + " FROM updated_operation, updated_plan RETURNING audit_event_id" +
		") SELECT " + strings.Join(restorePlanColumns, ", ") + ", " + strings.Join(operationSelectColumns, ", ") + " FROM updated_plan, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restorePreviewDiscardFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, phase, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_preview_discard' AND phase IN ('validate_restore_preview_discard','restore_preview_discarding') " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 FOR UPDATE" +
		"), discarding_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE e.phase = 'restore_preview_discarding' " +
		"AND p.preview_operation_id = e.input_summary->>'preview_operation_id' AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'discarding' FOR UPDATE" +
		"), updated_plan AS (" +
		"UPDATE restore_plans SET status = 'operator_intervention_required', updated_at = $11 FROM discarding_restore_plan WHERE restore_plans.restore_plan_id = discarding_restore_plan.restore_plan_id RETURNING " + strings.Join(restorePlanColumns, ", ") +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id " +
		"AND (eligible_operation.phase = 'validate_restore_preview_discard' OR EXISTS (SELECT 1 FROM updated_plan)) RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(20, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restorePreviewDiscardPreviewOperationID(record operations.OperationRecord) string {
	value, _ := record.InputSummary["preview_operation_id"].(string)
	return strings.TrimSpace(value)
}

func restorePreviewDiscardRestorePlanID(record operations.OperationRecord) string {
	for _, payload := range []any{record.JVSJSONOutput, record.VerificationResult} {
		mapped, ok := payload.(map[string]any)
		if !ok {
			continue
		}
		value, _ := mapped["restore_plan_id"].(string)
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if value := strings.TrimSpace(record.ExternalResourceIDs["restore_plan_id"]); value != "" && value != "[REDACTED]" {
		return value
	}
	return ""
}
