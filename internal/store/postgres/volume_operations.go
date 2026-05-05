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

func (store *Store) CommitVolumeEnsureWithLease(ctx context.Context, volume resources.Volume, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (resources.Volume, operations.OperationRecord, error) {
	if err := volume.Validate(); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	record := sanitized.Record()
	if err := validateVolumeEnsureOperationRecord(volume, record); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	if event.OperationID != record.ID {
		return resources.Volume{}, operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}
	if event.Type != audit.EventTypeVolumeEnsure || event.Outcome != audit.OutcomeSucceeded {
		return resources.Volume{}, operations.OperationRecord{}, auditOutboxInvalidRequest("event_type", "volume ensure audit event must be succeeded volume_ensure")
	}
	if event.Resource.Type != "volume" || event.Resource.ID != volume.ID || strings.TrimSpace(event.Resource.NamespaceID) != "" || event.CallerService != record.CallerService || event.CorrelationID != record.CorrelationID || event.AuthorizedActor.Type != record.AuthorizedActor.Type || event.AuthorizedActor.ID != record.AuthorizedActor.ID {
		return resources.Volume{}, operations.OperationRecord{}, auditOutboxInvalidRequest("resource", "volume ensure audit event must match operation")
	}
	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	volumeArgs, err := volumeArgs(volume)
	if err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	args := append(operationArgs, volumeEnsureStoredPredicateArgs(record)...)
	args = append(args, volumeArgs...)
	args = append(args, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, volumeEnsureOperationCommitWithLeaseSQL(), args...)
	gotVolume, gotOperation, err := scanVolumeAndOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resources.Volume{}, operations.OperationRecord{}, operationLeaseUnavailable("volume ensure commit", record.ID, err)
		}
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	return gotVolume, gotOperation, nil
}

func validateVolumeEnsureOperationRecord(volume resources.Volume, record operations.OperationRecord) error {
	if record.Type != operations.OperationVolumeEnsure {
		return operationLeaseInvalidRequest("operation_type", "operation record must be volume_ensure")
	}
	if record.State != operations.OperationStateSucceeded {
		return operationLeaseInvalidRequest("operation_state", "volume ensure commit requires succeeded operation update")
	}
	if record.Phase != operations.OperationPhaseVolumeEnsureCommitted {
		return operationLeaseInvalidRequest("phase", "volume ensure commit requires committed terminal phase")
	}
	if record.Resource.Type != "volume" || record.Resource.ID != volume.ID {
		return operationLeaseInvalidRequest("resource", "operation resource must match volume metadata")
	}
	if strings.TrimSpace(record.NamespaceID) != "" {
		return operationLeaseInvalidRequest("namespace_id", "volume ensure operation must be volume-global")
	}
	return nil
}

func volumeEnsureOperationCommitWithLeaseSQL() string {
	return "WITH updated_operation AS (" +
		operationLeaseFencedUpdateBaseSQL() +
		"AND operation_type = 'volume_ensure' " +
		"AND phase = 'validate_volume_ensure' " +
		"AND namespace_id = '' " +
		"AND resource_type = 'volume' " +
		"AND resource_id = $14 " +
		"AND caller_service = $15 " +
		"AND correlation_id = $16 " +
		"AND authorized_actor_type = $17 " +
		"AND authorized_actor_id = $18 " +
		"RETURNING " + strings.Join(operationSelectColumns, ", ") +
		"), upserted_volume AS (" +
		"INSERT INTO volumes (" + strings.Join(volumeColumns, ", ") + ") " +
		"SELECT " + placeholders(19, len(volumeColumns)) + " FROM updated_operation " +
		"ON CONFLICT (volume_id) DO UPDATE SET " +
		"backend = EXCLUDED.backend, " +
		"isolation_class = EXCLUDED.isolation_class, " +
		"status = EXCLUDED.status, " +
		"capabilities = EXCLUDED.capabilities, " +
		"updated_at = EXCLUDED.updated_at " +
		"RETURNING " + strings.Join(volumeColumns, ", ") +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(26, len(auditOutboxColumns)) + " FROM updated_operation, upserted_volume " +
		"RETURNING audit_event_id" +
		") SELECT " + prefixedColumns("upserted_volume", volumeColumns) + ", " + prefixedColumns("updated_operation", operationSelectColumns) +
		" FROM upserted_volume, updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func volumeEnsureStoredPredicateArgs(record operations.OperationRecord) []any {
	return []any{record.Resource.ID, record.CallerService, record.CorrelationID, record.AuthorizedActor.Type, record.AuthorizedActor.ID}
}

func scanVolumeAndOperation(row rowScanner) (resources.Volume, operations.OperationRecord, error) {
	var volume resources.Volume
	var backend, isolationClass, status string
	var capabilitiesJSON []byte
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	dest := []any{
		&volume.ID, &backend, &isolationClass, &status, &capabilitiesJSON, &volume.CreatedAt, &volume.UpdatedAt,
		&record.ID, &operationType, &operationState, &record.Phase, &record.Attempt, &leaseOwner, &leaseExpiresAt,
		&record.IdempotencyScope, &record.IdempotencyKey, &requestHash, &record.CorrelationID, &record.CallerService,
		&record.AuthorizedActor.Type, &record.AuthorizedActor.ID, &record.Resource.Type, &record.Resource.ID, &record.NamespaceID,
		&repoID, &templateID, &exportID, &mountBindingID, &sessionFenceID, &externalResourceIDsJSON, &inputSummaryJSON,
		&jvsJSONOutputJSON, &verificationResultJSON, &compensationStatus, &errorJSON, &record.CreatedAt, &startedAt, &finishedAt,
	}
	if err := row.Scan(dest...); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	volume.Backend = resources.VolumeBackend(backend)
	volume.IsolationClass = resources.VolumeIsolationClass(isolationClass)
	volume.Status = resources.VolumeStatus(status)
	if err := unmarshalObject(capabilitiesJSON, &volume.Capabilities); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, fmt.Errorf("unmarshal capabilities: %w", err)
	}
	if err := volume.Validate(); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
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
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return resources.Volume{}, operations.OperationRecord{}, err
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return resources.Volume{}, operations.OperationRecord{}, err
		}
		record.Error = &opErr
	}
	return volume, record.Sanitized(), nil
}
