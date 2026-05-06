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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

var workloadMountFullColumns = []string{
	"mount_binding_id",
	"namespace_id",
	"repo_id",
	"volume_id",
	"mount_path",
	"read_only",
	"status",
	"lease_seconds",
	"lease_expires_at",
	"last_heartbeat_at",
	"last_observed_at",
	"confirmed_unmounted_at",
	"unable_to_write_at",
	"terminal_observed_at",
	"status_reason",
	"created_at",
	"updated_at",
}

func (store *Store) GetWorkloadMountBinding(ctx context.Context, mountBindingID string) (workloadmount.Binding, error) {
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, strings.TrimSpace(mountBindingID)); err != nil {
		return workloadmount.Binding{}, err
	}
	row := store.exec.QueryRowContext(ctx, workloadMountBindingFullSelectSQL()+" WHERE mount_binding_id = $1", mountBindingID)
	return scanWorkloadMountBindingFull(row)
}

func (store *Store) ListStaleNonTerminalWorkloadMountBindings(ctx context.Context, now time.Time, limit int) ([]workloadmount.Binding, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list stale workload mount bindings: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list stale workload mount bindings: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, workloadMountStaleNonTerminalSelectSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bindings []workloadmount.Binding
	for rows.Next() {
		binding, err := scanWorkloadMountBindingFull(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bindings, nil
}

func (store *Store) GetOrchestratorMountPlan(ctx context.Context, namespaceID, mountBindingID string) (workloadmount.Plan, error) {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, strings.TrimSpace(namespaceID)); err != nil {
		return workloadmount.Plan{}, err
	}
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, strings.TrimSpace(mountBindingID)); err != nil {
		return workloadmount.Plan{}, err
	}
	var plan workloadmount.Plan
	var readOnly, allowPrivileged bool
	row := store.exec.QueryRowContext(ctx, workloadMountPlanSelectSQL(), namespaceID, mountBindingID)
	if err := row.Scan(&plan.MountBindingID, &plan.VolumeID, &plan.PayloadVolumeSubdir, &plan.MountPath, &readOnly, &allowPrivileged); err != nil {
		return workloadmount.Plan{}, err
	}
	plan.ReadOnly = readOnly
	plan.SecretRef = workloadmount.SecretRef{Namespace: "afscp-runtime", Name: "afscp-volume-" + strings.TrimPrefix(plan.VolumeID, "vol_")}
	plan.SecurityPolicy = workloadmount.SecurityPolicy{RunAsNonRoot: true, AllowPrivileged: allowPrivileged, JVSControlOutsidePayload: true}
	return plan, nil
}

func (store *Store) CommitWorkloadMountBindingCreateWithLease(ctx context.Context, binding workloadmount.Binding, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	if err := binding.Validate(); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	record := sanitized.Record()
	if err := validateWorkloadMountOperationRecord(record, operations.OperationMountBindingCreate, operations.OperationPhaseMountBindingCreateCommitted, binding.ID); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	if record.NamespaceID != binding.NamespaceID || record.RepoID != binding.RepoID {
		return workloadmount.Binding{}, operations.OperationRecord{}, operationLeaseInvalidRequest("binding", "operation metadata must match workload mount binding")
	}
	if err := validateWorkloadMountAuditEvent(record, event); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	args, err := workloadMountBindingCreateCommitArgs(binding, record, owner, now, event)
	if err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	return store.commitWorkloadMountBinding(ctx, workloadMountBindingCreateCommitSQL(), args...)
}

