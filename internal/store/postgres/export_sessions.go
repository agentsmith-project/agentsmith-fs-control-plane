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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

var exportSessionPublicColumns = []string{
	"export_id",
	"namespace_id",
	"repo_id",
	"protocol",
	"access_mode",
	"status",
	"expires_at",
	"created_by_caller_service",
	"created_by_actor_type",
	"created_by_actor_id",
	"revoked_at",
	"last_accessed_at",
	"active_request_count",
	"active_write_count",
	"last_observed_at",
	"last_gateway_heartbeat_at",
	"gateway_heartbeat_expires_at",
	"write_drained_at",
	"terminal_observed_at",
	"status_reason",
	"created_at",
	"updated_at",
}

var exportSessionPersistColumns = append(append([]string(nil), exportSessionPublicColumns...),
	"verifier_algorithm",
	"verifier_hash",
	"verifier_salt",
)

func (store *Store) CreateOrReuseExport(ctx context.Context, request exportaccess.CreateRequest) (exportaccess.CreateResult, error) {
	if err := request.Session.Validate(); err != nil {
		return exportaccess.CreateResult{}, err
	}
	if err := request.Verifier.Validate(); err != nil {
		return exportaccess.CreateResult{}, err
	}
	record := request.Operation.SanitizedForPersistence().Record()
	if err := validateExportOperationRecord(record, operations.OperationExportCreate, operations.OperationPhaseExportCreateCommitted, request.Session.ID); err != nil {
		return exportaccess.CreateResult{}, err
	}
	if record.NamespaceID != request.Session.NamespaceID || record.RepoID != request.Session.RepoID {
		return exportaccess.CreateResult{}, operationLeaseInvalidRequest("session", "operation metadata must match export session")
	}
	if err := validateExportAuditEvent(record, request.Audit, audit.EventTypeExportCreate); err != nil {
		return exportaccess.CreateResult{}, err
	}
	args, err := exportCreateArgs(request, record)
	if err != nil {
		return exportaccess.CreateResult{}, err
	}
	var inserted bool
	row := store.exec.QueryRowContext(ctx, exportCreateOrReuseSQL(), args...)
	session, operation, err := scanExportSessionAndOperationWithInserted(row, &inserted)
	if errors.Is(err, sql.ErrNoRows) {
		inserted = false
		row = store.exec.QueryRowContext(ctx, exportCreateReplaySQL(), args...)
		session, operation, err = scanExportSessionAndOperationWithInserted(row, &inserted)
	}
	if err != nil {
		return exportaccess.CreateResult{}, err
	}
	if !inserted && operation.RequestHash != record.RequestHash {
		return exportaccess.CreateResult{}, fmt.Errorf("%w: export_create scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, record.IdempotencyScope)
	}
	return exportaccess.CreateResult{Session: session, Operation: operation, Reused: !inserted}, nil
}

func (store *Store) GetExportSession(ctx context.Context, exportID string) (exportaccess.Session, error) {
	exportID = strings.TrimSpace(exportID)
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return exportaccess.Session{}, err
	}
	row := store.exec.QueryRowContext(ctx, exportSessionPublicSelectSQL()+" WHERE export_id = $1", exportID)
	return scanExportAccessSession(row)
}

