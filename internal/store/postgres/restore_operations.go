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
)

func (store *Store) MarkRestoreWriterFencedWithLease(ctx context.Context, fence fences.Fence, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (fences.Fence, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreWriterFencedRecord(record, fence); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	args = append(args, restoreStoredPredicateArgs(record)...)
	args = append(args, repoFenceInsertArgs(fence)...)
	row := store.exec.QueryRowContext(ctx, restoreWriterFencedMarkWithLeaseSQL(), args...)
	gotFence, gotOperation, err := scanRepoFenceAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fences.Fence{}, operations.OperationRecord{}, operationLeaseUnavailable("restore writer fenced mark", record.ID, err)
		}
		return fences.Fence{}, operations.OperationRecord{}, err
	}
	return gotFence, gotOperation, nil
}

func (store *Store) CommitRestoreSucceededWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreSuccessRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateRestoreAuditEvent(record, event, audit.OutcomeSucceeded); err != nil {
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
	args := append(operationArgs, restoreStoredPredicateArgs(record)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restoreSuccessCommitWithLeaseSQL(), args...)
	gotOperation, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("restore success commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return gotOperation, nil
}

func (store *Store) CommitRestoreFailedWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateRestoreFailureRecord(record); err != nil {
		return operations.OperationRecord{}, err
	}
	if err := validateRestoreAuditEvent(record, event, audit.OutcomeFailed); err != nil {
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
	args := append(operationArgs, restoreStoredPredicateArgs(record)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, restoreFailureCommitWithLeaseSQL(), args...)
	gotOperation, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("restore failure commit", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return gotOperation, nil
}

func validateRestoreWriterFencedRecord(record operations.OperationRecord, fence fences.Fence) error {
	if err := validateRestoreProgressRecord(record, operations.OperationPhaseRestoreWriterFenced, "restore writer-fenced mark requires writer-fenced phase"); err != nil {
		return err
	}
	if strings.TrimSpace(record.SessionFenceID) == "" || record.SessionFenceID != fence.ID {
		return operationLeaseInvalidRequest("session_fence_id", "restore writer-fenced mark requires matching session fence id")
	}
	if err := fences.ValidateFence(fence); err != nil {
		return err
	}
	if fence.Kind != fences.KindWriterSession || fence.Status != fences.StatusActive || fence.RepoID != record.RepoID || fence.HolderOperationID != record.ID {
		return operationLeaseInvalidRequest("session_fence_id", "restore writer fence must be active and owned by the operation")
	}
	return nil
}

func validateRestoreProgressRecord(record operations.OperationRecord, phase, message string) error {
	if record.Type != operations.OperationRestore {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore")
	}
	if record.State != operations.OperationStateRunning {
		return operationLeaseInvalidRequest("operation_state", "restore progress requires running operation update")
	}
	if record.Phase != phase {
		return operationLeaseInvalidRequest("phase", message)
	}
	if restoreRecordWasRedacted(record) {
		return operationLeaseInvalidRequest("redaction", "restore progress must not persist redacted storage or command evidence")
	}
	if restoreContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore must not persist raw commands")
	}
	return validateRestoreRecordResource(record, false)
}

func validateRestoreSuccessRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestore {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "restore success requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseRestoreCommitted {
		return operationLeaseInvalidRequest("phase", "restore success requires committed terminal phase")
	}
	if strings.TrimSpace(record.SessionFenceID) == "" {
		return operationLeaseInvalidRequest("session_fence_id", "restore success requires session fence id")
	}
	if restoreRecordWasRedacted(record) {
		return operationLeaseInvalidRequest("redaction", "restore success must not persist redacted storage or command evidence")
	}
	if restoreContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore must not persist raw commands")
	}
	if err := validateRestoreRecordResource(record, false); err != nil {
		return err
	}
	jvsOutput, _ := record.JVSJSONOutput.(map[string]any)
	if restored, _ := jvsOutput["restored_save_point_id"].(string); strings.TrimSpace(restored) == "" {
		return operationLeaseInvalidRequest("jvs_json_output", "restore success requires restored save point evidence")
	}
	if previousHead, ok := jvsOutput["previous_head"].(string); ok && strings.TrimSpace(previousHead) == "" {
		return operationLeaseInvalidRequest("jvs_json_output", "restore success requires non-empty previous head evidence when present")
	}
	if newHead, _ := jvsOutput["new_head"].(string); strings.TrimSpace(newHead) == "" {
		return operationLeaseInvalidRequest("jvs_json_output", "restore success requires new head evidence")
	}
	return nil
}

