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
	"github.com/lib/pq"
)

var operationColumns = []string{
	"operation_id",
	"operation_type",
	"operation_state",
	"phase",
	"attempt",
	"lease_owner",
	"lease_expires_at",
	"idempotency_scope",
	"idempotency_key",
	"request_hash",
	"correlation_id",
	"caller_service",
	"authorized_actor_type",
	"authorized_actor_id",
	"resource_type",
	"resource_id",
	"namespace_id",
	"repo_id",
	"template_id",
	"export_id",
	"mount_binding_id",
	"session_fence_id",
	"external_resource_ids",
	"input_summary",
	"jvs_json_output",
	"verification_result",
	"compensation_status",
	"error_json",
	"created_at",
	"started_at",
	"finished_at",
	"updated_at",
}

var operationSelectColumns = operationColumns[:len(operationColumns)-1]

func operationReturningColumnsSQL() string {
	return prefixedColumns("operations", operationSelectColumns)
}

func (store *Store) CreateOperation(ctx context.Context, sanitized operations.SanitizedOperationRecord) error {
	record := sanitized.Record()
	args, err := operationInsertArgs(record)
	if err != nil {
		return err
	}
	_, err = store.exec.ExecContext(ctx, operationInsertSQL(), args...)
	return err
}

func (store *Store) UpdateOperation(ctx context.Context, sanitized operations.SanitizedOperationRecord) error {
	record := sanitized.Record()
	args, err := operationUpdateArgs(record, store.now())
	if err != nil {
		return err
	}
	result, err := store.exec.ExecContext(ctx, operationUpdateSQL(), args...)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: update operation %q", sql.ErrNoRows, record.ID)
	}
	return nil
}

func (store *Store) GetOperation(ctx context.Context, operationID string) (operations.OperationRecord, error) {
	row := store.exec.QueryRowContext(ctx, operationSelectSQL()+" WHERE operation_id = $1", operationID)
	return scanOperation(row)
}

func (store *Store) ListOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list operations for recovery: limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, operationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListNamespaceUpsertOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list namespace upsert operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list namespace upsert operations for recovery: limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, namespaceUpsertOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListNamespaceDisableOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list namespace disable operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list namespace disable operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, namespaceDisableOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListVolumeEnsureOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list volume ensure operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list volume ensure operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, volumeEnsureOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListNamespaceVolumeBindingPutOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list namespace volume binding put operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list namespace volume binding put operations for recovery: limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, namespaceVolumeBindingPutOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListRepoCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list repo create operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list repo create operations for recovery: limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, repoCreateOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListRepoLifecycleOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list repo lifecycle operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list repo lifecycle operations for recovery: limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, repoLifecycleOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListRepoPurgeOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list repo purge operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list repo purge operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, repoPurgeOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListSavePointCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list save point create operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list save point create operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, savePointCreateOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListRestorePreviewOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list restore preview operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list restore preview operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, restorePreviewOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListRestorePreviewDiscardOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list restore preview discard operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list restore preview discard operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, restorePreviewDiscardOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListRestoreRunOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list restore run operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list restore run operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, restoreRunOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListTemplateCreateOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list template create operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list template create operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, templateCreateOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListTemplateCloneOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list template clone operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list template clone operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, templateCloneOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) ListWorkloadMountBindingOperationsForRecovery(ctx context.Context, now time.Time, limit int) ([]operations.OperationRecord, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("list workload mount binding operations for recovery: now must be set")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list workload mount binding operations for recovery: limit must be positive")
	}
	rows, err := store.exec.QueryContext(ctx, workloadMountBindingOperationRecoveryCandidatesSQL(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) RepoHasNonTerminalJVSMutation(ctx context.Context, repoID string) (bool, error) {
	if err := pathresolver.ValidateID(pathresolver.RepoID, strings.TrimSpace(repoID)); err != nil {
		return false, err
	}
	var exists bool
	row := store.exec.QueryRowContext(ctx, repoHasNonTerminalJVSMutationSQL(), repoID)
	if err := row.Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (store *Store) RestoreRunExistsForPreviewOperation(ctx context.Context, namespaceID, repoID, previewOperationID string) (bool, error) {
	namespaceID = strings.TrimSpace(namespaceID)
	repoID = strings.TrimSpace(repoID)
	previewOperationID = strings.TrimSpace(previewOperationID)
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		return false, err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		return false, err
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, previewOperationID); err != nil {
		return false, err
	}
	var exists bool
	row := store.exec.QueryRowContext(ctx, restoreRunExistsForPreviewOperationSQL(), namespaceID, repoID, previewOperationID)
	if err := row.Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (store *Store) AcquireOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	row := store.exec.QueryRowContext(ctx, operationAcquireLeaseSQL(),
		args.operationID,
		args.owner,
		args.expiresAt,
		args.now,
		args.recoveryMode,
		args.cancelPolicy,
	)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireNamespaceUpsertOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "namespace upsert recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, namespaceUpsertOperationAcquireLeaseSQL(),
		args.operationID,
		args.owner,
		args.expiresAt,
		args.now,
		args.cancelPolicy,
	)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire namespace upsert", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireNamespaceDisableOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "namespace disable recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, namespaceDisableOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire namespace disable", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireVolumeEnsureOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "volume ensure recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, volumeEnsureOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire volume ensure", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireNamespaceVolumeBindingPutOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "namespace volume binding put recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, namespaceVolumeBindingPutOperationAcquireLeaseSQL(),
		args.operationID,
		args.owner,
		args.expiresAt,
		args.now,
		args.cancelPolicy,
	)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire namespace volume binding put", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireRepoCreateOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "repo create recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, repoCreateOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire repo create", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireRepoLifecycleOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "repo lifecycle recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, repoLifecycleOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire repo lifecycle", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireRepoPurgeOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "repo purge recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, repoPurgeOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("repo purge acquire lease", operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireSavePointCreateOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "save point create recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, savePointCreateOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire save point create", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireRestorePreviewOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "restore preview recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, restorePreviewOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire restore preview", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireRestorePreviewDiscardOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "restore preview discard recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, restorePreviewDiscardOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire restore preview discard", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireRestoreRunOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "restore run recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, restoreRunOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire restore run", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireTemplateCreateOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "template create recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, templateCreateOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire template create", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireTemplateCloneOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "template clone recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, templateCloneOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire template clone", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) AcquireWorkloadMountBindingOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	if args.recoveryMode != "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("recovery_mode", "workload mount binding recovery does not perform explicit recovery actions")
	}
	row := store.exec.QueryRowContext(ctx, workloadMountBindingOperationAcquireLeaseSQL(), args.operationID, args.owner, args.expiresAt, args.now, args.cancelPolicy)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("acquire workload mount binding", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) ListEarlierNonTerminalRepoLifecycleOperations(ctx context.Context, repoID, operationID string, createdAt time.Time) ([]operations.OperationRecord, error) {
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		return nil, err
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, operationID); err != nil {
		return nil, err
	}
	rows, err := store.exec.QueryContext(ctx, earlierNonTerminalRepoLifecycleOperationsSQL(), repoID, operationID, createdAt.UTC())
	if err != nil {
		return nil, err
	}
	return scanOperations(rows)
}

func (store *Store) RenewOperationLease(ctx context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	args, err := operationLeaseRequestArgs(operationID, request)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	row := store.exec.QueryRowContext(ctx, operationRenewLeaseSQL(),
		args.operationID,
		args.owner,
		args.expiresAt,
		args.now,
	)
	record, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("renew", args.operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return record, nil
}

func (store *Store) UpdateOperationWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time) (operations.OperationRecord, error) {
	record := sanitized.Record()
	args, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	row := store.exec.QueryRowContext(ctx, operationLeaseFencedUpdateSQL(), args...)
	updated, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("update", record.ID, err)
		}
		return operations.OperationRecord{}, err
	}
	return updated, nil
}