func (store *Store) RevokeExport(ctx context.Context, request exportaccess.RevokeRequest) (exportaccess.RevokeResult, error) {
	if err := pathresolver.ValidateID(pathresolver.ExportID, strings.TrimSpace(request.ExportID)); err != nil {
		return exportaccess.RevokeResult{}, err
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, strings.TrimSpace(request.NamespaceID)); err != nil {
		return exportaccess.RevokeResult{}, err
	}
	if request.Now.IsZero() {
		return exportaccess.RevokeResult{}, operationLeaseInvalidRequest("now", "revoke time must be set")
	}
	record := request.Operation.SanitizedForPersistence().Record()
	if err := validateExportOperationRecord(record, operations.OperationExportRevoke, operations.OperationPhaseExportRevokeCommitted, request.ExportID); err != nil {
		return exportaccess.RevokeResult{}, err
	}
	if record.NamespaceID != request.NamespaceID {
		return exportaccess.RevokeResult{}, operationLeaseInvalidRequest("namespace_id", "operation namespace must match revoke request")
	}
	if err := validateExportAuditEvent(record, request.Audit, audit.EventTypeExportRevoke); err != nil {
		return exportaccess.RevokeResult{}, err
	}
	args, err := exportRevokeArgs(request, record)
	if err != nil {
		return exportaccess.RevokeResult{}, err
	}
	var inserted bool
	row := store.exec.QueryRowContext(ctx, exportRevokeSQL(), args...)
	session, operation, err := scanExportSessionAndOperationWithInserted(row, &inserted)
	if err != nil {
		return exportaccess.RevokeResult{}, err
	}
	if !inserted && operation.RequestHash != record.RequestHash {
		return exportaccess.RevokeResult{}, fmt.Errorf("%w: export_revoke scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, record.IdempotencyScope)
	}
	return exportaccess.RevokeResult{Session: session, Operation: operation, Reused: !inserted}, nil
}

func (store *Store) GetExportGatewayCredential(ctx context.Context, exportID string) (exportaccess.GatewayCredential, error) {
	exportID = strings.TrimSpace(exportID)
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return exportaccess.GatewayCredential{}, err
	}
	row := store.exec.QueryRowContext(ctx, exportGatewayCredentialSQL(), exportID)
	var credential exportaccess.GatewayCredential
	var algorithm string
	if err := scanExportSessionPrefix(row, &credential.Session, &algorithm, &credential.Verifier.Hash, &credential.Verifier.Salt, &credential.VolumeID, &credential.PayloadVolumeSubdir); err != nil {
		return exportaccess.GatewayCredential{}, err
	}
	credential.Verifier.Algorithm = algorithm
	if err := credential.Verifier.Validate(); err != nil {
		return exportaccess.GatewayCredential{}, err
	}
	return credential, nil
}

func (store *Store) RecordExportAccess(ctx context.Context, exportID string, accessedAt time.Time) error {
	exportID = strings.TrimSpace(exportID)
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return err
	}
	if accessedAt.IsZero() {
		return operationLeaseInvalidRequest("accessed_at", "access time must be set")
	}
	_, err := store.exec.ExecContext(ctx, "UPDATE export_sessions SET last_accessed_at = GREATEST(COALESCE(last_accessed_at, $2), $2), updated_at = $2 WHERE export_id = $1", exportID, accessedAt.UTC())
	return err
}

func (store *Store) RecordExportRuntimeObservation(ctx context.Context, observation exportaccess.RuntimeObservation) (exportaccess.Session, error) {
	observation.ExportID = strings.TrimSpace(observation.ExportID)
	if err := pathresolver.ValidateID(pathresolver.ExportID, observation.ExportID); err != nil {
		return exportaccess.Session{}, err
	}
	if observation.ObservedAt.IsZero() {
		return exportaccess.Session{}, operationLeaseInvalidRequest("observed_at", "runtime observation time must be set")
	}
	if observation.ActiveWriteDelta > 0 && observation.ActiveWriteDelta > observation.ActiveRequestDelta {
		return exportaccess.Session{}, operationLeaseInvalidRequest("active_deltas", "positive active_write_delta requires matching active_request_delta")
	}
	row := store.exec.QueryRowContext(ctx, exportRuntimeObservationSQL(),
		observation.ExportID,
		observation.ActiveRequestDelta,
		observation.ActiveWriteDelta,
		observation.ObservedAt.UTC(),
		timePtrArg(observation.GatewayHeartbeatAt),
		timePtrArg(observation.GatewayHeartbeatExpiresAt),
		timePtrArg(observation.SuccessfulRequestAccessedAt),
	)
	return scanExportAccessSession(row)
}

