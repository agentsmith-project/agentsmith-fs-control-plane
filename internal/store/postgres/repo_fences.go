package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

var repoFenceColumns = []string{
	"fence_id",
	"repo_id",
	"fence_kind",
	"holder_operation_id",
	"status",
	"expires_at",
	"released_at",
	"recovery_operation_id",
	"recovery_reason",
	"recovery_started_at",
	"recovered_at",
	"created_at",
	"updated_at",
}

func (store *Store) ListHeldRepoFences(ctx context.Context, repoID string) (fencesOut []fences.Fence, err error) {
	rows, err := store.exec.QueryContext(ctx, repoFenceListHeldSQL(), repoID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	for rows.Next() {
		fence, err := scanRepoFence(rows)
		if err != nil {
			return nil, err
		}
		fencesOut = append(fencesOut, fence)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fencesOut, nil
}

func (store *Store) ListAllHeldRepoFences(ctx context.Context) (fencesOut []fences.Fence, err error) {
	rows, err := store.exec.QueryContext(ctx, repoFenceListAllHeldSQL())
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	for rows.Next() {
		fence, err := scanRepoFence(rows)
		if err != nil {
			return nil, err
		}
		fencesOut = append(fencesOut, fence)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fencesOut, nil
}

func (store *Store) CreateRepoFence(ctx context.Context, fence fences.Fence) error {
	if err := fences.ValidateFence(fence); err != nil {
		return err
	}
	_, err := store.exec.ExecContext(ctx, repoFenceInsertSQL(), repoFenceInsertArgs(fence)...)
	return err
}

func (store *Store) ReleaseRepoFence(ctx context.Context, repoID, fenceID string) error {
	releasedAt := store.now()
	result, err := store.exec.ExecContext(ctx, repoFenceReleaseSQL(),
		string(fences.StatusReleased),
		releasedAt,
		releasedAt,
		repoID,
		fenceID,
		string(fences.StatusActive),
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: release active repo fence %q for repo %q", sql.ErrNoRows, fenceID, repoID)
	}
	return nil
}

func repoFenceInsertSQL() string {
	return "INSERT INTO repo_fences (" + strings.Join(repoFenceColumns, ", ") + ") VALUES (" + placeholders(1, len(repoFenceColumns)) + ")"
}

func repoFenceListHeldSQL() string {
	return "SELECT " + strings.Join(repoFenceColumns, ", ") + " FROM repo_fences WHERE repo_id = $1 AND released_at IS NULL ORDER BY created_at, fence_id"
}

func repoFenceListAllHeldSQL() string {
	return "SELECT " + strings.Join(repoFenceColumns, ", ") + " FROM repo_fences WHERE released_at IS NULL ORDER BY repo_id, created_at, fence_id"
}

func repoFenceReleaseSQL() string {
	return "UPDATE repo_fences SET " +
		"status = $1, " +
		"released_at = $2, " +
		"updated_at = $3 " +
		"WHERE repo_id = $4 AND fence_id = $5 AND status = $6 AND released_at IS NULL AND recovered_at IS NULL"
}

func repoFenceInsertArgs(fence fences.Fence) []any {
	return []any{
		fence.ID,
		fence.RepoID,
		string(fence.Kind),
		fence.HolderOperationID,
		string(fence.Status),
		fence.ExpiresAt,
		timePtrArg(fence.ReleasedAt),
		nullableStringArg(fence.RecoveryOperationID),
		nullableStringArg(fence.RecoveryReason),
		timePtrArg(fence.RecoveryStartedAt),
		timePtrArg(fence.RecoveredAt),
		fence.CreatedAt,
		fence.UpdatedAt,
	}
}

func scanRepoFence(row rowScanner) (fences.Fence, error) {
	var fence fences.Fence
	var kind, status string
	var releasedAt, recoveryStartedAt, recoveredAt sql.NullTime
	var recoveryOperationID, recoveryReason sql.NullString

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
	); err != nil {
		return fences.Fence{}, err
	}

	fence.Kind = fences.Kind(kind)
	fence.Status = fences.Status(status)
	fence.ReleasedAt = nullTimePtr(releasedAt)
	fence.RecoveryOperationID = nullStringValue(recoveryOperationID)
	fence.RecoveryReason = nullStringValue(recoveryReason)
	fence.RecoveryStartedAt = nullTimePtr(recoveryStartedAt)
	fence.RecoveredAt = nullTimePtr(recoveredAt)

	if err := fences.ValidateFence(fence); err != nil {
		return fences.Fence{}, fmt.Errorf("invalid repo fence %q: %w", fence.ID, err)
	}
	return fence, nil
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

	values := []any{
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
	}
	if err := row.Scan(values...); err != nil {
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
		return fences.Fence{}, operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return fences.Fence{}, operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
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