func (store *Store) CommitWorkloadMountBindingStatusWithLease(ctx context.Context, mountBindingID string, status sessionstate.MountStatus, reason string, observedAt time.Time, leaseExpiresAt *time.Time, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateWorkloadMountOperationRecord(record, operations.OperationMountBindingStatusUpdate, operations.OperationPhaseMountBindingStatusCommitted, mountBindingID); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	if !workloadmount.ValidOrchestratorStatus(status) {
		return workloadmount.Binding{}, operations.OperationRecord{}, operationLeaseInvalidRequest("status", "invalid workload mount status")
	}
	if observedAt.IsZero() {
		return workloadmount.Binding{}, operations.OperationRecord{}, operationLeaseInvalidRequest("observed_at", "observed_at must be set")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > workloadmount.MaxReasonLength {
		return workloadmount.Binding{}, operations.OperationRecord{}, operationLeaseInvalidRequest("reason", "workload mount status reason is too long")
	}
	if leaseExpiresAt != nil && leaseExpiresAt.Before(observedAt) {
		return workloadmount.Binding{}, operations.OperationRecord{}, operationLeaseInvalidRequest("lease_expires_at", "lease_expires_at cannot be before observed_at")
	}
	if err := validateWorkloadMountAuditEvent(record, event); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	args, err := workloadMountBindingUpdateCommitArgs(mountBindingID, string(status), reason, observedAt.UTC(), leaseExpiresAt, record, owner, now, event)
	if err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	return store.commitWorkloadMountBinding(ctx, workloadMountBindingStatusCommitSQL(), args...)
}

func (store *Store) CommitWorkloadMountBindingHeartbeatWithLease(ctx context.Context, mountBindingID string, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateWorkloadMountOperationRecord(record, operations.OperationMountBindingHeartbeat, operations.OperationPhaseMountBindingHeartbeatCommitted, mountBindingID); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	if err := validateWorkloadMountAuditEvent(record, event); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	args, err := workloadMountBindingUpdateCommitArgs(mountBindingID, "", "", now, nil, record, owner, now, event)
	if err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	return store.commitWorkloadMountBinding(ctx, workloadMountBindingHeartbeatCommitSQL(), args...)
}

func (store *Store) CommitWorkloadMountBindingReleaseWithLease(ctx context.Context, mountBindingID string, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateWorkloadMountOperationRecord(record, operations.OperationMountBindingRelease, operations.OperationPhaseMountBindingReleaseCommitted, mountBindingID); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	if err := validateWorkloadMountAuditEvent(record, event); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	args, err := workloadMountBindingUpdateCommitArgs(mountBindingID, "", "released", now, nil, record, owner, now, event)
	if err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	return store.commitWorkloadMountBinding(ctx, workloadMountBindingReleaseCommitSQL(), args...)
}

func (store *Store) CommitWorkloadMountBindingRevokeWithLease(ctx context.Context, mountBindingID string, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	record := sanitized.Record()
	if err := validateWorkloadMountOperationRecord(record, operations.OperationMountBindingRevoke, operations.OperationPhaseMountBindingRevokeCommitted, mountBindingID); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	if err := validateWorkloadMountAuditEvent(record, event); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	args, err := workloadMountBindingUpdateCommitArgs(mountBindingID, "", "releasing", now, nil, record, owner, now, event)
	if err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	return store.commitWorkloadMountBinding(ctx, workloadMountBindingRevokeCommitSQL(), args...)
}

func (store *Store) commitWorkloadMountBinding(ctx context.Context, query string, args ...any) (workloadmount.Binding, operations.OperationRecord, error) {
	row := store.exec.QueryRowContext(ctx, query, args...)
	binding, record, err := scanWorkloadMountBindingAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workloadmount.Binding{}, operations.OperationRecord{}, operationLeaseUnavailable("workload mount binding commit", "", err)
		}
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	return binding, record, nil
}

func validateWorkloadMountOperationRecord(record operations.OperationRecord, typ operations.OperationType, phase, mountBindingID string) error {
	if record.Type != typ {
		return operationLeaseInvalidRequest("operation_type", "operation type does not match workload mount commit")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "workload mount commit requires succeeded operation update")
	}
	if strings.TrimSpace(record.Phase) != phase {
		return operationLeaseInvalidRequest("phase", "workload mount commit requires committed phase")
	}
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, mountBindingID); err != nil {
		return err
	}
	if record.MountBindingID != mountBindingID || record.Resource.Type != "workload_mount_binding" || record.Resource.ID != mountBindingID {
		return operationLeaseInvalidRequest("mount_binding_id", "operation must target workload mount binding")
	}
	return nil
}

func validateWorkloadMountAuditEvent(record operations.OperationRecord, event audit.Event) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	wantType, ok := audit.EventTypeForOperationType(record.Type.String())
	if !ok {
		return auditOutboxInvalidRequest("event_type", fmt.Sprintf("operation type %q has no audit event type", record.Type))
	}
	if event.Type != wantType {
		return auditOutboxInvalidRequest("event_type", "workload mount audit event must match operation type")
	}
	if event.Outcome != audit.OutcomeSucceeded {
		return auditOutboxInvalidRequest("outcome", "workload mount audit outcome must be succeeded")
	}
	if event.Resource.Type != "workload_mount_binding" || event.Resource.ID != record.MountBindingID || event.Resource.NamespaceID != record.NamespaceID {
		return auditOutboxInvalidRequest("resource", "workload mount audit resource must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "workload mount audit caller context must match operation")
	}
	return nil
}

func workloadMountBindingCreateCommitArgs(binding workloadmount.Binding, record operations.OperationRecord, owner string, now time.Time, event audit.Event) ([]any, error) {
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return nil, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return nil, err
	}
	args := append(operationArgs, workloadMountBindingArgs(binding)...)
	return append(args, auditOutboxInsertArgs(outboxRecord)...), nil
}