func (store *Store) ListExportSessionsForTerminalReconcile(ctx context.Context, now time.Time, limit int) (sessions []exportaccess.Session, err error) {
	if now.IsZero() {
		return nil, operationLeaseInvalidRequest("now", "terminal reconcile list time must be set")
	}
	if limit <= 0 {
		return nil, operationLeaseInvalidRequest("limit", "terminal reconcile list limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, exportTerminalReconcileListSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		session, err := scanExportAccessSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (store *Store) ReconcileExportSessionTerminal(ctx context.Context, request exportaccess.ReconcileRequest) (exportaccess.ReconcileResult, error) {
	request.ExportID = strings.TrimSpace(request.ExportID)
	request.NamespaceID = strings.TrimSpace(request.NamespaceID)
	if err := pathresolver.ValidateID(pathresolver.ExportID, request.ExportID); err != nil {
		return exportaccess.ReconcileResult{}, err
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, request.NamespaceID); err != nil {
		return exportaccess.ReconcileResult{}, err
	}
	if request.ObservedAt.IsZero() {
		return exportaccess.ReconcileResult{}, operationLeaseInvalidRequest("observed_at", "terminal observation time must be set")
	}
	if request.ActiveRequestCount != 0 || request.ActiveWriteCount != 0 {
		return exportaccess.ReconcileResult{}, operationLeaseInvalidRequest("active_counts", "terminal reconcile requires zero active counts")
	}
	switch request.TargetStatus {
	case sessionstate.ExportStatusRevoked, sessionstate.ExportStatusExpired:
	case sessionstate.ExportStatusFailed:
		if strings.TrimSpace(request.StatusReason) == "" {
			return exportaccess.ReconcileResult{}, operationLeaseInvalidRequest("status_reason", "failed export reconcile requires a reason")
		}
	default:
		return exportaccess.ReconcileResult{}, operationLeaseInvalidRequest("status", "export terminal reconcile requires revoked, expired, or failed")
	}
	record := request.Operation.SanitizedForPersistence().Record()
	if err := validateExportOperationRecord(record, operations.OperationExportSessionReconcile, operations.OperationPhaseExportSessionReconcileCommitted, request.ExportID); err != nil {
		return exportaccess.ReconcileResult{}, err
	}
	if record.NamespaceID != request.NamespaceID {
		return exportaccess.ReconcileResult{}, operationLeaseInvalidRequest("namespace_id", "operation namespace must match reconcile request")
	}
	if err := validateExportAuditEvent(record, request.Audit, audit.EventTypeExportSessionReconcile); err != nil {
		return exportaccess.ReconcileResult{}, err
	}
	args, err := exportReconcileArgs(request, record)
	if err != nil {
		return exportaccess.ReconcileResult{}, err
	}
	var inserted bool
	row := store.exec.QueryRowContext(ctx, exportReconcileTerminalSQL(), args...)
	session, operation, err := scanExportSessionAndOperationWithInserted(row, &inserted)
	if err != nil {
		return exportaccess.ReconcileResult{}, err
	}
	if !inserted && operation.RequestHash != record.RequestHash {
		return exportaccess.ReconcileResult{}, fmt.Errorf("%w: export_session_reconcile scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, record.IdempotencyScope)
	}
	return exportaccess.ReconcileResult{Session: session, Operation: operation, Reused: !inserted}, nil
}

// MarkExportTerminal is retained for legacy callers/tests only. GA terminal
// reconciliation must use ReconcileExportSessionTerminal so the operation,
// terminal session update, and audit outbox event commit atomically.
func (store *Store) MarkExportTerminal(ctx context.Context, exportID string, status sessionstate.ExportStatus, observedAt time.Time) (exportaccess.Session, error) {
	exportID = strings.TrimSpace(exportID)
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return exportaccess.Session{}, err
	}
	if observedAt.IsZero() {
		return exportaccess.Session{}, operationLeaseInvalidRequest("observed_at", "terminal observation time must be set")
	}
	switch status {
	case sessionstate.ExportStatusRevoked, sessionstate.ExportStatusExpired, sessionstate.ExportStatusFailed:
	default:
		return exportaccess.Session{}, operationLeaseInvalidRequest("status", "export terminal reconcile requires revoked, expired, or failed")
	}
	row := store.exec.QueryRowContext(ctx, exportTerminalSQL(), exportID, string(status), observedAt.UTC())
	return scanExportAccessSession(row)
}

func exportCreateArgs(request exportaccess.CreateRequest, record operations.OperationRecord) ([]any, error) {
	operationArgs, err := operationInsertArgs(record)
	if err != nil {
		return nil, err
	}
	outboxRecord, err := audit.NewOutboxRecord(request.Audit, record.CreatedAt)
	if err != nil {
		return nil, err
	}
	args := append(operationArgs, exportSessionPersistArgs(request.Session, request.Verifier)...)
	return append(args, auditOutboxInsertArgs(outboxRecord)...), nil
}

func exportRevokeArgs(request exportaccess.RevokeRequest, record operations.OperationRecord) ([]any, error) {
	operationArgs, err := operationInsertArgs(record)
	if err != nil {
		return nil, err
	}
	outboxRecord, err := audit.NewOutboxRecord(request.Audit, request.Now)
	if err != nil {
		return nil, err
	}
	args := append(operationArgs, request.ExportID, request.NamespaceID, request.Now.UTC())
	return append(args, auditOutboxInsertArgs(outboxRecord)...), nil
}

func exportReconcileArgs(request exportaccess.ReconcileRequest, record operations.OperationRecord) ([]any, error) {
	operationArgs, err := operationInsertArgs(record)
	if err != nil {
		return nil, err
	}
	outboxRecord, err := audit.NewOutboxRecord(request.Audit, request.ObservedAt)
	if err != nil {
		return nil, err
	}
	args := append(operationArgs,
		string(request.TargetStatus),
		request.ExportID,
		request.ObservedAt.UTC(),
		strings.TrimSpace(request.StatusReason),
		request.ActiveRequestCount,
		request.ActiveWriteCount,
	)
	return append(args, auditOutboxInsertArgs(outboxRecord)...), nil
}

func exportSessionPersistArgs(session exportaccess.Session, verifier exportaccess.PasswordVerifier) []any {
	return []any{
		session.ID,
		session.NamespaceID,
		session.RepoID,
		string(session.Protocol),
		string(session.Mode),
		string(session.Status),
		session.ExpiresAt,
		session.CreatedByCallerService,
		session.CreatedByActor.Type,
		session.CreatedByActor.ID,
		timePtrArg(session.RevokedAt),
		timePtrArg(session.LastAccessedAt),
		session.ActiveRequestCount,
		session.ActiveWriteCount,
		timePtrArg(session.LastObservedAt),
		timePtrArg(session.LastGatewayHeartbeatAt),
		timePtrArg(session.GatewayHeartbeatExpiresAt),
		timePtrArg(session.WriteDrainedAt),
		timePtrArg(session.TerminalObservedAt),
		session.StatusReason,
		session.CreatedAt,
		session.UpdatedAt,
		verifier.Algorithm,
		verifier.Hash,
		verifier.Salt,
	}
}

func validateExportOperationRecord(record operations.OperationRecord, typ operations.OperationType, phase, exportID string) error {
	if record.Type != typ {
		return operationLeaseInvalidRequest("operation_type", "operation type does not match export commit")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "export commit requires succeeded operation update")
	}
	if strings.TrimSpace(record.Phase) != phase {
		return operationLeaseInvalidRequest("phase", "export commit requires committed phase")
	}
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return err
	}
	if record.ExportID != exportID || record.Resource.Type != "export" || record.Resource.ID != exportID {
		return operationLeaseInvalidRequest("export_id", "operation must target export")
	}
	return nil
}

