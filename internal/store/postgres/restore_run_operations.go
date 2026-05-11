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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

func (store *Store) MarkRestoreRunWriterFencedWithLease(ctx context.Context, fence fences.Fence, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreRunWriterFencedRecord(record, fence); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	args = append(args, restoreRunStoredPredicateArgs(record)...)
	args = append(args, repoFenceInsertArgs(fence)...)
	row := store.exec.QueryRowContext(ctx, restoreRunWriterFencedMarkWithLeaseSQL(), args...)
	gotFence, gotOperation, err := scanRepoFenceAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fences.Fence{}, operations.OperationRecord{}, operationLeaseUnavailable("restore run writer fenced mark", record.ID, err)
		}
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	return gotFence, gotOperation, nil
}

func (store *Store) MarkRestoreRunConsumingWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreRunConsumingRecord(record); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	args = append(args, restoreRunStoredPredicateArgs(record)...)
	row := store.exec.QueryRowContext(ctx, restoreRunConsumingMarkWithLeaseSQL(), args...)
	gotPlan, gotOperation, err := scanRestorePlanAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restoreplan.Plan{}, operations.OperationRecord{}, operationLeaseUnavailable("restore run consuming mark", record.ID, err)
		}
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	return gotPlan, gotOperation, nil
}

func (store *Store) CommitRestoreRunSucceededWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreRunSuccessRecord(record); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	if err := validateRestoreRunAuditEvent(record, event, audit.OutcomeSucceeded); err != nil {
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
	args := append(operationArgs, restoreRunStoredPredicateArgs(record)...)
	args = append(args, restoreRunRestorePlanID(record))
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restoreRunSuccessCommitWithLeaseSQL(), args...)
	gotPlan, gotOperation, err := scanRestorePlanAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restoreplan.Plan{}, operations.OperationRecord{}, operationLeaseUnavailable("restore run success commit", record.ID, err)
		}
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	return gotPlan, gotOperation, nil
}

func (store *Store) CommitRestoreRunStalePreviewWithLease(ctx context.Context, plan restoreplan.Plan, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreRunStalePreviewRecord(plan, record); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	if err := validateRestoreRunAuditEvent(record, event, audit.OutcomeFailed); err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	blockersJSON, err := marshalRestorePlanBlockers(plan.Blockers)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, restoreRunStoredPredicateArgs(record)...)
	args = append(args, plan.Stale, blockersJSON)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restoreRunStalePreviewCommitWithLeaseSQL(), args...)
	gotPlan, gotOperation, err := scanRestorePlanAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restoreplan.Plan{}, operations.OperationRecord{}, operationLeaseUnavailable("restore run stale preview commit", record.ID, err)
		}
		return restoreplan.Plan{}, operations.OperationRecord{}, err
	}
	return gotPlan, gotOperation, nil
}