func (store *Store) CommitOperationWithLease(ctx context.Context, sanitized operations.SanitizedOperationRecord, owner string, now time.Time, event audit.Event) (operations.OperationRecord, error) {
	record := sanitized.Record()
	operationID := strings.TrimSpace(record.ID)
	if strings.TrimSpace(event.OperationID) == "" {
		return operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "missing audit operation id")
	}
	if event.OperationID != operationID {
		return operations.OperationRecord{}, auditOutboxInvalidRequest("operation_id", "audit operation id must match operation update")
	}

	operationArgs, err := operationLeaseFencedUpdateArgs(record, owner, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	outboxRecord, err := audit.NewOutboxRecord(event, now)
	if err != nil {
		return operations.OperationRecord{}, err
	}

	args := append(operationArgs, auditOutboxInsertArgs(outboxRecord)...)
	row := store.exec.QueryRowContext(ctx, operationCommitWithLeaseSQL(), args...)
	updated, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.OperationRecord{}, operationLeaseUnavailable("commit", operationID, err)
		}
		return operations.OperationRecord{}, err
	}
	return updated, nil
}

func (store *Store) CreateOrReuseOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()

	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}

	var inserted bool
	row := store.exec.QueryRowContext(ctx, operationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInserted(row, &inserted)
	if err != nil {
		if mapped := mapOperationUniqueViolation(err, record.RepoID); mapped != nil {
			return operations.IdempotencyResolution{}, mapped
		}
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	return operations.IdempotencyResolution{
		Operation: got.Sanitized(),
		Existing:  !inserted,
		Reused:    !inserted,
	}, nil
}

func (store *Store) GetOperationByIdempotencyScope(ctx context.Context, scope operations.IdempotencyScope) (operations.OperationRecord, error) {
	if strings.TrimSpace(scope.CallerService) == "" || strings.TrimSpace(scope.IdempotencyKey) == "" || strings.TrimSpace(string(scope.OperationType)) == "" {
		return operations.OperationRecord{}, operationLeaseInvalidRequest("idempotency_scope", "operation idempotency scope is incomplete")
	}
	row := store.exec.QueryRowContext(ctx, operationSelectByIdempotencyScopeSQL(), scope.CallerService, scope.NamespaceID, string(scope.OperationType), scope.IdempotencyKey)
	return scanOperation(row)
}

func (store *Store) CreateOrReuseRepoCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if err := validateRepoCreateOperationSpec(record); err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()

	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}

	var inserted bool
	row := store.exec.QueryRowContext(ctx, repoCreateOperationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInserted(row, &inserted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.IdempotencyResolution{}, fmt.Errorf("%w: repo %q already exists", operations.ErrRepoAlreadyExists, record.RepoID)
		}
		if mapped := mapOperationUniqueViolation(err, record.RepoID); mapped != nil {
			return operations.IdempotencyResolution{}, mapped
		}
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	return operations.IdempotencyResolution{
		Operation: got.Sanitized(),
		Existing:  !inserted,
		Reused:    !inserted,
	}, nil
}

func (store *Store) CreateOrReuseRestorePreviewOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if err := validateRestorePreviewOperationSpec(record); err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()

	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}

	var inserted bool
	var gateCode string
	row := store.exec.QueryRowContext(ctx, restorePreviewOperationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInsertedAndGate(row, &inserted, &gateCode)
	if err != nil {
		if mapped := mapOperationUniqueViolation(err, record.RepoID); mapped != nil {
			return operations.IdempotencyResolution{}, mapped
		}
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	if gateCode != "" {
		return operations.IdempotencyResolution{}, restorePreviewIntakeGateError(gateCode, record.RepoID)
	}
	return operations.IdempotencyResolution{Operation: got.Sanitized(), Existing: !inserted, Reused: !inserted}, nil
}

func (store *Store) CreateOrReuseRestorePreviewDiscardOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if err := validateRestorePreviewDiscardOperationSpec(record); err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()

	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}

	var inserted bool
	var gateCode string
	row := store.exec.QueryRowContext(ctx, restorePreviewDiscardOperationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInsertedAndGate(row, &inserted, &gateCode)
	if err != nil {
		if mapped := mapOperationUniqueViolation(err, record.RepoID); mapped != nil {
			return operations.IdempotencyResolution{}, mapped
		}
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	if gateCode != "" {
		return operations.IdempotencyResolution{}, restorePreviewDiscardIntakeGateError(gateCode, record.RepoID)
	}
	return operations.IdempotencyResolution{Operation: got.Sanitized(), Existing: !inserted, Reused: !inserted}, nil
}

func (store *Store) CreateOrReuseRestoreRunOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if err := validateRestoreRunOperationSpec(record); err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()

	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}

	var inserted bool
	var gateCode string
	row := store.exec.QueryRowContext(ctx, restoreRunOperationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInsertedAndGate(row, &inserted, &gateCode)
	if err != nil {
		if mapped := mapOperationUniqueViolation(err, record.RepoID); mapped != nil {
			return operations.IdempotencyResolution{}, mapped
		}
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	if gateCode != "" {
		return operations.IdempotencyResolution{}, restoreRunIntakeGateError(gateCode, record.RepoID)
	}
	return operations.IdempotencyResolution{Operation: got.Sanitized(), Existing: !inserted, Reused: !inserted}, nil
}

func (store *Store) CreateOrReuseTemplateCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if err := validateTemplateCreateOperationSpec(record); err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()
	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	var inserted bool
	row := store.exec.QueryRowContext(ctx, templateCreateOperationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInserted(row, &inserted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.IdempotencyResolution{}, fmt.Errorf("%w: template %q already exists", operations.ErrRepoAlreadyExists, record.TemplateID)
		}
		if mapped := mapOperationUniqueViolation(err, record.RepoID); mapped != nil {
			return operations.IdempotencyResolution{}, mapped
		}
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	return operations.IdempotencyResolution{Operation: got.Sanitized(), Existing: !inserted, Reused: !inserted}, nil
}

func (store *Store) CreateOrReuseTemplateCloneOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if err := validateTemplateCloneOperationSpec(record); err != nil {
		return operations.IdempotencyResolution{}, err
	}
	record = record.SanitizedForPersistence().Record()
	args, err := operationInsertArgs(record)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	var inserted bool
	row := store.exec.QueryRowContext(ctx, templateCloneOperationCreateOrReuseSQL(), args...)
	got, err := scanOperationWithInserted(row, &inserted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return operations.IdempotencyResolution{}, fmt.Errorf("%w: repo %q already exists", operations.ErrRepoAlreadyExists, record.RepoID)
		}
		if mapped := mapOperationUniqueViolation(err, record.RepoID); mapped != nil {
			return operations.IdempotencyResolution{}, mapped
		}
		return operations.IdempotencyResolution{}, err
	}
	if !inserted && got.RequestHash != spec.RequestHash {
		return operations.IdempotencyResolution{}, fmt.Errorf("%w: scope %q already exists with a different request hash", operations.ErrIdempotencyConflict, spec.Scope.String())
	}
	return operations.IdempotencyResolution{Operation: got.Sanitized(), Existing: !inserted, Reused: !inserted}, nil
}

