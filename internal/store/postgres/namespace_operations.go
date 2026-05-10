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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func (store *Store) CommitNamespaceUpsertWithLease(ctx context.Context, namespace resources.Namespace, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	if err := namespace.Validate(); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	if namespace.Status != resources.NamespaceStatusActive {
		return resources.Namespace{}, operations.OperationRecord{}, operationLeaseInvalidRequest("namespace_status", "namespace upsert commit requires active namespace metadata")
	}
	record := sanitized.Record()
	if err := validateNamespaceUpsertOperationRecord(namespace, record); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	operationID := strings.TrimSpace(record.ID)
	if strings.TrimSpace(event.OperationID) == "" {
		return resources.Namespace{}, operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "missing audit operation id")
	}
	if event.OperationID != operationID {
		return resources.Namespace{}, operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	wantEventType, ok := audit.EventTypeForOperationType(record.Type.String())
	if !ok {
		return resources.Namespace{}, operations.OperationRecord{}, auditOutboxInvalidRequest("event_type", fmt.Sprintf("operation type %q has no audit event type", record.Type))
	}
	if event.Type != wantEventType {
		return resources.Namespace{}, operations.OperationRecord{}, auditOutboxInvalidRequest("event_type", "audit event type must match operation type")
	}
	if err := validateNamespaceUpsertAuditEvent(namespace, record, event); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}

	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}

	args := append(operationArgs, namespaceUpsertStoredPredicateArgs(record)...)
	args = append(args, namespaceArgs(namespace)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, namespaceUpsertOperationCommitWithLeaseSQL(), args...)
	gotNamespace, gotOperation, err := scanNamespaceAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.Namespace{}, operations.OperationRecord{}, operationLeaseUnavailable("namespace upsert commit", operationID, err)
		}
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	return gotNamespace, gotOperation, nil
}

func (store *Store) CommitNamespaceDisableWithLease(ctx context.Context, namespace resources.Namespace, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	if err := namespace.Validate(); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	if namespace.Status != resources.NamespaceStatusDisabled {
		return resources.Namespace{}, operations.OperationRecord{}, operationLeaseInvalidRequest("namespace_status", "namespace disable commit requires disabled namespace metadata")
	}
	record := sanitized.Record()
	if record.Type != operations.OperationNamespaceDisable {
		return resources.Namespace{}, operations.OperationRecord{}, operationLeaseInvalidRequest("operation_type", "operation record must be namespace_disable")
	}
	if record.State != operations.OperationStateSucceeded || record.Phase != operations.OperationPhaseNamespaceDisableCommitted {
		return resources.Namespace{}, operations.OperationRecord{}, operationLeaseInvalidRequest("operation_state", "namespace disable commit requires succeeded committed operation update")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || record.NamespaceID != namespace.ID || record.Resource.Type != "namespace" || record.Resource.ID != namespace.ID {
		return resources.Namespace{}, operations.OperationRecord{}, operationLeaseInvalidRequest("namespace_id", "operation namespace resource must match namespace metadata")
	}
	if strings.TrimSpace(event.OperationID) == "" || event.OperationID != record.ID {
		return resources.Namespace{}, operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeNamespaceDisable || event.Outcome != audit.OutcomeSucceeded {
		return resources.Namespace{}, operations.OperationRecord{}, auditOutboxInvalidRequest("event_type", "namespace disable audit event must be succeeded namespace_disable")
	}
	if err := validateNamespaceUpsertAuditEvent(namespace, record, event); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, namespaceUpsertStoredPredicateArgs(record)...)
	args = append(args, namespaceArgs(namespace)...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, namespaceDisableOperationCommitWithLeaseSQL(), args...)
	gotNamespace, gotOperation, err := scanNamespaceAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.Namespace{}, operations.OperationRecord{}, operationLeaseUnavailable("namespace disable commit", record.ID, err)
		}
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	return gotNamespace, gotOperation, nil
}

