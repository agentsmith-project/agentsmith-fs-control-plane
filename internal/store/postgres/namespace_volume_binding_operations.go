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

func (store *Store) CommitNamespaceVolumeBindingPutWithLease(ctx context.Context, binding resources.NamespaceVolumeBinding, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.NamespaceVolumeBinding, operations.OperationRecord, error) {
	if err := binding.Validate(); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}
	record := sanitized.Record()
	if err := validateNamespaceVolumeBindingPutOperationRecord(binding, record); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}
	operationID := strings.TrimSpace(record.ID)
	if strings.TrimSpace(event.OperationID) == "" {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "missing audit operation id")
	}
	if event.OperationID != operationID {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	wantEventType, ok := audit.EventTypeForOperationType(record.Type.String())
	if !ok {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, auditOutboxInvalidRequest("event_type", fmt.Sprintf("operation type %q has no audit event type", record.Type))
	}
	if event.Type != wantEventType {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, auditOutboxInvalidRequest("event_type", "audit event type must match operation type")
	}
	if err := validateNamespaceVolumeBindingPutAuditEvent(binding, record, event); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}

	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}
	bindingArgs, err := namespaceVolumeBindingArgs(binding)
	if err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}

	args := append(operationArgs, namespaceVolumeBindingPutStoredPredicateArgs(record)...)
	args = append(args, bindingArgs...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, namespaceVolumeBindingPutOperationCommitWithLeaseSQL(), args...)
	gotBinding, gotOperation, err := scanNamespaceVolumeBindingAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, operationLeaseUnavailable("namespace volume binding put commit", operationID, err)
		}
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}
	return gotBinding, gotOperation, nil
}

func validateNamespaceVolumeBindingPutOperationRecord(binding resources.NamespaceVolumeBinding, record operations.OperationRecord) error {
	if record.Type != operations.OperationNamespaceVolumeBindingPut {
		return operationLeaseInvalidRequest("operation_type", "operation record must be namespace_volume_binding_put")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "namespace volume binding put commit requires succeeded operation update")
	}
	if strings.TrimSpace(record.Phase) != operations.OperationPhaseNamespaceVolumeBindingPutCommitted {
		return operationLeaseInvalidRequest("phase", "namespace volume binding put commit requires committed terminal phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || record.NamespaceID != binding.NamespaceID {
		return operationLeaseInvalidRequest("namespace_id", "operation namespace must match binding metadata")
	}
	if record.Resource.Type != "namespace_volume_binding" {
		return operationLeaseInvalidRequest("resource_type", "operation resource must be namespace_volume_binding")
	}
	if strings.TrimSpace(record.Resource.ID) == "" || record.Resource.ID != binding.NamespaceID {
		return operationLeaseInvalidRequest("resource_id", "operation resource id must match binding namespace")
	}
	return nil
}