func validateRepoCreateOperationSpec(record operations.OperationRecord) error {
	if record.Type != operations.OperationRepoCreate {
		return operationLeaseInvalidRequest("operation_type", "operation record must be repo_create")
	}
	if record.State != operations.OperationStateQueued {
		return operationLeaseInvalidRequest("operation_state", "repo_create intake requires queued operation")
	}
	if record.Phase != operations.OperationPhaseRepoCreateValidate {
		return operationLeaseInvalidRequest("phase", "repo_create intake requires validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" {
		return operationLeaseInvalidRequest("resource", "repo_create intake requires namespace and repo ids")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "repo_create resource must match target repo")
	}
	return nil
}

func validateRestorePreviewOperationSpec(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestorePreview {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview")
	}
	if record.State != operations.OperationStateQueued {
		return operationLeaseInvalidRequest("operation_state", "restore_preview intake requires queued operation")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewValidate {
		return operationLeaseInvalidRequest("phase", "restore_preview intake requires validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" {
		return operationLeaseInvalidRequest("resource", "restore_preview intake requires namespace and repo ids")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "restore_preview resource must match target repo")
	}
	savePointID, _ := record.InputSummary["save_point_id"].(string)
	if err := operations.ValidateSavePointID(strings.TrimSpace(savePointID)); err != nil {
		return operationLeaseInvalidRequest("input_summary.save_point_id", "restore_preview intake requires safe save point id")
	}
	return nil
}

func validateRestoreRunOperationSpec(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestoreRun {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_run")
	}
	if record.State != operations.OperationStateQueued {
		return operationLeaseInvalidRequest("operation_state", "restore_run intake requires queued operation")
	}
	if record.Phase != operations.OperationPhaseRestoreRunValidate {
		return operationLeaseInvalidRequest("phase", "restore_run intake requires validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" {
		return operationLeaseInvalidRequest("resource", "restore_run intake requires namespace and repo ids")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "restore_run resource must match target repo")
	}
	previewOperationID, _ := record.InputSummary["preview_operation_id"].(string)
	if err := pathresolver.ValidateID(pathresolver.OperationID, strings.TrimSpace(previewOperationID)); err != nil {
		return operationLeaseInvalidRequest("input_summary.preview_operation_id", "restore_run intake requires safe preview operation id")
	}
	return nil
}

func validateRestorePreviewDiscardOperationSpec(record operations.OperationRecord) error {
	if record.Type != operations.OperationRestorePreviewDiscard {
		return operationLeaseInvalidRequest("operation_type", "operation record must be restore_preview_discard")
	}
	if record.State != operations.OperationStateQueued {
		return operationLeaseInvalidRequest("operation_state", "restore_preview_discard intake requires queued operation")
	}
	if record.Phase != operations.OperationPhaseRestorePreviewDiscardValidate {
		return operationLeaseInvalidRequest("phase", "restore_preview_discard intake requires validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" {
		return operationLeaseInvalidRequest("resource", "restore_preview_discard intake requires namespace and repo ids")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "restore_preview_discard resource must match target repo")
	}
	previewOperationID, _ := record.InputSummary["preview_operation_id"].(string)
	if err := pathresolver.ValidateID(pathresolver.OperationID, strings.TrimSpace(previewOperationID)); err != nil {
		return operationLeaseInvalidRequest("input_summary.preview_operation_id", "restore_preview_discard intake requires safe preview operation id")
	}
	return nil
}

func validateTemplateCreateOperationSpec(record operations.OperationRecord) error {
	if record.Type != operations.OperationTemplateCreate {
		return operationLeaseInvalidRequest("operation_type", "operation record must be template_create")
	}
	if record.State != operations.OperationStateQueued || record.Phase != operations.OperationPhaseTemplateCreateValidate {
		return operationLeaseInvalidRequest("phase", "template_create intake requires queued validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || strings.TrimSpace(record.TemplateID) == "" {
		return operationLeaseInvalidRequest("resource", "template_create intake requires namespace, source repo, and template ids")
	}
	if record.Resource.Type != "repo_template" || record.Resource.ID != record.TemplateID {
		return operationLeaseInvalidRequest("resource", "template_create resource must match target template")
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, record.RepoID); err != nil {
		return operationLeaseInvalidRequest("repo_id", "template_create requires safe source repo id")
	}
	if err := pathresolver.ValidateID(pathresolver.TemplateID, record.TemplateID); err != nil {
		return operationLeaseInvalidRequest("template_id", "template_create requires safe target template id")
	}
	return nil
}

func validateTemplateCloneOperationSpec(record operations.OperationRecord) error {
	if record.Type != operations.OperationTemplateClone {
		return operationLeaseInvalidRequest("operation_type", "operation record must be template_clone")
	}
	if record.State != operations.OperationStateQueued || record.Phase != operations.OperationPhaseTemplateCloneValidate {
		return operationLeaseInvalidRequest("phase", "template_clone intake requires queued validate phase")
	}
	if strings.TrimSpace(record.NamespaceID) == "" || strings.TrimSpace(record.RepoID) == "" || strings.TrimSpace(record.TemplateID) == "" {
		return operationLeaseInvalidRequest("resource", "template_clone intake requires namespace, target repo, and template ids")
	}
	if record.Resource.Type != "repo" || record.Resource.ID != record.RepoID {
		return operationLeaseInvalidRequest("resource", "template_clone resource must match target repo")
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, record.RepoID); err != nil {
		return operationLeaseInvalidRequest("repo_id", "template_clone requires safe target repo id")
	}
	if err := pathresolver.ValidateID(pathresolver.TemplateID, record.TemplateID); err != nil {
		return operationLeaseInvalidRequest("template_id", "template_clone requires safe template id")
	}
	return nil
}

func restorePreviewIntakeGateError(gateCode, repoID string) error {
	switch gateCode {
	case "active_restore_plan":
		return fmt.Errorf("%w: repo %q has active restore plan", operations.ErrActiveRestorePlan, repoID)
	case "same_repo_jvs_mutation":
		return fmt.Errorf("%w: repo %q has non-terminal JVS mutation", operations.ErrRepoJVSMutationInProgress, repoID)
	default:
		return fmt.Errorf("%w: unknown restore preview intake gate %q", operations.ErrMissingOperationBoundary, gateCode)
	}
}

func restorePreviewDiscardIntakeGateError(gateCode, repoID string) error {
	switch gateCode {
	case "plan_not_pending":
		return fmt.Errorf("%w: repo %q restore preview plan is not pending", operations.ErrRestorePlanNotPending, repoID)
	default:
		return fmt.Errorf("%w: unknown restore preview discard intake gate %q", operations.ErrMissingOperationBoundary, gateCode)
	}
}

func restoreRunIntakeGateError(gateCode, repoID string) error {
	switch gateCode {
	case "duplicate_restore_run":
		return fmt.Errorf("%w: repo %q preview already has restore_run", operations.ErrRestoreRunAlreadyExists, repoID)
	case "plan_not_pending":
		return fmt.Errorf("%w: repo %q restore preview plan is not pending", operations.ErrRestorePlanNotPending, repoID)
	default:
		return fmt.Errorf("%w: unknown restore run intake gate %q", operations.ErrMissingOperationBoundary, gateCode)
	}
}

func mapOperationUniqueViolation(err error, repoID string) error {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || string(pqErr.Code) != "23505" {
		return nil
	}
	switch pqErr.Constraint {
	case "operations_one_non_terminal_jvs_mutation_per_repo_idx":
		return fmt.Errorf("%w: repo %q has concurrent non-terminal JVS mutation", operations.ErrRepoJVSMutationInProgress, repoID)
	case "operations_restore_run_one_per_preview_idx":
		return fmt.Errorf("%w: repo %q preview already has restore_run", operations.ErrRestoreRunAlreadyExists, repoID)
	default:
		return nil
	}
}

func operationInsertSQL() string {
	return "INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") VALUES (" + placeholders(1, len(operationColumns)) + ")"
}

func operationCreateOrReuseSQL() string {
	return "INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") VALUES (" + placeholders(1, len(operationColumns)) + ") " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + operationReturningColumnsSQL() + ", (xmax = 0) AS inserted"
}

func repoCreateOperationCreateOrReuseSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", false AS inserted FROM operations " +
		"WHERE caller_service = $12 " +
		"AND namespace_id = $17 " +
		"AND operation_type = 'repo_create' " +
		"AND idempotency_key = $9" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") " +
		"SELECT " + placeholders(1, len(operationColumns)) + " " +
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $18) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + operationReturningColumnsSQL() + ", (xmax = 0) AS inserted" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted FROM existing_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted FROM inserted_operation " +
		"LIMIT 1"
}

func templateCreateOperationCreateOrReuseSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", false AS inserted FROM operations " +
		"WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'template_create' AND idempotency_key = $9" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") " +
		"SELECT " + placeholders(1, len(operationColumns)) + " " +
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $19) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + operationReturningColumnsSQL() + ", (xmax = 0) AS inserted" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted FROM existing_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted FROM inserted_operation LIMIT 1"
}

func templateCloneOperationCreateOrReuseSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", false AS inserted FROM operations " +
		"WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'template_clone' AND idempotency_key = $9" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") " +
		"SELECT " + placeholders(1, len(operationColumns)) + " " +
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND NOT EXISTS (SELECT 1 FROM repos WHERE repo_id = $18) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + operationReturningColumnsSQL() + ", (xmax = 0) AS inserted" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted FROM existing_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted FROM inserted_operation LIMIT 1"
}

func restorePreviewOperationCreateOrReuseSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", false AS inserted, '' AS gate_code FROM operations " +
		"WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'restore_preview' AND idempotency_key = $9" +
		"), same_repo_jvs_mutation AS (" +
		"SELECT 1 FROM operations WHERE repo_id = $18 " +
		"AND operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") " +
		"AND operation_state NOT IN ('succeeded','failed','cancelled') " +
		"AND NOT EXISTS (SELECT 1 FROM existing_operation) LIMIT 1" +
		"), active_restore_plan AS (" +
		"SELECT 1 FROM restore_plans WHERE repo_id = $18 " +
		"AND status IN (" + restorePlanActiveStatusSQLList() + ") " +
		"AND NOT EXISTS (SELECT 1 FROM existing_operation) LIMIT 1" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") " +
		"SELECT " + placeholders(1, len(operationColumns)) + " " +
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND NOT EXISTS (SELECT 1 FROM same_repo_jvs_mutation) " +
		"AND NOT EXISTS (SELECT 1 FROM active_restore_plan) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + operationReturningColumnsSQL() + ", (xmax = 0) AS inserted, '' AS gate_code" +
		"), selected_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM existing_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM inserted_operation" +
		"), gate_failure AS (" +
		"SELECT " + operationSelectColumnPlaceholdersSQL() + ", false AS inserted, " +
		"CASE WHEN EXISTS (SELECT 1 FROM active_restore_plan) THEN 'active_restore_plan' WHEN EXISTS (SELECT 1 FROM same_repo_jvs_mutation) THEN 'same_repo_jvs_mutation' ELSE '' END AS gate_code " +
		"WHERE NOT EXISTS (SELECT 1 FROM selected_operation)" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM selected_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM gate_failure LIMIT 1"
}

func restoreRunOperationCreateOrReuseSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", false AS inserted, '' AS gate_code FROM operations " +
		"WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'restore_run' AND idempotency_key = $9" +
		"), duplicate_restore_run AS (" +
		"SELECT 1 FROM operations WHERE operation_type = 'restore_run' " +
		"AND namespace_id = $17 AND repo_id = $18 " +
		"AND resource_type = 'repo' AND resource_id = $18 " +
		"AND input_summary->>'preview_operation_id' = $24::jsonb->>'preview_operation_id' " +
		"AND operation_state NOT IN ('failed','cancelled') " +
		"AND NOT EXISTS (SELECT 1 FROM existing_operation) LIMIT 1" +
		"), matching_pending_restore_plan AS (" +
		"SELECT restore_plan_id FROM restore_plans WHERE namespace_id = $17 AND repo_id = $18 " +
		"AND preview_operation_id = $24::jsonb->>'preview_operation_id' " +
		"AND status = 'pending' " +
		"AND NOT EXISTS (SELECT 1 FROM existing_operation) LIMIT 1 FOR UPDATE" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") " +
		"SELECT " + placeholders(1, len(operationColumns)) + " " +
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND EXISTS (SELECT 1 FROM matching_pending_restore_plan) " +
		"AND NOT EXISTS (SELECT 1 FROM duplicate_restore_run) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + operationReturningColumnsSQL() + ", (xmax = 0) AS inserted, '' AS gate_code" +
		"), selected_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM existing_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM inserted_operation" +
		"), gate_failure AS (" +
		"SELECT " + operationSelectColumnPlaceholdersSQL() + ", false AS inserted, " +
		"CASE WHEN EXISTS (SELECT 1 FROM duplicate_restore_run) THEN 'duplicate_restore_run' ELSE 'plan_not_pending' END AS gate_code " +
		"WHERE NOT EXISTS (SELECT 1 FROM selected_operation)" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM selected_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM gate_failure LIMIT 1"
}

func restorePreviewDiscardOperationCreateOrReuseSQL() string {
	return "WITH existing_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", false AS inserted, '' AS gate_code FROM operations " +
		"WHERE caller_service = $12 AND namespace_id = $17 AND operation_type = 'restore_preview_discard' AND idempotency_key = $9" +
		"), matching_pending_restore_plan AS (" +
		"SELECT restore_plan_id FROM restore_plans WHERE namespace_id = $17 AND repo_id = $18 " +
		"AND preview_operation_id = $24::jsonb->>'preview_operation_id' " +
		"AND status = 'pending' " +
		"AND NOT EXISTS (SELECT 1 FROM existing_operation) LIMIT 1 FOR UPDATE" +
		"), inserted_operation AS (" +
		"INSERT INTO operations (" + strings.Join(operationColumns, ", ") + ") " +
		"SELECT " + placeholders(1, len(operationColumns)) + " " +
		"WHERE NOT EXISTS (SELECT 1 FROM existing_operation) " +
		"AND EXISTS (SELECT 1 FROM matching_pending_restore_plan) " +
		"ON CONFLICT (caller_service, namespace_id, operation_type, idempotency_key) DO UPDATE SET operation_id = operations.operation_id " +
		"RETURNING " + operationReturningColumnsSQL() + ", (xmax = 0) AS inserted, '' AS gate_code" +
		"), selected_operation AS (" +
		"SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM existing_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM inserted_operation" +
		"), gate_failure AS (" +
		"SELECT " + operationSelectColumnPlaceholdersSQL() + ", false AS inserted, 'plan_not_pending' AS gate_code " +
		"WHERE NOT EXISTS (SELECT 1 FROM selected_operation)" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM selected_operation " +
		"UNION ALL SELECT " + strings.Join(operationSelectColumns, ", ") + ", inserted, gate_code FROM gate_failure LIMIT 1"
}

func operationSelectSQL() string {
	return "SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM operations"
}

func operationSelectByIdempotencyScopeSQL() string {
	return operationSelectSQL() + " WHERE caller_service = $1 AND namespace_id = $2 AND operation_type = $3 AND idempotency_key = $4"
}

func operationSelectColumnPlaceholdersSQL() string {
	parts := make([]string, len(operationSelectColumns))
	for idx, column := range operationSelectColumns {
		parts[idx] = fmt.Sprintf("$%d AS %s", idx+1, column)
	}
	return strings.Join(parts, ", ")
}

func operationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required') " +
		"ORDER BY created_at, operation_id LIMIT $2"
}

func namespaceUpsertOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'namespace_upsert' AND phase = 'validate_namespace_upsert' AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func namespaceDisableOperationRecoveryCandidatesSQL() string {
	return scopedOperationRecoveryCandidatesSQL("namespace_disable", "validate_namespace_disable", false)
}

func scopedOperationRecoveryCandidatesSQL(operationType, phase string, requireGlobal bool) string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	global := ""
	if requireGlobal {
		global = " AND namespace_id = ''"
	}
	return operationSelectSQL() + " WHERE " +
		"operation_type = '" + operationType + "' AND phase = '" + phase + "'" + global + " AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func volumeEnsureOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'volume_ensure' AND phase = 'validate_volume_ensure' AND namespace_id = '' AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func namespaceVolumeBindingPutOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'namespace_volume_binding_put' AND phase = 'validate_namespace_volume_binding_put' AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func repoCreateOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'repo_create' AND phase = 'validate_repo_create' AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func repoLifecycleOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type IN (" + repoLifecycleOperationTypeSQLList() + ") AND phase = 'validate_repo_lifecycle' AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func repoPurgeOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'repo_purge' AND phase = 'validate_repo_lifecycle' AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func savePointCreateOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'save_point_create' AND phase IN ('validate_save_point_create','save_point_create_prepared') AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func restorePreviewOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'restore_preview' AND phase IN ('validate_restore_preview','restore_preview_preflight_idle') AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND phase = 'validate_restore_preview' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func restorePreviewDiscardOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'restore_preview_discard' AND phase IN ('validate_restore_preview_discard','restore_preview_discarding') AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND phase = 'validate_restore_preview_discard' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func restoreRunOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'restore_run' AND phase IN ('validate_restore_run','restore_run_writer_fenced','restore_run_consuming') AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND phase = 'validate_restore_run' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func templateCreateOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type = 'template_create' AND phase IN ('validate_template_create','template_create_writer_fenced') AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND phase = 'validate_template_create' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func templateCloneOperationRecoveryCandidatesSQL() string {
	return scopedOperationRecoveryCandidatesSQL("template_clone", operations.OperationPhaseTemplateCloneValidate, false)
}

func workloadMountBindingOperationRecoveryCandidatesSQL() string {
	noLeasePair := "(lease_owner IS NULL AND lease_expires_at IS NULL)"
	completeLeasePair := "(lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)"
	invalidLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NOT NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) = '') OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NULL))"
	return operationSelectSQL() + " WHERE " +
		"operation_type IN ('mount_binding_create','mount_binding_status_update','mount_binding_heartbeat','mount_binding_release','mount_binding_revoke') " +
		"AND phase IN ('validate_mount_binding_create','validate_mount_binding_status_update','validate_mount_binding_heartbeat','validate_mount_binding_release','validate_mount_binding_revoke') AND (" +
		"(operation_state = 'queued') OR " +
		"(operation_state = 'running' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'cancel_requested' AND (" + noLeasePair + " OR " + invalidLeasePair + " OR (" + completeLeasePair + " AND lease_expires_at <= $1))) OR " +
		"(operation_state = 'operator_intervention_required')" +
		") ORDER BY created_at, operation_id LIMIT $2"
}

func operationUpdateSQL() string {
	return "UPDATE operations SET " +
		"operation_state = $1, " +
		"phase = $2, " +
		"attempt = $3, " +
		"lease_owner = $4, " +
		"lease_expires_at = $5, " +
		"external_resource_ids = $6, " +
		"input_summary = $7, " +
		"jvs_json_output = $8, " +
		"verification_result = $9, " +
		"compensation_status = $10, " +
		"error_json = $11, " +
		"started_at = $12, " +
		"finished_at = $13, " +
		"updated_at = $14 " +
		"WHERE operation_id = $15"
}

func operationAcquireLeaseSQL() string {
	validLeasePair := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL))"
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 AND (" +
		"(operation_state = 'queued' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'operator_intervention_required' AND $5 = 'explicit_recovery_action' AND " + validLeasePair + ") OR " +
		"(operation_state = 'cancel_requested' AND $6 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func namespaceUpsertOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type = 'namespace_upsert' " +
		"AND phase = 'validate_namespace_upsert' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func namespaceDisableOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type = 'namespace_disable' " +
		"AND phase = 'validate_namespace_disable' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func scopedOperationAcquireLeaseSQL(operationType, phase string) string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type = '" + operationType + "' " +
		"AND phase = '" + phase + "' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func volumeEnsureOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type = 'volume_ensure' " +
		"AND phase = 'validate_volume_ensure' " +
		"AND namespace_id = '' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func namespaceVolumeBindingPutOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type = 'namespace_volume_binding_put' " +
		"AND phase = 'validate_namespace_volume_binding_put' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func repoCreateOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type = 'repo_create' " +
		"AND phase = 'validate_repo_create' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func repoLifecycleOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, repo_id, created_at, operation_state AS eligible_operation_state FROM operations WHERE operation_id = $1 " +
		"AND operation_type IN (" + repoLifecycleOperationTypeSQLList() + ") " +
		"AND phase = 'validate_repo_lifecycle' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") FOR UPDATE" +
		"), held_fence AS (" +
		"SELECT repo_fences.fence_id FROM repo_fences, eligible_operation WHERE $5 = 'finalize_cancellation' AND eligible_operation.eligible_operation_state = 'cancel_requested' AND repo_fences.repo_id = eligible_operation.repo_id AND repo_fences.fence_kind = 'lifecycle' AND repo_fences.holder_operation_id = $1 AND repo_fences.status = 'active' AND repo_fences.released_at IS NULL AND repo_fences.recovered_at IS NULL FOR UPDATE" +
		"), released_fence AS (" +
		"UPDATE repo_fences SET status = 'released', released_at = $4, updated_at = $4 FROM held_fence WHERE repo_fences.fence_id = held_fence.fence_id RETURNING repo_fences.fence_id" +
		"), earlier_jvs_mutation AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), active_restore_plan AS (" +
		"SELECT 1 FROM restore_plans p, eligible_operation e WHERE p.repo_id = e.repo_id " +
		"AND p.status IN (" + restorePlanActiveStatusSQLList() + ") LIMIT 1" +
		"), updated_operation AS (" +
		"UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id AND ($5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation)) AND ($5 = 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM active_restore_plan)) AND ($5 <> 'finalize_cancellation' OR NOT EXISTS (SELECT 1 FROM held_fence) OR EXISTS (SELECT 1 FROM released_fence)) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation"
}

func repoPurgeOperationAcquireLeaseSQL() string {
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, repo_id, created_at FROM operations WHERE operation_id = $1 " +
		"AND operation_type = 'repo_purge' " +
		"AND phase = 'validate_repo_lifecycle' " +
		"AND $5 = '' " +
		"AND (" +
		"(operation_state = 'queued' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4)" +
		") FOR UPDATE" +
		"), earlier_jvs_mutation AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), active_restore_plan AS (" +
		"SELECT 1 FROM restore_plans p, eligible_operation e WHERE p.repo_id = e.repo_id " +
		"AND p.status IN (" + restorePlanActiveStatusSQLList() + ") LIMIT 1" +
		"), updated_operation AS (" +
		"UPDATE operations SET " +
		"operation_state = 'running', " +
		"attempt = attempt + 1, " +
		"lease_owner = $2, " +
		"lease_expires_at = $3, " +
		"started_at = COALESCE(started_at, $4), " +
		"updated_at = $4 FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id AND NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation) AND NOT EXISTS (SELECT 1 FROM active_restore_plan) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation"
}

func savePointCreateOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, repo_id, created_at FROM operations WHERE operation_id = $1 " +
		"AND operation_type = 'save_point_create' " +
		"AND phase IN ('validate_save_point_create','save_point_create_prepared') " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") FOR UPDATE" +
		"), earlier_jvs_mutation AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), earlier_repo_lifecycle AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoLifecycleAndPurgeOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), active_restore_plan AS (" +
		"SELECT 1 FROM restore_plans p, eligible_operation e WHERE p.repo_id = e.repo_id " +
		"AND p.status IN (" + restorePlanActiveStatusSQLList() + ") LIMIT 1" +
		"), updated_operation AS (" +
		"UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id AND ($5 = 'finalize_cancellation' OR (NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation) AND NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle) AND NOT EXISTS (SELECT 1 FROM active_restore_plan))) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation"
}

func restorePreviewOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, repo_id, created_at, phase FROM operations WHERE operation_id = $1 " +
		"AND operation_type = 'restore_preview' " +
		"AND phase IN ('validate_restore_preview','restore_preview_preflight_idle') " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND phase = 'validate_restore_preview' AND " + cancelFinalizableLease + ")" +
		") FOR UPDATE" +
		"), earlier_jvs_mutation AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), earlier_repo_lifecycle AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoLifecycleAndPurgeOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), active_restore_plan AS (" +
		"SELECT 1 FROM restore_plans p, eligible_operation e WHERE p.repo_id = e.repo_id " +
		"AND p.status IN (" + restorePlanActiveStatusSQLList() + ") LIMIT 1" +
		"), updated_operation AS (" +
		"UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id " +
		"AND ($5 <> 'finalize_cancellation' OR eligible_operation.phase = 'validate_restore_preview') " +
		"AND ($5 = 'finalize_cancellation' OR (NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation) AND NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle) AND NOT EXISTS (SELECT 1 FROM active_restore_plan))) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation"
}

func restorePreviewDiscardOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, namespace_id, repo_id, created_at, phase, input_summary FROM operations WHERE operation_id = $1 " +
		"AND operation_type = 'restore_preview_discard' " +
		"AND phase IN ('validate_restore_preview_discard','restore_preview_discarding') " +
		"AND (input_summary->>'preview_operation_id') IS NOT NULL AND btrim(input_summary->>'preview_operation_id') <> '' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND phase = 'validate_restore_preview_discard' AND " + cancelFinalizableLease + ")" +
		") FOR UPDATE" +
		"), matching_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE p.preview_operation_id = e.input_summary->>'preview_operation_id' " +
		"AND p.namespace_id = e.namespace_id AND p.repo_id = e.repo_id " +
		"AND ((e.phase = 'validate_restore_preview_discard' AND p.status = 'pending') OR (e.phase = 'restore_preview_discarding' AND p.status = 'discarding')) LIMIT 1 FOR UPDATE" +
		"), earlier_jvs_mutation AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') " +
		"AND NOT (o.operation_id = e.input_summary->>'preview_operation_id' AND o.operation_type = 'restore_preview' AND o.operation_state = 'succeeded') LIMIT 1" +
		"), earlier_repo_lifecycle AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoLifecycleAndPurgeOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), unrelated_active_restore_plan AS (" +
		"SELECT 1 FROM restore_plans p, eligible_operation e WHERE p.repo_id = e.repo_id AND p.namespace_id = e.namespace_id " +
		"AND p.preview_operation_id <> e.input_summary->>'preview_operation_id' " +
		"AND p.status IN (" + restorePlanActiveStatusSQLList() + ") LIMIT 1" +
		"), updated_operation AS (" +
		"UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id " +
		"AND ($5 <> 'finalize_cancellation' OR eligible_operation.phase = 'validate_restore_preview_discard') " +
		"AND ($5 = 'finalize_cancellation' OR (EXISTS (SELECT 1 FROM matching_restore_plan) AND NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation) AND NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle) AND NOT EXISTS (SELECT 1 FROM unrelated_active_restore_plan))) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation"
}

func restoreRunOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "WITH eligible_operation AS (" +
		"SELECT operation_id, namespace_id, repo_id, created_at, phase, input_summary FROM operations WHERE operation_id = $1 " +
		"AND operation_type = 'restore_run' " +
		"AND phase IN ('validate_restore_run','restore_run_writer_fenced','restore_run_consuming') " +
		"AND (input_summary->>'preview_operation_id') IS NOT NULL AND btrim(input_summary->>'preview_operation_id') <> '' " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND phase = 'validate_restore_run' AND " + cancelFinalizableLease + ")" +
		") FOR UPDATE" +
		"), matching_restore_plan AS (" +
		"SELECT p.restore_plan_id FROM restore_plans p, eligible_operation e WHERE p.preview_operation_id = e.input_summary->>'preview_operation_id' " +
		"AND p.namespace_id = e.namespace_id AND p.repo_id = e.repo_id " +
		"AND ((e.phase IN ('validate_restore_run','restore_run_writer_fenced') AND p.status = 'pending') OR (e.phase = 'restore_run_consuming' AND p.status = 'consuming')) LIMIT 1 FOR UPDATE" +
		"), earlier_jvs_mutation AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), earlier_repo_lifecycle AS (" +
		"SELECT 1 FROM operations o, eligible_operation e WHERE o.repo_id = e.repo_id AND o.operation_id <> e.operation_id " +
		"AND (o.created_at < e.created_at OR (o.created_at = e.created_at AND o.operation_id < e.operation_id)) " +
		"AND o.operation_type IN (" + repoLifecycleAndPurgeOperationTypeSQLList() + ") AND o.operation_state NOT IN ('succeeded','failed','cancelled') LIMIT 1" +
		"), unrelated_active_restore_plan AS (" +
		"SELECT 1 FROM restore_plans p, eligible_operation e WHERE p.repo_id = e.repo_id AND p.namespace_id = e.namespace_id " +
		"AND p.preview_operation_id <> e.input_summary->>'preview_operation_id' " +
		"AND p.status IN (" + restorePlanActiveStatusSQLList() + ") LIMIT 1" +
		"), updated_operation AS (" +
		"UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 FROM eligible_operation WHERE operations.operation_id = eligible_operation.operation_id " +
		"AND ($5 <> 'finalize_cancellation' OR eligible_operation.phase = 'validate_restore_run') " +
		"AND ($5 = 'finalize_cancellation' OR (EXISTS (SELECT 1 FROM matching_restore_plan) AND NOT EXISTS (SELECT 1 FROM earlier_jvs_mutation) AND NOT EXISTS (SELECT 1 FROM earlier_repo_lifecycle) AND NOT EXISTS (SELECT 1 FROM unrelated_active_restore_plan))) RETURNING " + operationReturningColumnsSQL() +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation"
}

func templateCreateOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type = 'template_create' " +
		"AND phase IN ('validate_template_create','template_create_writer_fenced') " +
		"AND (" +
		"(operation_state = 'queued' AND phase = 'validate_template_create' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND phase = 'validate_template_create' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func templateCloneOperationAcquireLeaseSQL() string {
	return scopedOperationAcquireLeaseSQL("template_clone", operations.OperationPhaseTemplateCloneValidate)
}

func workloadMountBindingOperationAcquireLeaseSQL() string {
	cancelFinalizableLease := "((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4))"
	return "UPDATE operations SET " +
		"operation_state = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN 'cancelled' ELSE 'running' END, " +
		"attempt = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN attempt ELSE attempt + 1 END, " +
		"lease_owner = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL ELSE $2 END, " +
		"lease_expires_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN NULL::timestamptz ELSE $3::timestamptz END, " +
		"started_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN started_at ELSE COALESCE(started_at, $4) END, " +
		"finished_at = CASE WHEN operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' THEN COALESCE(finished_at, $4) ELSE finished_at END, " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 " +
		"AND operation_type IN ('mount_binding_create','mount_binding_status_update','mount_binding_heartbeat','mount_binding_release','mount_binding_revoke') " +
		"AND phase IN ('validate_mount_binding_create','validate_mount_binding_status_update','validate_mount_binding_heartbeat','validate_mount_binding_release','validate_mount_binding_revoke') " +
		"AND (" +
		"(operation_state = 'queued' AND $5 = '' AND lease_owner IS NULL AND lease_expires_at IS NULL) OR " +
		"(operation_state = 'running' AND $5 = '' AND lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $4) OR " +
		"(operation_state = 'cancel_requested' AND $5 = 'finalize_cancellation' AND " + cancelFinalizableLease + ")" +
		") RETURNING " + operationReturningColumnsSQL()
}