func (store *Store) CommitRestoreRunFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreRunFailureRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateRestoreRunAuditEvent(record, event, audit.OutcomeFailed); err != nil {
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
	args := append(operationArgs, restoreRunStoredPredicateArgs(record)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restoreRunFailureCommitWithLeaseSQL(), args...)
	gotOperation, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("restore run failure commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return gotOperation, nil
}

func validateRestoreRunWriterFencedRecord(record operations.OperationRecord, fence fences.Fence) error {
	if err := validateRestoreRunProgressRecord(record, operations.OperationPhaseRestoreRunWriterFenced, "restore run writer-fenced mark requires writer-fenced phase"); err != nil {
		return err
	}
	if strings.TrimSpace(record.SessionFenceID) == "" || record.SessionFenceID != fence.ID {
		return operationLeaseInvalidRequest("session_fence_id", "restore run writer-fenced mark requires matching session fence id")
	}
	if err := fences.ValidateFence(fence); err != nil {
		return err
	}
	if fence.Kind != fences.KindWriterSession || fence.Status != fences.StatusActive || fence.RepoID != record.RepoID || fence.HolderOperationID != record.ID {
		return operationLeaseInvalidRequest("session_fence_id", "restore run writer fence must be active and owned by the operation")
	}
	return nil
}

func validateRestoreRunConsumingRecord(record operations.OperationRecord) error {
	if err := validateRestoreRunProgressRecord(record, operations.OperationPhaseRestoreRunConsuming, "restore run consuming mark requires consuming phase"); err != nil {
		return err
	}
	if strings.TrimSpace(record.SessionFenceID) == "" {
		return operationLeaseInvalidRequest("session_fence_id", "restore run consuming mark requires session fence id")
	}
	return nil
}

func validateRestoreRunProgressRecord(record operations.OperationRecord, phase, message string) error {
	if record.Type != operations.OperationRestoreRun {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_run")
	}
	if record.State != operations.OperationStateRunning {
		return operationLeaseInvalidRequest("operation_state", "restore run progress requires running operation update")
	}
	if record.Phase != phase {
		return operationLeaseInvalidRequest("phase", message)
	}
	if restoreRunContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore run must not persist raw commands")
	}
	return validateRestoreRunRecordResource(record, false)
}

func validateRestoreRunSuccessRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestoreRun {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_run")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "restore run success requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseRestoreRunCommitted {
		return operationLeaseInvalidRequest("phase", "restore run success requires committed terminal phase")
	}
	if strings.TrimSpace(record.SessionFenceID) == "" {
		return operationLeaseInvalidRequest("session_fence_id", "restore run success requires session fence id")
	}
	if restoreRunContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore run must not persist raw commands")
	}
	if err := validateRestoreRunRecordResource(record, false); err != nil {
		return err
	}
	if restoreRunRestorePlanID(record) == "" {
		return operationLeaseInvalidRequest("restore_plan", "restore run success requires restore plan id")
	}
	verification, _ := record.VerificationResult.(map[string]any)
	if status, _ := verification["restore_plan_status"].(string); strings.TrimSpace(status) != restoreplan.StatusConsumed.String() {
		return operationLeaseInvalidRequest("verification_result", "restore run success requires consumed plan verification")
	}
	return nil
}

func validateRestoreRunFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestoreRun {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_run")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "restore run failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseRestoreRunValidate && record.Phase != operations.OperationPhaseRestoreRunWriterFenced && record.Phase != operations.OperationPhaseRestoreRunConsuming {
		return operationLeaseInvalidRequest("phase", "restore run failure must stay in validate, writer-fenced, or consuming phase")
	}
	if record.Phase != operations.OperationPhaseRestoreRunValidate && strings.TrimSpace(record.SessionFenceID) == "" {
		return operationLeaseInvalidRequest("session_fence_id", "restore run post-fence failure requires session fence id")
	}
	if restoreRunContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore run must not persist raw commands")
	}
	return validateRestoreRunRecordResource(record, true)
}