func validateExportAuditEvent(record operations.OperationRecord, event audit.Event, eventType audit.EventType) error {
	if event.OperationID != record.ID {
		return auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != eventType {
		return auditOutboxInvalidRequest("event_type", "export audit event must match operation type")
	}
	if event.Outcome != audit.OutcomeSucceeded {
		return auditOutboxInvalidRequest("outcome", "export audit outcome must be succeeded")
	}
	if event.Resource.Type != "export" || event.Resource.ID != record.ExportID || event.Resource.NamespaceID != record.NamespaceID {
		return auditOutboxInvalidRequest("resource", "export audit resource must match operation")
	}
	if event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("caller", "export audit caller context must match operation")
	}
	return nil
}

func exportSessionPublicSelectSQL() string {
	return "SELECT " + strings.Join(exportSessionPublicColumns, ", ") + " FROM export_sessions"
}

func exportCreateOrReuseSQL() string {
	auditPlaceholderStart := len(operationColumns) + len(exportSessionPersistColumns) + 1
	return "WITH existing_operation AS (" +
		"SELECT " + prefixedColumns("operations", operationSelectColumns) + ", false AS inserted FROM operations WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'export_create' AND idempotency_key = $9" +
		"), existing_session AS (" +
		"SELECT " + prefixedColumns("export_sessions", exportSessionPublicColumns) + " FROM export_sessions JOIN existing_operation ON export_sessions.export_id = existing_operation.export_id AND export_sessions.namespace_id = existing_operation.namespace_id AND export_sessions.repo_id = existing_operation.repo_id" +
		"), active_namespace AS (" +
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $17 AND status = 'active' FOR SHARE" +
		"), active_binding AS (" +
		"SELECT namespace_id FROM namespace_volume_bindings WHERE namespace_id = $17 AND status = 'active' AND COALESCE((export_policy->>'webdav_enabled')::boolean, false) = true FOR SHARE" +
		"), active_repo AS (" +
		"SELECT repo_id FROM repos WHERE namespace_id = $17 AND repo_id = $18 AND repo_kind = 'repo' AND status = 'active' AND lifecycle_status = 'active' FOR UPDATE" +
		"), active_volume AS (" +
		"SELECT volumes.volume_id FROM volumes JOIN repos ON repos.volume_id = volumes.volume_id WHERE repos.namespace_id = $17 AND repos.repo_id = $18 AND volumes.status = 'active' AND COALESCE((volumes.capabilities->>'webdav_export')::boolean, false) = true FOR SHARE" +
		"), held_lifecycle_fence AS (" +
		"SELECT fence_id FROM repo_fences, active_repo WHERE repo_fences.repo_id = active_repo.repo_id AND repo_fences.fence_kind = 'lifecycle' AND status IN ('active','expired','recovery_required') AND released_at IS NULL AND recovered_at IS NULL FOR UPDATE" +
		"), held_writer_fence AS (" +
		"SELECT fence_id FROM repo_fences, active_repo WHERE repo_fences.repo_id = active_repo.repo_id AND repo_fences.fence_kind = 'writer_session' AND status IN ('active','expired','recovery_required') AND released_at IS NULL AND recovered_at IS NULL FOR UPDATE" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") SELECT " + placeholders(1, len(operationColumns)) + " WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND $3 = 'succeeded' AND $2 = 'export_create' AND $4 = 'export_create_committed' AND $15 = 'export' AND $16 = $20 AND $20 = $33 AND $17 = $34 AND $18 = $35 " +
		"AND EXISTS (SELECT 1 FROM active_namespace) AND EXISTS (SELECT 1 FROM active_binding) AND EXISTS (SELECT 1 FROM active_repo) AND EXISTS (SELECT 1 FROM active_volume) " +
		"AND NOT EXISTS (SELECT 1 FROM held_lifecycle_fence) AND ($37 = 'read_only' OR NOT EXISTS (SELECT 1 FROM held_writer_fence)) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") + ", (xmax = 0) AS inserted" +
		"), inserted_session AS (" +
		"INSERT INTO export_sessions (" + strings.Join(exportSessionPersistColumns, ", ") + ") SELECT " + placeholders(33, len(exportSessionPersistColumns)) + " FROM inserted_operation WHERE inserted_operation.inserted RETURNING " + strings.Join(exportSessionPublicColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(auditPlaceholderStart, len(auditOutboxColumns)) + " FROM inserted_operation, inserted_session RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("existing_session", exportSessionPublicColumns) + ", " + prefixedColumns("existing_operation", operationSelectColumns) + ", inserted FROM existing_session, existing_operation " +
		"UNION ALL SELECT " + prefixedColumns("inserted_session", exportSessionPublicColumns) + ", " + prefixedColumns("inserted_operation", operationSelectColumns) + ", inserted FROM inserted_session, inserted_operation WHERE EXISTS (SELECT 1 FROM inserted_audit) LIMIT 1"
}