func repoJVSMutationOperationTypeSQLList() string {
	return "'save_point_create', 'restore_preview', 'restore_preview_discard', 'restore_run', 'template_create', 'template_clone'"
}

func repoHasNonTerminalJVSMutationSQL() string {
	return "SELECT EXISTS (SELECT 1 FROM operations WHERE repo_id = $1 AND operation_type IN (" + repoJVSMutationOperationTypeSQLList() + ") AND operation_state NOT IN ('succeeded','failed','cancelled'))"
}

func restoreRunExistsForPreviewOperationSQL() string {
	return "SELECT EXISTS (SELECT 1 FROM operations WHERE operation_type = 'restore_run' AND namespace_id = $1 AND repo_id = $2 AND resource_type = 'repo' AND resource_id = $2 AND input_summary->>'preview_operation_id' = $3 AND operation_state NOT IN ('failed','cancelled'))"
}

func repoLifecycleAndPurgeOperationTypeSQLList() string {
	return repoLifecycleOperationTypeSQLList() + ", 'repo_purge'"
}

func earlierNonTerminalRepoLifecycleOperationsSQL() string {
	return operationSelectSQL() + " WHERE repo_id = $1 AND (created_at < $3 OR (created_at = $3 AND operation_id < $2)) AND operation_type IN ('repo_archive','repo_restore_archived','repo_delete','repo_restore_tombstoned','repo_purge') AND operation_state NOT IN ('succeeded','failed','cancelled') ORDER BY created_at, operation_id"
}

func operationRenewLeaseSQL() string {
	return "UPDATE operations SET " +
		"lease_expires_at = GREATEST(lease_expires_at, $3), " +
		"updated_at = $4 " +
		"WHERE operation_id = $1 AND operation_state = 'running' AND lease_owner = $2 AND lease_expires_at IS NOT NULL AND lease_expires_at > $4 " +
		"RETURNING " + operationReturningColumnsSQL()
}

func operationLeaseFencedUpdateSQL() string {
	return operationLeaseFencedUpdateBaseSQL() + "RETURNING " + operationReturningColumnsSQL()
}

func operationLeaseFencedUpdateBaseSQL() string {
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
		"updated_at = $11 " +
		"WHERE operation_id = $12 AND operation_state = 'running' AND lease_owner = $13 AND lease_expires_at IS NOT NULL AND lease_expires_at > $11 "
}

func operationCommitWithLeaseSQL() string {
	return "WITH updated_operation AS (" +
		operationLeaseFencedUpdateBaseSQL() +
		"RETURNING " + operationReturningColumnsSQL() +
		"), inserted_audit AS (" +
		"INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") " +
		"SELECT " + placeholders(14, len(auditOutboxColumns)) + " FROM updated_operation " +
		"RETURNING audit_event_id" +
		") SELECT " + strings.Join(operationSelectColumns, ", ") + " FROM updated_operation WHERE EXISTS (SELECT 1 FROM inserted_audit)"
}

func operationInsertArgs(record operations.OperationRecord) ([]any, error) {
	externalResourceIDs, err := marshalObject(record.ExternalResourceIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal external_resource_ids: %w", err)
	}
	inputSummary, err := marshalObject(record.InputSummary)
	if err != nil {
		return nil, fmt.Errorf("marshal input_summary: %w", err)
	}
	jvsJSONOutput, err := marshalNullableJSON(record.JVSJSONOutput)
	if err != nil {
		return nil, fmt.Errorf("marshal jvs_json_output: %w", err)
	}
	verificationResult, err := marshalNullableJSON(record.VerificationResult)
	if err != nil {
		return nil, fmt.Errorf("marshal verification_result: %w", err)
	}
	errorJSON, err := marshalNullableJSON(record.Error)
	if err != nil {
		return nil, fmt.Errorf("marshal error_json: %w", err)
	}

	return []any{
		record.ID,
		string(record.Type),
		string(record.State),
		record.Phase,
		record.Attempt,
		leaseOwnerArg(record),
		leaseExpiresAtArg(record),
		record.IdempotencyScope,
		record.IdempotencyKey,
		string(record.RequestHash),
		record.CorrelationID,
		record.CallerService,
		record.AuthorizedActor.Type,
		record.AuthorizedActor.ID,
		record.Resource.Type,
		record.Resource.ID,
		record.NamespaceID,
		nullableStringArg(record.RepoID),
		nullableStringArg(record.TemplateID),
		nullableStringArg(record.ExportID),
		nullableStringArg(record.MountBindingID),
		nullableStringArg(record.SessionFenceID),
		externalResourceIDs,
		inputSummary,
		jvsJSONOutput,
		verificationResult,
		nullableStringArg(record.CompensationStatus),
		errorJSON,
		record.CreatedAt,
		timePtrArg(record.StartedAt),
		timePtrArg(record.FinishedAt),
		record.CreatedAt,
	}, nil
}

func operationUpdateArgs(record operations.OperationRecord, updatedAt any) ([]any, error) {
	externalResourceIDs, err := marshalObject(record.ExternalResourceIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal external_resource_ids: %w", err)
	}
	inputSummary, err := marshalObject(record.InputSummary)
	if err != nil {
		return nil, fmt.Errorf("marshal input_summary: %w", err)
	}
	jvsJSONOutput, err := marshalNullableJSON(record.JVSJSONOutput)
	if err != nil {
		return nil, fmt.Errorf("marshal jvs_json_output: %w", err)
	}
	verificationResult, err := marshalNullableJSON(record.VerificationResult)
	if err != nil {
		return nil, fmt.Errorf("marshal verification_result: %w", err)
	}
	errorJSON, err := marshalNullableJSON(record.Error)
	if err != nil {
		return nil, fmt.Errorf("marshal error_json: %w", err)
	}

	return []any{
		string(record.State),
		record.Phase,
		record.Attempt,
		leaseOwnerArg(record),
		leaseExpiresAtArg(record),
		externalResourceIDs,
		inputSummary,
		jvsJSONOutput,
		verificationResult,
		nullableStringArg(record.CompensationStatus),
		errorJSON,
		timePtrArg(record.StartedAt),
		timePtrArg(record.FinishedAt),
		updatedAt,
		record.ID,
	}, nil
}

type operationLeaseArgs struct {
	operationID  string
	owner        string
	expiresAt    time.Time
	now          time.Time
	recoveryMode string
	cancelPolicy string
}

func operationLeaseRequestArgs(operationID string, request operations.LeaseRequest) (operationLeaseArgs, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("operation_id", "missing operation id")
	}
	owner := strings.TrimSpace(request.Owner)
	if owner == "" {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("owner", "missing lease owner")
	}
	if request.Duration <= 0 {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("duration", "lease duration must be positive")
	}
	if request.Now.IsZero() {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("now", "lease decision time must be set")
	}
	if !validOperationLeaseRecoveryMode(request.RecoveryMode) {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("recovery_mode", fmt.Sprintf("unknown recovery mode %q", request.RecoveryMode))
	}
	if !validOperationLeaseCancelPolicy(request.CancelPolicy) {
		return operationLeaseArgs{}, operationLeaseInvalidRequest("cancel_policy", fmt.Sprintf("unknown cancel policy %q", request.CancelPolicy))
	}

	now := request.Now.UTC()
	return operationLeaseArgs{
		operationID:  operationID,
		owner:        owner,
		expiresAt:    now.Add(request.Duration),
		now:          now,
		recoveryMode: string(request.RecoveryMode),
		cancelPolicy: string(request.CancelPolicy),
	}, nil
}