func validateRestoreRunStalePreviewRecord(plan restoreplan.Plan, record operations.OperationRecord) error {
	if err := validateRestoreRunFailureRecord(record); err != nil {
		return err
	}
	if record.State != operations.OperationStateFailed {
		return operationLeaseInvalidRequest("operation_state", "stale restore preview must be a typed failed operation")
	}
	if record.Phase != operations.OperationPhaseRestoreRunValidate {
		return operationLeaseInvalidRequest("phase", "stale restore preview must fail before writer fence")
	}
	if strings.TrimSpace(record.SessionFenceID) != "" {
		return operationLeaseInvalidRequest("session_fence_id", "stale restore preview must not hold a writer fence")
	}
	if record.Error == nil || record.Error.Code != "RESTORE_PREVIEW_STALE" {
		return operationLeaseInvalidRequest("error", "stale restore preview requires RESTORE_PREVIEW_STALE")
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	if plan.Status != restoreplan.StatusPending || !plan.Stale {
		return operationLeaseInvalidRequest("restore_plan", "stale restore preview requires a pending stale restore plan")
	}
	if plan.NamespaceID != record.NamespaceID || plan.RepoID != record.RepoID || plan.PreviewOperationID != restoreRunPreviewOperationID(record) {
		return operationLeaseInvalidRequest("restore_plan", "stale restore preview plan must match operation input")
	}
	if !restorePlanHasBlocker(plan, "restore_preview_stale") {
		return operationLeaseInvalidRequest("restore_plan", "stale restore preview requires durable blocker")
	}
	return nil
}

func restorePlanHasBlocker(plan restoreplan.Plan, code string) bool {
	for _, blocker := range plan.Blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
}

func validateRestoreRunRecordResource(record operations.OperationRecord, requireError bool) error {
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "restore run requires target repo resource")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return operationLeaseInvalidRequest("caller", "restore run requires caller context")
	}
	if previewID := restoreRunPreviewOperationID(record); previewID == "" {
		return operationLeaseInvalidRequest("input_summary", "restore run requires preview operation id")
	} else if err := pathresolver.ValidateID(pathresolver.OperationID, previewID); err != nil {
		return operationLeaseInvalidRequest("input_summary", "restore run preview operation id is invalid")
	}
	if requireError && record.Error == nil {
		return operationLeaseInvalidRequest("error", "restore run failure requires operation error")
	}
	return nil
}

func validateRestoreRunAuditEvent(record operations.OperationRecord, event audit.Event, outcome audit.Outcome) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeRestoreRun || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event_type", "restore run audit event must match operation outcome")
	}
	if event.Resource.Type != "repo" || event.Resource.ID != record.RepoID || event.Resource.NamespaceID != record.NamespaceID {
		return auditOutboxInvalidRequest("resource", "restore run audit resource must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "restore run audit caller context must match operation")
	}
	if containsForbiddenRestoreRunCommand(event.Details) {
		return auditOutboxInvalidRequest("details", "restore run audit details must not persist raw commands")
	}
	return nil
}

func restoreRunStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.NamespaceID, record.RepoID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID, restoreRunPreviewOperationID(record), record.SessionFenceID}
}

func restoreRunWriterFencedMarkWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_run' AND phase = 'validate_restore_run' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'preview_operation_id' = $20 FOR UPDATE" +
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
		restoreRunWriterFencedOperationUpdateSetSQL() +
		"FROM eligible_operation, confirmed_writer_fence WHERE operations.operation_id = eligible_operation.operation_id AND confirmed_writer_fence.fence_id = $21 AND NOT EXISTS (SELECT 1 FROM held_lifecycle_fence) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + prefixedColumns("confirmed_writer_fence", repoFenceColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM confirmed_writer_fence, updated_operation"
}

func restoreRunConsumingMarkWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_run' AND phase = 'restore_run_writer_fenced' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'preview_operation_id' = $20 AND session_fence_id = $21 FOR UPDATE" +
		"), active_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, eligible_operation WHERE repo_fences.repo_id = $15 AND repo_fences.fence_id = $21 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), preview_operation AS (" +
		"SELECT o.operation_id FROM operations o, eligible_operation e WHERE o.operation_id = $20 " +
		"AND o.operation_type = 'restore_preview' AND o.operation_state = 'succeeded' AND o.phase = 'restore_preview_committed' " +
		"AND o.namespace_id = $14 AND o.repo_id = $15 AND o.resource_type = 'repo' AND o.resource_id = $15 LIMIT 1" +
		"), pending_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e, preview_operation po WHERE p.preview_operation_id = po.operation_id AND p.preview_operation_id = $20 " +
		"AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'pending' AND p.stale = false FOR UPDATE" +
		"), updated_plan AS (" +
		"UPDATE restore_plans SET status = 'consuming', updated_at = $11 FROM pending_restore_plan, active_writer_fence WHERE restore_plans.restore_plan_id = pending_restore_plan.restore_plan_id RETURNING " + prefixedColumns("restore_plans", restorePlanColumns) +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, updated_plan WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + prefixedColumns("updated_plan", restorePlanColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM updated_plan, updated_operation"
}

func restoreRunSuccessCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_run' AND phase = 'restore_run_consuming' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'preview_operation_id' = $20 AND session_fence_id = $21 FOR UPDATE" +
		"), consuming_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE p.restore_plan_id = $22 AND p.preview_operation_id = e.input_summary->>'preview_operation_id' " +
		"AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'consuming' FOR UPDATE" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, eligible_operation, consuming_restore_plan WHERE repo_fences.repo_id = $15 AND repo_fences.fence_id = $21 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_writer_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM held_writer_fence WHERE repo_fences.fence_id = held_writer_fence.fence_id RETURNING repo_fences.fence_id" +
		"), updated_plan AS (" +
		"UPDATE restore_plans SET status = 'consumed', updated_at = $11 FROM consuming_restore_plan, released_writer_fence WHERE restore_plans.restore_plan_id = consuming_restore_plan.restore_plan_id RETURNING " + prefixedColumns("restore_plans", restorePlanColumns) +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, updated_plan, released_writer_fence WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(23, len(auditOutboxColumns)) + " FROM updated_operation, updated_plan, released_writer_fence RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("updated_plan", restorePlanColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM updated_plan, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restoreRunStalePreviewCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_run' AND phase = 'validate_restore_run' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'preview_operation_id' = $20 AND $21 = '' FOR UPDATE" +
		"), pending_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE p.preview_operation_id = e.input_summary->>'preview_operation_id' " +
		"AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'pending' FOR UPDATE" +
		"), updated_plan AS (" +
		"UPDATE restore_plans SET stale = $22, blockers_json = $23, updated_at = $11 FROM pending_restore_plan WHERE restore_plans.restore_plan_id = pending_restore_plan.restore_plan_id RETURNING " + prefixedColumns("restore_plans", restorePlanColumns) +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, updated_plan WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(24, len(auditOutboxColumns)) + " FROM updated_operation, updated_plan RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("updated_plan", restorePlanColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) + " FROM updated_plan, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restoreRunFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, phase, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore_run' AND phase IN ('validate_restore_run','restore_run_writer_fenced','restore_run_consuming') " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'preview_operation_id' = $20 " +
		"AND ((phase = 'validate_restore_run' AND $21 = '') OR (phase IN ('restore_run_writer_fenced','restore_run_consuming') AND session_fence_id = $21 AND $21 <> '')) FOR UPDATE" +
		"), pending_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE e.phase = 'restore_run_writer_fenced' AND p.preview_operation_id = e.input_summary->>'preview_operation_id' AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'pending' FOR UPDATE" +
		"), consuming_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE e.phase = 'restore_run_consuming' AND p.preview_operation_id = e.input_summary->>'preview_operation_id' AND p.namespace_id = $14 AND p.repo_id = $15 AND p.status = 'consuming' FOR UPDATE" +
		"), updated_plan AS (" +
		"UPDATE restore_plans SET status = 'operator_intervention_required', updated_at = $11 FROM consuming_restore_plan WHERE restore_plans.restore_plan_id = consuming_restore_plan.restore_plan_id RETURNING restore_plans.restore_plan_id" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, eligible_operation WHERE eligible_operation.phase = 'restore_run_writer_fenced' AND repo_fences.repo_id = $15 AND repo_fences.fence_id = $21 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_writer_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM held_writer_fence, pending_restore_plan WHERE repo_fences.fence_id = held_writer_fence.fence_id RETURNING repo_fences.fence_id" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id " +
		"AND (eligible_operation.phase = 'validate_restore_run' OR (eligible_operation.phase = 'restore_run_writer_fenced' AND EXISTS (SELECT 1 FROM pending_restore_plan) AND EXISTS (SELECT 1 FROM released_writer_fence)) OR (eligible_operation.phase = 'restore_run_consuming' AND EXISTS (SELECT 1 FROM updated_plan))) RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(22, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restoreRunWriterFencedOperationUpdateSetSQL() string {
	return "UPDATE operations SET " +
		"operation_state = $1, " +
		"phase = $2, " +
		"lease_owner = CASE WHEN $1 = 'running' THEN operations.lease_owner ELSE NULL END, " +
		"lease_expires_at = CASE WHEN $1 = 'running' THEN operations.lease_expires_at ELSE NULL END, " +
		"external_resource_ids = $3, " +
		"input_summary = $4, " +
		"jvs_json_output = $5, " +
		"verification_result = $6, " +
		"compensation_status = $7, " +
		"error_json = $8, " +
		"started_at = COALESCE(operations.started_at, $9, $11), " +
		"finished_at = CASE WHEN $1 IN ('succeeded', 'failed', 'cancelled') THEN COALESCE($10, $11) ELSE NULL END, " +
		"updated_at = $11, " +
		"session_fence_id = $21 "
}