func validateNamespaceVolumeBindingPutAuditEvent(binding resources.NamespaceVolumeBinding, record operations.OperationRecord, event audit.Event) error {
	if event.Outcome != audit.OutcomeSucceeded {
		return auditOutboxInvalidRequest("outcome", "namespace volume binding put audit outcome must be succeeded")
	}
	if event.Resource.Type != "namespace_volume_binding" {
		return auditOutboxInvalidRequest("resource_type", "namespace volume binding put audit resource must be namespace_volume_binding")
	}
	if strings.TrimSpace(event.Resource.ID) == "" || event.Resource.ID != binding.NamespaceID {
		return auditOutboxInvalidRequest("resource_id", "namespace volume binding put audit resource id must match binding namespace")
	}
	if strings.TrimSpace(event.Resource.NamespaceID) == "" || event.Resource.NamespaceID != binding.NamespaceID {
		return auditOutboxInvalidRequest("resource_namespace_id", "namespace volume binding put audit resource namespace must match binding metadata")
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

func namespaceVolumeBindingPutOperationCommitWithLeaseSQL() string {
	return "WITH active_namespace AS (" +
		"SELECT namespace_id FROM namespaces WHERE namespace_id = $14 AND status = 'active'" +
		"), active_volume AS (" +
		"SELECT volume_id FROM volumes WHERE volume_id = $20 AND status = 'active'" +
		"), updated_operation AS (" +
		namespaceVolumeBindingPutOperationLeaseFencedUpdateBaseSQL() +
		"RETURNING " + operationReturningColumnsSQL() +
		"), upserted_binding AS (" +
		"INSERT INTO namespace_volume_bindings (" + strings.Join(namespaceVolumeBindingColumns, ", ") + ") " +
		"SELECT " + placeholders(19, len(namespaceVolumeBindingColumns)) + " FROM updated_operation, active_namespace, active_volume " +
		"ON CONFLICT (namespace_id) DO UPDATE SET " +
		"default_volume_id = EXCLUDED.default_volume_id, " +
		"allowed_callers = EXCLUDED.allowed_callers, " +
		"quota_bytes_default = EXCLUDED.quota_bytes_default, " +
		"export_policy = EXCLUDED.export_policy, " +
		"lifecycle_policy = EXCLUDED.lifecycle_policy, " +
		"mount_policy = EXCLUDED.mount_policy, " +
		"template_policy = EXCLUDED.template_policy, " +
		"status = EXCLUDED.status, " +
		"updated_at = EXCLUDED.updated_at " +
		"RETURNING " + strings.Join(namespaceVolumeBindingColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(30, len(auditOutboxColumns)) + " FROM updated_operation, upserted_binding " +
		"RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("upserted_binding", namespaceVolumeBindingColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) +
		" FROM upserted_binding, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func namespaceVolumeBindingPutOperationLeaseFencedUpdateBaseSQL() string {
	return operationLeaseFencedUpdateBaseSQL() +
		"AND EXISTS (SELECT 1 FROM active_namespace) " +
		"AND EXISTS (SELECT 1 FROM active_volume) " +
		"AND operation_type = 'namespace_volume_binding_put' " +
		"AND phase = 'validate_namespace_volume_binding_put' " +
		"AND namespace_id = $14 " +
		"AND resource_type = 'namespace_volume_binding' " +
		"AND resource_id = $14 " +
		"AND caller_service = $15 " +
		"AND correlation_id = $16 " +
		"AND authorized_actor_type = $17 " +
		"AND authorized_actor_id = $18 "
}

func namespaceVolumeBindingPutStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{
		record.NamespaceID,
		record.CallerService,
		record.CorrelationID,
		record.AuthorizedActor.Type,
		record.AuthorizedActor.ID,
	}
}

func scanNamespaceVolumeBindingAndOperation(row rowScanner) (resources.NamespaceVolumeBinding, operations.OperationRecord, error) {
	var binding resources.NamespaceVolumeBinding
	var bindingStatus string
	var allowedCallersJSON, exportPolicyJSON, lifecyclePolicyJSON, mountPolicyJSON, templatePolicyJSON []byte
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	dest := []any{
		&binding.NamespaceID,
		&binding.DefaultVolumeID,
		&allowedCallersJSON,
		&binding.QuotaBytesDefault,
		&exportPolicyJSON,
		&lifecyclePolicyJSON,
		&mountPolicyJSON,
		&templatePolicyJSON,
		&bindingStatus,
		&binding.CreatedAt,
		&binding.UpdatedAt,
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
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
	}
	if err := json.Unmarshal(allowedCallersJSON, &binding.AllowedCallers); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal allowed_callers: %w", err)
	}
	if err := unmarshalObject(exportPolicyJSON, &binding.ExportPolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal export_policy: %w", err)
	}
	if err := unmarshalObject(lifecyclePolicyJSON, &binding.LifecyclePolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal lifecycle_policy: %w", err)
	}
	if err := unmarshalObject(mountPolicyJSON, &binding.MountPolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal mount_policy: %w", err)
	}
	if err := unmarshalObject(templatePolicyJSON, &binding.TemplatePolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal template_policy: %w", err)
	}
	binding.Status = resources.NamespaceStatus(bindingStatus)
	if err := binding.Validate(); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, err
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
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, fmt.Errorf("unmarshal error_json: %w", err)
		}
		record.Error = &opErr
	}
	return binding, record.Sanitized(), nil
}