func exportCreateReplaySQL() string {
	return "SELECT " + prefixedColumns("export_sessions", exportSessionPublicColumns) + ", " + prefixedColumns("operations", operationSelectColumns) + ", false AS inserted " +
		"FROM operations JOIN export_sessions ON export_sessions.export_id = operations.export_id AND export_sessions.namespace_id = operations.namespace_id AND export_sessions.repo_id = operations.repo_id " +
		"WHERE operations.caller_service = $12 AND operations.namespace_id = $17 AND operations.operation_type = 'export_create' AND operations.idempotency_key = $9 LIMIT 1"
}

func exportRevokeSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + prefixedColumns("operations", operationSelectColumns) + ", false AS inserted FROM operations WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'export_revoke' AND idempotency_key = $9" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") SELECT " + placeholders(1, len(operationColumns)) + " WHERE NOT EXISTS (SELECT 1 FROM existing_operation) AND $20 = $33 AND $17 = $34 " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") + ", (xmax = 0) AS inserted" +
		"), updated_session AS (" +
		"UPDATE export_sessions SET status = CASE WHEN status IN ('revoked','expired','failed') THEN status ELSE 'revoking' END, revoked_at = COALESCE(revoked_at, $35), updated_at = $35 " +
		"FROM inserted_operation WHERE export_sessions.export_id = $33 AND export_sessions.namespace_id = $34 RETURNING " + prefixedColumns("export_sessions", exportSessionPublicColumns) +
		"), existing_session AS (" +
		"SELECT " + prefixedColumns("export_sessions", exportSessionPublicColumns) + " FROM export_sessions JOIN existing_operation ON export_sessions.export_id = existing_operation.export_id WHERE export_sessions.namespace_id = $34" +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(36, len(auditOutboxColumns)) + " FROM inserted_operation, updated_session RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("existing_session", exportSessionPublicColumns) + ", " + prefixedColumns("existing_operation", operationSelectColumns) + ", inserted FROM existing_session, existing_operation " +
		"UNION ALL SELECT " + prefixedColumns("updated_session", exportSessionPublicColumns) + ", " + prefixedColumns("inserted_operation", operationSelectColumns) + ", inserted FROM updated_session, inserted_operation WHERE EXISTS (SELECT 1 FROM inserted_audit) LIMIT 1"
}