func validateNamespaceUpsertOperationRecord(namespace resources.Namespace, record operations.OperationRecord) error {
	if record.Type != operations.OperationNamespaceUpsert {
		return operationLeaseInvalidRequest("operation_type", "operation record must be namespace_upsert")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "namespace upsert commit requires succeeded operation update")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || record.NamespaceID != namespace.ID {
		return operationLeaseInvalidRequest("namespace_id", "operation namespace must match namespace metadata")
	}
	if record.Resource.Type != "namespace" {
		return operationLeaseInvalidRequest("resource_type", "operation resource must be namespace")
	}
	if strings.TrimSpace(record.Resource.ID) == "" || record.Resource.ID != namespace.ID {
		return operationLeaseInvalidRequest("resource_id", "operation resource id must match namespace metadata")
	}
	return nil
}

func validateNamespaceUpsertAuditEvent(namespace resources.Namespace, record operations.OperationRecord, event audit.Event) error {
	if event.Outcome != audit.OutcomeSucceeded {
		return auditOutboxInvalidRequest("outcome", "namespace upsert audit outcome must be succeeded")
	}
	if event.Resource.Type != "namespace" {
		return auditOutboxInvalidRequest("resource_type", "namespace upsert audit resource must be namespace")
	}
	if strings.TrimSpace(event.Resource.ID) == "" || event.Resource.ID != namespace.ID {
		return auditOutboxInvalidRequest("resource_id", "namespace upsert audit resource id must match namespace metadata")
	}
	if strings.TrimSpace(event.Resource.NamespaceID) != "" && event.Resource.NamespaceID != namespace.ID {
		return auditOutboxInvalidRequest("resource_namespace_id", "namespace upsert audit resource namespace must match namespace metadata")
	}
	if event.CallerService != record.CallerService {
		return auditOutboxInvalidRequest("caller_service", "audit caller service must match operation")
	}
	if event.CorrelationID != record.CorrelationID {
		return auditOutboxInvalidRequest("correlation_id", "audit correlation id must match operation")
	}
	if event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return auditOutboxInvalidRequest("authorized_actor", "audit actor must match operation")
	}
	return nil
}