func validateRestoreFailureRecord(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestore {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore")
	}
	if record.State != operations.OperationStateFailed && record.State != operations.OperationStateOperatorInterventionRequired {
		return operationLeaseInvalidRequest("operation_state", "restore failure requires failed or operator intervention operation update")
	}
	if record.Phase != operations.OperationPhaseRestoreValidate && record.Phase != operations.OperationPhaseRestoreWriterFenced {
		return operationLeaseInvalidRequest("phase", "restore failure must stay in validate or writer-fenced phase")
	}
	if record.Phase != operations.OperationPhaseRestoreValidate && strings.TrimSpace(record.SessionFenceID) == "" {
		return operationLeaseInvalidRequest("session_fence_id", "restore post-fence failure requires session fence id")
	}
	if restoreRecordWasRedacted(record) {
		return operationLeaseInvalidRequest("redaction", "restore failure must not persist redacted storage or command evidence")
	}
	if restoreContainsForbiddenCommand(record) {
		return operationLeaseInvalidRequest("jvs_json_output", "restore must not persist raw commands")
	}
	return validateRestoreRecordResource(record, true)
}

func validateRestoreRecordResource(record operations.OperationRecord, requireError bool) error {
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "restore requires target repo resource")
	}
	if strings.TrimSpace(record.CallerService) == "" || strings.TrimSpace(record.CorrelationID) == "" || strings.TrimSpace(record.AuthorizedActor.Type) == "" || strings.TrimSpace(record.AuthorizedActor.ID) == "" {
		return operationLeaseInvalidRequest("caller", "restore requires caller context")
	}
	savePointID := restoreSavePointID(record)
	if savePointID == "" {
		return operationLeaseInvalidRequest("input_summary", "restore requires save point id")
	} else if err := operations.ValidateSavePointID(savePointID); err != nil {
		return operationLeaseInvalidRequest("input_summary", "restore save point id is invalid")
	}
	if requireError && record.Error == nil {
		return operationLeaseInvalidRequest("error", "restore failure requires operation error")
	}
	return nil
}

func validateRestoreAuditEvent(record operations.OperationRecord, event audit.Event, outcome audit.Outcome) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeRestore || event.Outcome != outcome {
		return auditOutboxInvalidRequest("event_type", "restore audit event must match operation outcome")
	}
	if event.Resource.Type != "repo" || event.Resource.ID != record.RepoID || event.Resource.NamespaceID != record.NamespaceID {
		return auditOutboxInvalidRequest("resource", "restore audit resource must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "restore audit caller context must match operation")
	}
	if containsForbiddenDirectRestoreEvidence(event.Details) {
		return auditOutboxInvalidRequest("details", "restore audit details must not persist raw commands")
	}
	return nil
}

func restoreStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.NamespaceID, record.RepoID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID, restoreSavePointID(record), record.SessionFenceID}
}

func restoreSavePointID(record operations.OperationRecord) string {
	value, _ := record.InputSummary["save_point_id"].(string)
	return strings.TrimSpace(value)
}

func restoreContainsForbiddenCommand(record operations.OperationRecord) bool {
	return containsForbiddenDirectRestoreEvidence(record.InputSummary) ||
		containsForbiddenDirectRestoreEvidence(record.ExternalResourceIDs) ||
		containsForbiddenDirectRestoreEvidence(record.JVSJSONOutput) ||
		containsForbiddenDirectRestoreEvidence(record.VerificationResult)
}

func restoreRecordWasRedacted(record operations.OperationRecord) bool {
	if !record.Redaction.Redacted && len(record.Redaction.Fields) == 0 {
		return false
	}
	if record.Redaction.Redacted && len(record.Redaction.Fields) == 0 {
		return true
	}
	for _, field := range record.Redaction.Fields {
		if restoreRedactionFieldIsForbiddenEvidence(field) {
			return true
		}
	}
	return false
}