func exportRuntimeObservationSQL() string {
	return "UPDATE export_sessions SET " +
		"active_request_count = active_request_count + $2, " +
		"active_write_count = active_write_count + $3, " +
		"last_observed_at = GREATEST(COALESCE(last_observed_at, $4), $4), " +
		"last_gateway_heartbeat_at = CASE WHEN $5::timestamptz IS NULL THEN last_gateway_heartbeat_at ELSE GREATEST(COALESCE(last_gateway_heartbeat_at, $5), $5) END, " +
		"gateway_heartbeat_expires_at = CASE WHEN $6::timestamptz IS NULL THEN gateway_heartbeat_expires_at ELSE GREATEST(COALESCE(gateway_heartbeat_expires_at, $6), $6) END, " +
		"write_drained_at = CASE WHEN active_write_count + $3 = 0 THEN GREATEST(COALESCE(write_drained_at, $4), $4) ELSE NULL END, " +
		"last_accessed_at = CASE WHEN $7::timestamptz IS NULL THEN last_accessed_at ELSE GREATEST(COALESCE(last_accessed_at, $7), $7) END, " +
		"updated_at = $4 " +
		"WHERE export_id = $1 " +
		"AND active_request_count + $2 >= 0 AND active_write_count + $3 >= 0 AND active_write_count + $3 <= active_request_count + $2 " +
		"AND (($2 <= 0 AND $3 <= 0) OR (status = 'active' AND expires_at > $4)) " +
		"RETURNING " + strings.Join(exportSessionPublicColumns, ", ")
}