func restoreRunPreviewOperationID(record operations.OperationRecord) string {
	value, _ := record.InputSummary["preview_operation_id"].(string)
	return strings.TrimSpace(value)
}

func restoreRunRestorePlanID(record operations.OperationRecord) string {
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

func restoreRunContainsForbiddenCommand(record operations.OperationRecord) bool {
	return containsForbiddenRestoreRunCommand(record.InputSummary) ||
		containsForbiddenRestoreRunCommand(record.JVSJSONOutput) ||
		containsForbiddenRestoreRunCommand(record.VerificationResult)
}

func containsForbiddenRestoreRunCommand(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, value := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			switch normalized {
			case "run_command", "recommended_next_command", "restore_command", "mount_command", "raw_mount_command", "direct_mount_command", "command":
				return true
			}
			if containsForbiddenRestoreRunCommand(value) {
				return true
			}
		}
	case map[string]string:
		for key := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			switch normalized {
			case "run_command", "recommended_next_command", "restore_command", "mount_command", "raw_mount_command", "direct_mount_command", "command":
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsForbiddenRestoreRunCommand(item) {
				return true
			}
		}
	}
	return false
}

func scanRepoFenceAndOperation(row rowScanner) (fences.Fence, operations.OperationRecord, error) {
	var fence fences.Fence
	var kind, status string
	var releasedAt, recoveryStartedAt, recoveredAt sql.NullTime
	var recoveryOperationID, recoveryReason sql.NullString
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte

	if err := row.Scan(
		&fence.ID,
		&fence.RepoID,
		&kind,
		&fence.HolderOperationID,
		&status,
		&fence.ExpiresAt,
		&releasedAt,
		&recoveryOperationID,
		&recoveryReason,
		&recoveryStartedAt,
		&recoveredAt,
		&fence.CreatedAt,
		&fence.UpdatedAt,
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
		return fences.Fence{}, operations.OperationRecord{}, err
	}

	fence.Kind = fences.Kind(kind)
	fence.Status = fences.Status(status)
	fence.ReleasedAt = nullTimePtr(releasedAt)
	fence.RecoveryOperationID = nullStringValue(recoveryOperationID)
	fence.RecoveryReason = nullStringValue(recoveryReason)
	fence.RecoveryStartedAt = nullTimePtr(recoveryStartedAt)
	fence.RecoveredAt = nullTimePtr(recoveredAt)
	if err := fences.ValidateFence(fence); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, fmt.Errorf("invalid repo fence %q: %w", fence.ID, err)
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
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return fences.Fence{}, operations.OperationRecord{}, fmt.Errorf("unmarshal error_json: %w", err)
		}
		record.Error = &opErr
	}
	return fence, record.Sanitized(), nil
}
