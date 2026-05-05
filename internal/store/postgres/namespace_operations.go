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
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
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