func exportTerminalReconcileListSQL() string {
	return exportSessionPublicSelectSQL() +
		" WHERE active_request_count = 0 AND active_write_count = 0 " +
		"AND (status = 'revoking' OR (status = 'active' AND expires_at <= $1)) " +
		"ORDER BY updated_at, export_id LIMIT $2"
}

func exportReconcileTerminalSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + prefixedColumns("operations", operationSelectColumns) + ", false AS inserted FROM operations WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'export_session_reconcile' AND idempotency_key = $9" +
		"), eligible_session AS (" +
		"SELECT export_id FROM export_sessions WHERE export_id = $34 AND namespace_id = $17 " +
		"AND active_request_count = 0 AND active_write_count = 0 AND $37 = 0 AND $38 = 0 " +
		"AND (($33 = 'revoked' AND status = 'revoking' AND revoked_at IS NOT NULL) OR ($33 = 'expired' AND status = 'active' AND expires_at <= $35) OR ($33 = 'failed' AND btrim($36) <> '')) FOR UPDATE" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") SELECT " + placeholders(1, len(operationColumns)) + " WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND $3 = 'succeeded' AND $2 = 'export_session_reconcile' AND $4 = 'export_session_reconcile_committed' AND $15 = 'export' AND $16 = $20 AND $20 = $34 " +
		"AND EXISTS (SELECT 1 FROM eligible_session) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") + ", (xmax = 0) AS inserted" +
		"), updated_session AS (" +
		"UPDATE export_sessions SET status = $33, terminal_observed_at = $35, active_request_count = 0, active_write_count = 0, write_drained_at = COALESCE(write_drained_at, $35), status_reason = $36, updated_at = $35 " +
		"FROM eligible_session, inserted_operation WHERE inserted_operation.inserted AND export_sessions.export_id = eligible_session.export_id RETURNING " + prefixedColumns("export_sessions", exportSessionPublicColumns) +
		"), existing_session AS (" +
		"SELECT " + prefixedColumns("export_sessions", exportSessionPublicColumns) + " FROM export_sessions JOIN existing_operation ON export_sessions.export_id = existing_operation.export_id WHERE export_sessions.namespace_id = $17" +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") SELECT " + placeholders(39, len(auditOutboxColumns)) + " FROM inserted_operation, updated_session RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("existing_session", exportSessionPublicColumns) + ", " + prefixedColumns("existing_operation", operationSelectColumns) + ", inserted FROM existing_session, existing_operation " +
		"UNION ALL SELECT " + prefixedColumns("updated_session", exportSessionPublicColumns) + ", " + prefixedColumns("inserted_operation", operationSelectColumns) + ", inserted FROM updated_session, inserted_operation WHERE EXISTS (SELECT 1 FROM inserted_audit) LIMIT 1"
}

func exportGatewayCredentialSQL() string {
	return "SELECT " + prefixedColumns("s", exportSessionPublicColumns) + ", s.verifier_algorithm, s.verifier_hash, s.verifier_salt, r.volume_id, r.payload_volume_subdir " +
		"FROM export_sessions s JOIN repos r ON r.namespace_id = s.namespace_id AND r.repo_id = s.repo_id WHERE s.export_id = $1"
}

func exportTerminalSQL() string {
	return "UPDATE export_sessions SET status = $2, terminal_observed_at = $3, updated_at = $3 WHERE export_id = $1 AND status IN ('active','revoking') RETURNING " + strings.Join(exportSessionPublicColumns, ", ")
}