func validOperationLeaseRecoveryMode(mode operations.LeaseRecoveryMode) bool {
	switch mode {
	case operations.LeaseRecoveryNone, operations.LeaseRecoveryExplicitAction:
		return true
	default:
		return false
	}
}

func validOperationLeaseCancelPolicy(policy operations.LeaseCancelPolicy) bool {
	switch policy {
	case operations.LeaseCancelPolicyNone, operations.LeaseCancelPolicyFinalize:
		return true
	default:
		return false
	}
}

func operationLeaseFencedUpdateArgs(record operations.OperationRecord, owner string, now time.Time) ([]any, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, operationLeaseInvalidRequest("owner", "missing lease owner")
	}
	operationID := strings.TrimSpace(record.ID)
	if operationID == "" {
		return nil, operationLeaseInvalidRequest("operation_id", "missing operation id")
	}
	if now.IsZero() {
		return nil, operationLeaseInvalidRequest("now", "lease fence time must be set")
	}
	if !validOperationLeaseFencedUpdateState(record.State) {
		return nil, operationLeaseInvalidRequest("operation_state", fmt.Sprintf("%q cannot be written through a lease-fenced worker update", record.State))
	}
	if recordOwner := strings.TrimSpace(record.LeaseOwner); recordOwner != "" && recordOwner != owner {
		return nil, operationLeaseInvalidRequest("lease_owner", "record lease owner differs from fence owner")
	}

	externalResourceIDs, err := marshalObject(record.ExternalResourceIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal external_resource_ids: %w", err)
	}
	inputSummary, err := marshalObject(record.InputSummary)
	if err != nil {
		return nil, fmt.Errorf("marshal input_summary: %w", err)
	}
	jvsJSONOutput, err := marshalNullableJSON(record.JVSJSONOutput)
	if err != nil {
		return nil, fmt.Errorf("marshal jvs_json_output: %w", err)
	}
	verificationResult, err := marshalNullableJSON(record.VerificationResult)
	if err != nil {
		return nil, fmt.Errorf("marshal verification_result: %w", err)
	}
	errorJSON, err := marshalNullableJSON(record.Error)
	if err != nil {
		return nil, fmt.Errorf("marshal error_json: %w", err)
	}

	return []any{
		string(record.State),
		record.Phase,
		externalResourceIDs,
		inputSummary,
		jvsJSONOutput,
		verificationResult,
		nullableStringArg(record.CompensationStatus),
		errorJSON,
		timePtrArg(record.StartedAt),
		timePtrArg(record.FinishedAt),
		now.UTC(),
		operationID,
		owner,
	}, nil
}

func auditOutboxInsertArgs(record audit.OutboxRecord) []any {
	return []any{
		record.EventID,
		string(record.EventType),
		record.EventTime,
		[]byte(record.PayloadJSON),
		string(record.Status),
		record.DeliveryAttempt,
		timePtrArg(record.NextRetryAt),
		record.LastError,
		record.CreatedAt,
		record.UpdatedAt,
		timePtrArg(record.DeliveredAt),
	}
}

func validOperationLeaseFencedUpdateState(state operations.OperationState) bool {
	switch state {
	case operations.OperationStateRunning,
		operations.OperationStateSucceeded,
		operations.OperationStateFailed,
		operations.OperationStateCancelled,
		operations.OperationStateOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

func operationLeaseInvalidRequest(field, reason string) error {
	return &operations.OperationLeaseError{
		Code:   operations.OperationLeaseErrorInvalidRequest,
		Field:  field,
		Reason: reason,
		Cause:  operations.ErrInvalidLeaseRequest,
	}
}

func operationLeaseUnavailable(action, operationID string, err error) error {
	return fmt.Errorf("%w: %s operation lease %q: %w", operations.ErrLeaseUnavailable, action, operationID, err)
}

func scanOperation(row rowScanner) (operations.OperationRecord, error) {
	return scanOperationWithInserted(row, nil)
}

func scanOperations(rows rowsScanner) (records []operations.OperationRecord, err error) {
	defer func() {
		closeErr := rows.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	for rows.Next() {
		record, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func scanOperationWithInserted(row rowScanner, inserted *bool) (operations.OperationRecord, error) {
	return scanOperationWithInsertedAndGate(row, inserted, nil)
}

func scanOperationWithInsertedAndGate(row rowScanner, inserted *bool, gateCode *string) (operations.OperationRecord, error) {
	var record operations.OperationRecord
	var operationType, operationState, requestHash string
	var leaseOwner, repoID, templateID, exportID, mountBindingID, sessionFenceID, compensationStatus sql.NullString
	var leaseExpiresAt, startedAt, finishedAt sql.NullTime
	var externalResourceIDsJSON, inputSummaryJSON, jvsJSONOutputJSON, verificationResultJSON, errorJSON []byte
	var insertedValue bool
	var gateCodeValue string

	dest := []any{
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
	if inserted != nil {
		dest = append(dest, &insertedValue)
	}
	if gateCode != nil {
		dest = append(dest, &gateCodeValue)
	}
	if err := row.Scan(dest...); err != nil {
		return operations.OperationRecord{}, err
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
		return operations.OperationRecord{}, fmt.Errorf("unmarshal external_resource_ids: %w", err)
	}
	if err := unmarshalObject(inputSummaryJSON, &record.InputSummary); err != nil {
		return operations.OperationRecord{}, fmt.Errorf("unmarshal input_summary: %w", err)
	}
	if err := unmarshalNullableJSON(jvsJSONOutputJSON, &record.JVSJSONOutput); err != nil {
		return operations.OperationRecord{}, fmt.Errorf("unmarshal jvs_json_output: %w", err)
	}
	if err := unmarshalNullableJSON(verificationResultJSON, &record.VerificationResult); err != nil {
		return operations.OperationRecord{}, fmt.Errorf("unmarshal verification_result: %w", err)
	}
	if len(errorJSON) > 0 {
		var opErr operations.OperationError
		if err := json.Unmarshal(errorJSON, &opErr); err != nil {
			return operations.OperationRecord{}, fmt.Errorf("unmarshal error_json: %w", err)
		}
		record.Error = &opErr
	}
	if inserted != nil {
		*inserted = insertedValue
	}
	if gateCode != nil {
		*gateCode = gateCodeValue
	}

	return record.Sanitized(), nil
}

func placeholders(start, count int) string {
	parts := make([]string, count)
	for idx := range parts {
		parts[idx] = fmt.Sprintf("$%d", start+idx)
	}
	return strings.Join(parts, ", ")
}

func marshalObject(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if string(encoded) == "null" {
		return []byte("{}"), nil
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("value must marshal to a JSON object")
	}
	return encoded, nil
}

func marshalNullableJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func unmarshalObject(data []byte, dest any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	return json.Unmarshal(data, dest)
}

func unmarshalNullableJSON(data []byte, dest *any) error {
	if len(data) == 0 {
		*dest = nil
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*dest = value
	return nil
}

func nullableStringArg(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func leaseOwnerArg(record operations.OperationRecord) any {
	if record.LeaseOwner == "" && record.LeaseExpiresAt == nil {
		return nil
	}
	return record.LeaseOwner
}

func leaseExpiresAtArg(record operations.OperationRecord) any {
	if record.LeaseOwner == "" && record.LeaseExpiresAt == nil {
		return nil
	}
	if record.LeaseExpiresAt == nil {
		return nil
	}
	return *record.LeaseExpiresAt
}

func timePtrArg(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