func workloadMountBindingUpdateCommitArgs(mountBindingID, status, reason string, observedAt time.Time, leaseExpiresAt *time.Time, record operations.OperationRecord, owner string, now time.Time, event audit.Event) ([]any, error) {
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return nil, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return nil, err
	}
	args := append(operationArgs, mountBindingID, status, reason, observedAt.UTC(), timePtrArg(leaseExpiresAt))
	return append(args, auditOutboxInsertArgs(outboxRecord)...), nil
}

func workloadMountBindingArgs(binding workloadmount.Binding) []any {
	return []any{
		binding.ID,
		binding.NamespaceID,
		binding.RepoID,
		binding.VolumeID,
		binding.MountPath,
		binding.ReadOnly,
		string(binding.Status),
		binding.LeaseSeconds,
		binding.LeaseExpiresAt,
		timePtrArg(binding.LastHeartbeatAt),
		timePtrArg(binding.LastObservedAt),
		timePtrArg(binding.ConfirmedUnmountedAt),
		timePtrArg(binding.UnableToWriteAt),
		timePtrArg(binding.TerminalObservedAt),
		binding.StatusReason,
		binding.CreatedAt,
		binding.UpdatedAt,
	}
}

func workloadMountBindingFullSelectSQL() string {
	return "SELECT " + strings.Join(workloadMountFullColumns, ", ") + " FROM workload_mount_bindings"
}

func workloadMountStaleNonTerminalSelectSQL() string {
	return workloadMountBindingFullSelectSQL() + " WHERE lease_expires_at <= $1 AND status IN ('issued','pending','active','releasing') ORDER BY lease_expires_at, mount_binding_id LIMIT $2"
}

func workloadMountPlanSelectSQL() string {
	return "SELECT b.mount_binding_id, b.volume_id, r.payload_volume_subdir, b.mount_path, b.read_only, COALESCE((nvb.mount_policy->>'allow_privileged_workload')::boolean, false) " +
		"FROM workload_mount_bindings b JOIN repos r ON r.namespace_id = b.namespace_id AND r.repo_id = b.repo_id " +
		"JOIN namespace_volume_bindings nvb ON nvb.namespace_id = b.namespace_id " +
		"JOIN namespaces ns ON ns.namespace_id = b.namespace_id " +
		"WHERE b.namespace_id = $1 AND b.mount_binding_id = $2 AND ns.status = 'active' AND nvb.status = 'active' AND b.status IN ('issued','pending','active','releasing')"
}