func scanExportAccessSession(row rowScanner) (exportaccess.Session, error) {
	var session exportaccess.Session
	dest, finish := exportSessionScanDest(&session)
	if err := row.Scan(dest...); err != nil {
		return exportaccess.Session{}, err
	}
	finish()
	return session, nil
}

func scanExportSessionAndOperationWithInserted(row rowScanner, inserted *bool) (exportaccess.Session, operations.OperationRecord, error) {
	var session exportaccess.Session
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	var insertedValue bool
	dest, finishSession := exportSessionScanDest(&session)
	dest = append(dest,
		&record.ID, &operationType, &operationState, &record.Phase, &record.Attempt, &leaseOwner, &leaseExpiresAt,
		&record.IdempotencyScope, &record.IdempotencyKey, &requestHash, &record.CorrelationID, &record.CallerService,
		&record.AuthorizedActor.Type, &record.AuthorizedActor.ID, &record.Resource.Type, &record.Resource.ID, &record.NamespaceID,
		&repoID, &templateID, &exportID, &mountBindingID, &sessionFenceID, &externalResourceIDsJSON, &inputSummaryJSON,
		&jvsJSONOutputJSON, &verificationResultJSON, &compensationStatus, &errorJSON, &record.CreatedAt, &startedAt, &finishedAt,
		&insertedValue,
	)
	if err := row.Scan(dest...); err != nil {
		return exportaccess.Session{}, operations.OperationRecord{}, err
	}
	finishSession()
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
		return exportaccess.Session{}, operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return exportaccess.Session{}, operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return exportaccess.Session{}, operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return exportaccess.Session{}, operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := jsonUnmarshalOperationError(errorJSON, &opErr); err != nil {
			return exportaccess.Session{}, operations.OperationRecord{}, err
		}
		record.Error = &opErr
	}
	if inserted != nil {
		*inserted = insertedValue
	}
	return session, record.Sanitized(), nil
}

func scanExportSessionPrefix(row rowScanner, session *exportaccess.Session, extra ...any) error {
	dest, finish := exportSessionScanDest(session)
	dest = append(dest, extra...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	finish()
	return nil
}

func exportSessionScanDest(session *exportaccess.Session) ([]any, func()) {
	var protocol, mode, status string
	var revokedAt, lastAccessedAt, lastObservedAt, lastGatewayHeartbeatAt, gatewayHeartbeatExpiresAt, writeDrainedAt, terminalObservedAt sql.NullTime
	return []any{
			&session.ID,
			&session.NamespaceID,
			&session.RepoID,
			&protocol,
			&mode,
			&status,
			&session.ExpiresAt,
			&session.CreatedByCallerService,
			&session.CreatedByActor.Type,
			&session.CreatedByActor.ID,
			&revokedAt,
			&lastAccessedAt,
			&session.ActiveRequestCount,
			&session.ActiveWriteCount,
			&lastObservedAt,
			&lastGatewayHeartbeatAt,
			&gatewayHeartbeatExpiresAt,
			&writeDrainedAt,
			&terminalObservedAt,
			&session.StatusReason,
			&session.CreatedAt,
			&session.UpdatedAt,
		}, func() {
			if session != nil {
				session.Protocol = exportaccess.Protocol(protocol)
				session.Mode = sessionstate.AccessMode(mode)
				session.Status = sessionstate.ExportStatus(status)
				session.RevokedAt = nullTimePtr(revokedAt)
				session.LastAccessedAt = nullTimePtr(lastAccessedAt)
				session.LastObservedAt = nullTimePtr(lastObservedAt)
				session.LastGatewayHeartbeatAt = nullTimePtr(lastGatewayHeartbeatAt)
				session.GatewayHeartbeatExpiresAt = nullTimePtr(gatewayHeartbeatExpiresAt)
				session.WriteDrainedAt = nullTimePtr(writeDrainedAt)
				session.TerminalObservedAt = nullTimePtr(terminalObservedAt)
			}
		}
}

func jsonUnmarshalOperationError(data []byte, dest *operations.OperationError) error {
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("unmarshal error_json: %w", err)
	}
	return nil
}