func restoreRedactionFieldIsForbiddenEvidence(field string) bool {
	normalized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, field)
	if normalized == "" {
		return false
	}
	for _, forbidden := range []string{
		"planid",
		"restoreplanid",
		"runcommand",
		"recommendednextcommand",
		"mountcommand",
		"restorecommand",
		"rawcommand",
		"rawmountcommand",
		"directmountcommand",
		"stdout",
		"stderr",
		"command",
		"controlroot",
		"controlrootpath",
		"controlpath",
		"payloadroot",
		"payloadrootpath",
		"payloadvolumesubdir",
		"controlvolumesubdir",
		"reporoot",
		"targetfolder",
		"targetcontrolroot",
		"rawpath",
		"internalpath",
		"internalpaths",
		"internalroot",
		"internalrootpath",
		"homepath",
	} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

func containsForbiddenDirectRestoreEvidence(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, value := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			switch normalized {
			case "plan_id", "restore_plan_id":
				return true
			}
			if containsForbiddenDirectRestoreEvidence(value) {
				return true
			}
		}
	case map[string]string:
		for key := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			switch normalized {
			case "plan_id", "restore_plan_id":
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsForbiddenDirectRestoreEvidence(item) {
				return true
			}
		}
	}
	return containsForbiddenRestoreCommandText(value)
}

func containsForbiddenRestoreCommandText(value any) bool {
	switch typed := value.(type) {
	case string:
		normalized := strings.ToLower(typed)
		return strings.Contains(normalized, "run_command") ||
			strings.Contains(normalized, "recommended_next_command") ||
			strings.Contains(normalized, "jvs restore --run")
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "run_command" || normalized == "recommended_next_command" || normalized == "restore_command" || normalized == "mount_command" {
				return true
			}
			if containsForbiddenRestoreCommandText(child) {
				return true
			}
		}
	case map[string]string:
		for key, child := range typed {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "run_command" || normalized == "recommended_next_command" || normalized == "restore_command" || normalized == "mount_command" {
				return true
			}
			if containsForbiddenRestoreCommandText(child) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsForbiddenRestoreCommandText(item) {
				return true
			}
		}
	}
	return false
}

func restoreWriterFencedMarkWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore' AND phase = 'validate_restore' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'save_point_id' = $20 FOR UPDATE" +
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

func restoreSuccessCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore' AND phase = 'restore_writer_fenced' " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'save_point_id' = $20 AND session_fence_id = $21 FOR UPDATE" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, eligible_operation WHERE repo_fences.repo_id = $15 AND repo_fences.fence_id = $21 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_writer_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM held_writer_fence WHERE repo_fences.fence_id = held_writer_fence.fence_id RETURNING repo_fences.fence_id" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation, released_writer_fence WHERE operations.operation_id = eligible_operation.operation_id RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(22, len(auditOutboxColumns)) + " FROM updated_operation, released_writer_fence RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restoreFailureCommitWithLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, phase, input_summary FROM operations WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 " +
		"AND operation_type = 'restore' AND phase IN ('validate_restore','restore_writer_fenced') " +
		"AND namespace_id = $14 AND repo_id = $15 AND resource_type = 'repo' AND resource_id = $15 " +
		"AND caller_service = $16 AND correlation_id = $17 AND authorized_actor_type = $18 AND authorized_actor_id = $19 " +
		"AND input_summary->>'save_point_id' = $20 " +
		"AND ((phase = 'validate_restore' AND $21 = '') OR (phase = 'restore_writer_fenced' AND session_fence_id = $21 AND $21 <> '')) FOR UPDATE" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, eligible_operation WHERE eligible_operation.phase = 'restore_writer_fenced' AND repo_fences.repo_id = $15 AND repo_fences.fence_id = $21 AND repo_fences.fence_kind = 'writer_session' AND repo_fences.holder_operation_id = $12 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_writer_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $11, updated_at = $11 FROM held_writer_fence WHERE repo_fences.fence_id = held_writer_fence.fence_id RETURNING repo_fences.fence_id" +
		"), updated_operation AS (" +
		operationLeaseFencedUpdateSetSQL() +
		"FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id " +
		"AND (eligible_operation.phase = 'validate_restore' OR EXISTS (SELECT 1 FROM released_writer_fence)) RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(22, len(auditOutboxColumns)) + " FROM updated_operation RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func restoreWriterFencedOperationUpdateSetSQL() string {
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