func namespaceUpsertOperationCommitWithLeaseSQL() string {
	return "WITH updated_operation AS (" +
		namespaceUpsertOperationLeaseFencedUpdateBaseSQL() +
		"RETURNING " + operationReturningColumnsSQL() +
		"), upserted_namespace AS (" +
		"INSERT INTO namespaces (" + strings.Join(namespaceColumns, ", ") + ") " +
		"SELECT " + placeholders(19, len(namespaceColumns)) + " FROM updated_operation " +
		"ON CONFLICT (namespace_id) DO UPDATE SET " +
		"status = CASE WHEN namespaces.status = 'disabled' THEN namespaces.status ELSE EXCLUDED.status END, " +
		"disabled_reason = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_reason ELSE EXCLUDED.disabled_reason END, " +
		"disabled_at = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_at ELSE EXCLUDED.disabled_at END, " +
		"updated_at = EXCLUDED.updated_at " +
		"RETURNING " + strings.Join(namespaceColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(25, len(auditOutboxColumns)) + " FROM updated_operation, upserted_namespace " +
		"RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("upserted_namespace", namespaceColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) +
		" FROM upserted_namespace, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func namespaceDisableOperationCommitWithLeaseSQL() string {
	return "WITH updated_operation AS (" +
		namespaceDisableOperationLeaseFencedUpdateBaseSQL() +
		"RETURNING " + operationReturningColumnsSQL() +
		"), disabled_namespace AS (" +
		"UPDATE namespaces SET status = $20, disabled_reason = $21, disabled_at = $22, updated_at = $24 " +
		"FROM updated_operation WHERE namespaces.namespace_id = $19 AND namespaces.status = 'active' " +
		"RETURNING " + prefixedColumns("namespaces", namespaceColumns) +
		"), revoking_exports AS (" +
		"UPDATE export_sessions SET status = CASE WHEN status IN ('revoked','expired','failed') THEN status ELSE 'revoking' END, " +
		"revoked_at = CASE WHEN status IN ('revoked','expired','failed') THEN revoked_at ELSE COALESCE(revoked_at, $22) END, " +
		"status_reason = CASE WHEN status IN ('revoked','expired','failed') THEN status_reason WHEN btrim(status_reason) = '' THEN 'namespace_disabled' ELSE status_reason END, " +
		"updated_at = $24 FROM disabled_namespace WHERE export_sessions.namespace_id = disabled_namespace.namespace_id AND export_sessions.status IN ('active','revoking') RETURNING export_id" +
		"), releasing_mounts AS (" +
		"UPDATE workload_mount_bindings SET status = CASE WHEN status IN ('released','revoked','expired','failed') THEN status ELSE 'releasing' END, " +
		"status_reason = CASE WHEN status IN ('released','revoked','expired','failed') THEN status_reason WHEN btrim(status_reason) = '' THEN 'namespace_disabled' ELSE status_reason END, " +
		"confirmed_unmounted_at = confirmed_unmounted_at, terminal_observed_at = terminal_observed_at, unable_to_write_at = unable_to_write_at, updated_at = $24 " +
		"FROM disabled_namespace WHERE workload_mount_bindings.namespace_id = disabled_namespace.namespace_id AND workload_mount_bindings.status IN ('issued','pending','active','releasing') RETURNING mount_binding_id" +
		"), namespace_disable_effects AS (" +
		"SELECT (SELECT count(*) FROM revoking_exports) AS export_count, (SELECT count(*) FROM releasing_mounts) AS mount_count" +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(25, len(auditOutboxColumns)) + " FROM updated_operation, disabled_namespace, namespace_disable_effects " +
		"RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("disabled_namespace", namespaceColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) +
		" FROM disabled_namespace, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func namespaceDisableOperationLeaseFencedUpdateBaseSQL() string {
	return operationLeaseFencedUpdateBaseSQL() +
		"AND operation_type = 'namespace_disable' " +
		"AND phase = 'validate_namespace_disable' " +
		"AND namespace_id = $14 " +
		"AND resource_type = 'namespace' " +
		"AND resource_id = $14 " +
		"AND caller_service = $15 " +
		"AND correlation_id = $16 " +
		"AND authorized_actor_type = $17 " +
		"AND authorized_actor_id = $18 "
}

func namespaceUpsertOperationLeaseFencedUpdateBaseSQL() string {
	return operationLeaseFencedUpdateBaseSQL() +
		"AND operation_type = 'namespace_upsert' " +
		"AND namespace_id = $14 " +
		"AND resource_type = 'namespace' " +
		"AND resource_id = $14 " +
		"AND caller_service = $15 " +
		"AND correlation_id = $16 " +
		"AND authorized_actor_type = $17 " +
		"AND authorized_actor_id = $18 "
}

func namespaceUpsertStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{
		record.NamespaceID,
		record.CallerService,
		record.CorrelationID,
		record.AuthorizedActor.Type,
		record.AuthorizedActor.ID,
	}
}

func scanNamespaceAndOperation(row rowScanner) (resources.Namespace, operations.OperationRecord, error) {
	var namespace resources.Namespace
	var status string
	var disabledReason sql.NullString
	var disabledAt sql.NullTime
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	dest := []any{
		&namespace.ID,
		&status,
		&disabledReason,
		&disabledAt,
		&namespace.CreatedAt,
		&namespace.UpdatedAt,
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
	if err := row.Scan(dest...); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
	}
	namespace.Status = resources.NamespaceStatus(status)
	namespace.DisabledReason = nullStringValue(disabledReason)
	namespace.DisabledAt = nullTimePtr(disabledAt)
	if err := namespace.Validate(); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, err
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
		return resources.Namespace{}, operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return resources.Namespace{}, operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return resources.Namespace{}, operations.OperationRecord{}, fmt.Errorf("unmarshal error_json: %w", err)
		}
		record.Error = &opErr
	}
	return namespace, record.Sanitized(), nil
}

func prefixedColumns(prefix string, columns []string) string {
	out := make([]string, len(columns))
	for idx, column := range columns {
		out[idx] = prefix + "." + column
	}
	return strings.Join(out, ", ")
}