func workloadMountBindingCreateCommitSQL() string {
	return "WITH active_namespace AS (" +
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $15 AND status = 'active' FOR SHARE" +
		"), active_binding AS (" +
		"SELECT namespace_id FROM namespace_volume_bindings WHERE namespace_id = $15 AND status = 'active' " +
		"AND COALESCE((mount_policy->>'workload_mount_enabled')::boolean, false) = true " +
		"AND COALESCE((mount_policy->>'workload_mount_requires_jvs_external_control_root')::boolean, false) = true FOR SHARE" +
		"), active_repo AS (" +
		"SELECT repo_id FROM repos WHERE namespace_id = $15 AND repo_id = $16 AND volume_id = $17 AND repo_kind = 'repo' AND status = 'active' AND lifecycle_status = 'active' FOR UPDATE" +
		"), active_volume AS (" +
		"SELECT volume_id FROM volumes WHERE volume_id = $17 AND status = 'active' " +
		"AND COALESCE((capabilities->>'workload_mount')::boolean, false) = true " +
		"AND COALESCE((capabilities->>'jvs_external_control_root')::boolean, false) = true FOR SHARE" +
		"), held_lifecycle_fence AS (" +
		"SELECT fence_id FROM repo_fences, active_repo WHERE repo_fences.repo_id = active_repo.repo_id AND repo_fences.fence_kind = 'lifecycle' AND status IN ('active','expired','recovery_required') AND released_at IS NULL AND recovered_at IS NULL FOR UPDATE" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, active_repo WHERE repo_fences.repo_id = active_repo.repo_id AND repo_fences.fence_kind = 'writer_session' AND status IN ('active','expired','recovery_required') AND released_at IS NULL AND recovered_at IS NULL FOR UPDATE" +
		"), updated_operation AS (" + operationLeaseFencedUpdateBaseSQL() +
		"AND operation_type = 'mount_binding_create' AND phase = 'validate_mount_binding_create' AND mount_binding_id = $14 " +
		"AND namespace_id = $15 AND repo_id = $16 AND resource_type = 'workload_mount_binding' AND resource_id = $14 " +
		"AND input_summary @> jsonb_build_object('mount_binding_id', $14, 'namespace_id', $15, 'repo_id', $16, 'volume_id', $17, 'mount_path', $18, 'read_only', $19, 'lease_seconds', $21) " +
		"AND EXISTS (SELECT 1 FROM active_namespace) AND EXISTS (SELECT 1 FROM active_binding) AND EXISTS (SELECT 1 FROM active_repo) AND EXISTS (SELECT 1 FROM active_volume) " +
		"AND NOT EXISTS (SELECT 1 FROM held_lifecycle_fence) AND ($19 = true OR NOT EXISTS (SELECT 1 FROM held_writer_fence)) " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), inserted_binding AS (" +
		"INSERT INTO workload_mount_bindings (" + strings.Join(workloadMountFullColumns, ", ") + ") SELECT " + placeholders(14, len(workloadMountFullColumns)) + " FROM updated_operation RETURNING " + strings.Join(workloadMountFullColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(31, len(auditOutboxColumns)) + " FROM updated_operation, inserted_binding RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("inserted_binding", workloadMountFullColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) +
		" FROM inserted_binding, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func workloadMountBindingStatusCommitSQL() string {
	return workloadMountBindingUpdateCommitSQL("mount_binding_status_update", "validate_mount_binding_status_update",
		"status = CASE "+
			"WHEN status IN ('released','revoked','expired','failed') THEN status "+
			"WHEN status = 'releasing' AND $15 IN ('pending','active') THEN status "+
			"ELSE $15 END, "+
			"last_observed_at = CASE "+
			"WHEN status IN ('released','revoked','expired','failed') THEN last_observed_at "+
			"WHEN status = 'releasing' AND $15 IN ('pending','active') THEN last_observed_at "+
			"ELSE $17 END, "+
			"lease_expires_at = CASE "+
			"WHEN status IN ('released','revoked','expired','failed') THEN lease_expires_at "+
			"WHEN status = 'releasing' AND $15 IN ('pending','active') THEN lease_expires_at "+
			"WHEN $18::timestamptz IS NOT NULL THEN $18::timestamptz "+
			"ELSE lease_expires_at END, "+
			"terminal_observed_at = CASE WHEN $15 IN ('released','revoked','expired','failed') AND status NOT IN ('released','revoked','expired','failed') THEN $17 ELSE terminal_observed_at END, "+
			"confirmed_unmounted_at = CASE WHEN $15 IN ('released','revoked') AND status NOT IN ('released','revoked','expired','failed') THEN $17 ELSE confirmed_unmounted_at END, "+
			"unable_to_write_at = CASE WHEN $15 IN ('released','revoked','expired','failed') AND status NOT IN ('released','revoked','expired','failed') THEN $17 ELSE unable_to_write_at END, "+
			"status_reason = CASE "+
			"WHEN status IN ('released','revoked','expired','failed') THEN status_reason "+
			"WHEN status = 'releasing' AND $15 IN ('pending','active') THEN status_reason "+
			"ELSE $16 END, ")
}

func workloadMountBindingHeartbeatCommitSQL() string {
	return workloadMountBindingUpdateCommitSQL("mount_binding_heartbeat", "validate_mount_binding_heartbeat",
		"last_heartbeat_at = CASE WHEN status IN ('released','revoked','expired','failed','releasing') THEN last_heartbeat_at ELSE $17 END, "+
			"lease_expires_at = CASE WHEN status IN ('released','revoked','expired','failed','releasing') THEN lease_expires_at ELSE $17 + (lease_seconds || ' seconds')::interval END, ")
}

func workloadMountBindingReleaseCommitSQL() string {
	return workloadMountBindingUpdateCommitSQL("mount_binding_release", "validate_mount_binding_release",
		"status = CASE WHEN status IN ('released','revoked','expired','failed') THEN status ELSE 'releasing' END, "+
			"last_observed_at = $17, status_reason = CASE WHEN status IN ('released','revoked','expired','failed') THEN status_reason ELSE $16 END, ")
}

func workloadMountBindingRevokeCommitSQL() string {
	return workloadMountBindingUpdateCommitSQL("mount_binding_revoke", "validate_mount_binding_revoke",
		"status = CASE WHEN status IN ('released','revoked','expired','failed') THEN status ELSE 'releasing' END, "+
			"last_observed_at = $17, status_reason = CASE WHEN status IN ('released','revoked','expired','failed') THEN status_reason ELSE $16 END, ")
}

func workloadMountBindingUpdateCommitSQL(operationType, phase, setClause string) string {
	return "WITH updated_operation AS (" + operationLeaseFencedUpdateBaseSQL() +
		"AND operation_type = '" + operationType + "' AND phase = '" + phase + "' AND mount_binding_id = $14 " +
		"AND resource_type = 'workload_mount_binding' AND resource_id = $14 " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), updated_binding AS (" +
		"UPDATE workload_mount_bindings SET " + setClause + "updated_at = $11 FROM updated_operation WHERE workload_mount_bindings.mount_binding_id = $14 AND workload_mount_bindings.namespace_id = updated_operation.namespace_id AND workload_mount_bindings.repo_id = updated_operation.repo_id RETURNING " + prefixedColumns("workload_mount_bindings", workloadMountFullColumns) +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(19, len(auditOutboxColumns)) + " FROM updated_operation, updated_binding RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("updated_binding", workloadMountFullColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) +
		" FROM updated_binding, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func scanWorkloadMountBindingFull(row rowScanner) (workloadmount.Binding, error) {
	var binding workloadmount.Binding
	var status string
	var lastHeartbeatAt, lastObservedAt, confirmedUnmountedAt, unableToWriteAt, terminalObservedAt sql.NullTime
	if err := row.Scan(
		&binding.ID,
		&binding.NamespaceID,
		&binding.RepoID,
		&binding.VolumeID,
		&binding.MountPath,
		&binding.ReadOnly,
		&status,
		&binding.LeaseSeconds,
		&binding.LeaseExpiresAt,
		&lastHeartbeatAt,
		&lastObservedAt,
		&confirmedUnmountedAt,
		&unableToWriteAt,
		&terminalObservedAt,
		&binding.StatusReason,
		&binding.CreatedAt,
		&binding.UpdatedAt,
	); err != nil {
		return workloadmount.Binding{}, err
	}
	binding.Status = sessionstate.MountStatus(status)
	binding.LastHeartbeatAt = nullTimePtr(lastHeartbeatAt)
	binding.LastObservedAt = nullTimePtr(lastObservedAt)
	binding.ConfirmedUnmountedAt = nullTimePtr(confirmedUnmountedAt)
	binding.UnableToWriteAt = nullTimePtr(unableToWriteAt)
	binding.TerminalObservedAt = nullTimePtr(terminalObservedAt)
	return binding, nil
}

func scanWorkloadMountBindingAndOperation(row rowScanner) (workloadmount.Binding, operations.OperationRecord, error) {
	var binding workloadmount.Binding
	var status string
	var lastHeartbeatAt, lastObservedAt, confirmedUnmountedAt, unableToWriteAt, terminalObservedAt sql.NullTime
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	dest := []any{
		&binding.ID, &binding.NamespaceID, &binding.RepoID, &binding.VolumeID, &binding.MountPath, &binding.ReadOnly,
		&status, &binding.LeaseSeconds, &binding.LeaseExpiresAt, &lastHeartbeatAt, &lastObservedAt, &confirmedUnmountedAt, &unableToWriteAt, &terminalObservedAt, &binding.StatusReason, &binding.CreatedAt, &binding.UpdatedAt,
		&record.ID, &operationType, &operationState, &record.Phase, &record.Attempt, &leaseOwner, &leaseExpiresAt,
		&record.IdempotencyScope, &record.IdempotencyKey, &requestHash, &record.CorrelationID, &record.CallerService,
		&record.AuthorizedActor.Type, &record.AuthorizedActor.ID, &record.Resource.Type, &record.Resource.ID, &record.NamespaceID,
		&repoID, &templateID, &exportID, &mountBindingID, &sessionFenceID, &externalResourceIDsJSON, &inputSummaryJSON,
		&jvsJSONOutputJSON, &verificationResultJSON, &compensationStatus, &errorJSON, &record.CreatedAt, &startedAt, &finishedAt,
	}
	if err := row.Scan(dest...); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
	}
	binding.Status = sessionstate.MountStatus(status)
	binding.LastHeartbeatAt = nullTimePtr(lastHeartbeatAt)
	binding.LastObservedAt = nullTimePtr(lastObservedAt)
	binding.ConfirmedUnmountedAt = nullTimePtr(confirmedUnmountedAt)
	binding.UnableToWriteAt = nullTimePtr(unableToWriteAt)
	binding.TerminalObservedAt = nullTimePtr(terminalObservedAt)
	if err := binding.Validate(); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, err
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
		return workloadmount.Binding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return workloadmount.Binding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return workloadmount.Binding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal error_json: %w", err)
		}
		record.Error = &opErr
	}
	return binding, record.Sanitized(), nil
}
