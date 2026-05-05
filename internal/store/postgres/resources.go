package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

var volumeColumns = []string{
	"volume_id",
	"backend",
	"isolation_class",
	"status",
	"capabilities",
	"created_at",
	"updated_at",
}

var namespaceColumns = []string{
	"namespace_id",
	"status",
	"disabled_reason",
	"disabled_at",
	"created_at",
	"updated_at",
}

var namespaceVolumeBindingColumns = []string{
	"namespace_id",
	"default_volume_id",
	"allowed_callers",
	"quota_bytes_default",
	"export_policy",
	"lifecycle_policy",
	"mount_policy",
	"template_policy",
	"status",
	"created_at",
	"updated_at",
}

var repoColumns = []string{
	"repo_id",
	"namespace_id",
	"volume_id",
	"jvs_repo_id",
	"repo_kind",
	"status",
	"control_volume_subdir",
	"payload_volume_subdir",
	"lifecycle_status",
	"retention_expires_at",
	"last_lifecycle_operation_id",
	"pre_delete_status",
	"created_at",
	"updated_at",
}

func (store *Store) UpsertVolume(ctx context.Context, volume resources.Volume) error {
	if err := volume.Validate(); err != nil {
		return err
	}
	args, err := volumeArgs(volume)
	if err != nil {
		return err
	}
	_, err = store.exec.ExecContext(ctx, volumeUpsertSQL(), args...)
	return err
}

func (store *Store) GetVolume(ctx context.Context, volumeID string) (resources.Volume, error) {
	row := store.exec.QueryRowContext(ctx, volumeSelectSQL()+" WHERE volume_id = $1", volumeID)
	return scanVolume(row)
}

func (store *Store) ListActiveVolumes(ctx context.Context) (volumes []resources.Volume, err error) {
	rows, err := store.exec.QueryContext(ctx, volumeSelectSQL()+" WHERE status = $1 ORDER BY volume_id", string(resources.VolumeStatusActive))
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		volume, err := scanVolume(rows)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, volume)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return volumes, nil
}

func (store *Store) UpsertNamespace(ctx context.Context, namespace resources.Namespace) error {
	if err := namespace.Validate(); err != nil {
		return err
	}
	_, err := store.exec.ExecContext(ctx, namespaceUpsertSQL(), namespaceArgs(namespace)...)
	return err
}

func (store *Store) DisableNamespace(ctx context.Context, namespaceID, reason string) (resources.Namespace, error) {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		return resources.Namespace{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return resources.Namespace{}, fmt.Errorf("disable namespace reason must not be empty")
	}
	now := store.now()
	row := store.exec.QueryRowContext(ctx, namespaceDisableSQL(), namespaceID, reason, now, now)
	return scanNamespace(row)
}

func (store *Store) GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error) {
	row := store.exec.QueryRowContext(ctx, namespaceSelectSQL()+" WHERE namespace_id = $1", namespaceID)
	return scanNamespace(row)
}

func (store *Store) PutNamespaceVolumeBinding(ctx context.Context, binding resources.NamespaceVolumeBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	args, err := namespaceVolumeBindingArgs(binding)
	if err != nil {
		return err
	}
	_, err = store.exec.ExecContext(ctx, namespaceVolumeBindingUpsertSQL(), args...)
	return err
}

func (store *Store) GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error) {
	row := store.exec.QueryRowContext(ctx, namespaceVolumeBindingSelectSQL()+" WHERE namespace_id = $1", namespaceID)
	return scanNamespaceVolumeBinding(row)
}

func (store *Store) CreateRepo(ctx context.Context, repo resources.Repo) error {
	if err := repo.Validate(); err != nil {
		return err
	}
	_, err := store.exec.ExecContext(ctx, repoInsertSQL(), repoArgs(repo)...)
	return err
}

func (store *Store) GetRepo(ctx context.Context, repoID string) (resources.Repo, error) {
	row := store.exec.QueryRowContext(ctx, repoSelectSQL()+" WHERE repo_id = $1", repoID)
	return scanRepo(row)
}

func (store *Store) GetRepoInNamespace(ctx context.Context, namespaceID, repoID string) (resources.Repo, error) {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		return resources.Repo{}, err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		return resources.Repo{}, err
	}
	row := store.exec.QueryRowContext(ctx, repoSelectSQL()+" WHERE namespace_id = $1 AND repo_id = $2", namespaceID, repoID)
	return scanRepo(row)
}

func (store *Store) ListReposByNamespace(ctx context.Context, namespaceID string) (repos []resources.Repo, err error) {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		return nil, err
	}
	rows, err := store.exec.QueryContext(ctx, repoSelectSQL()+" WHERE namespace_id = $1 ORDER BY created_at, repo_id", namespaceID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		repo, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return repos, nil
}

func (store *Store) ListReposForRecoveryInspection(ctx context.Context) (repos []resources.Repo, err error) {
	statuses := repoRecoveryInspectionCandidateStatuses()
	args := make([]any, len(statuses))
	for idx, status := range statuses {
		args[idx] = string(status)
	}

	rows, err := store.exec.QueryContext(ctx, repoRecoveryInspectionCandidatesSQL(), args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		repo, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return repos, nil
}

func (store *Store) UpdateRepoLifecycle(ctx context.Context, repoID string, lifecycle resources.RepoLifecycle) (resources.Repo, error) {
	if err := validateRepoOrTemplateID(repoID); err != nil {
		return resources.Repo{}, err
	}
	if err := lifecycle.Validate(); err != nil {
		return resources.Repo{}, err
	}
	row := store.exec.QueryRowContext(ctx, repoLifecycleUpdateSQL(),
		string(lifecycle.Status),
		string(lifecycle.Status),
		timePtrArg(lifecycle.RetentionExpiresAt),
		nullableStringArg(lifecycle.LastLifecycleOperationID),
		nullableStringArg(string(lifecycle.PreDeleteStatus)),
		store.now(),
		repoID,
	)
	return scanRepo(row)
}

func validateRepoOrTemplateID(id string) error {
	if err := pathresolver.ValidateID(pathresolver.RepoID, id); err == nil {
		return nil
	}
	if err := pathresolver.ValidateID(pathresolver.TemplateID, id); err == nil {
		return nil
	}
	return fmt.Errorf("invalid repo or template id %q", id)
}

func volumeUpsertSQL() string {
	return "INSERT INTO volumes (" + strings.Join(volumeColumns, ", ") + ") VALUES (" + placeholders(1, len(volumeColumns)) + ") " +
		"ON CONFLICT (volume_id) DO UPDATE SET " +
		"backend = EXCLUDED.backend, " +
		"isolation_class = EXCLUDED.isolation_class, " +
		"status = EXCLUDED.status, " +
		"capabilities = EXCLUDED.capabilities, " +
		"updated_at = EXCLUDED.updated_at"
}

func volumeSelectSQL() string {
	return "SELECT " + strings.Join(volumeColumns, ", ") + " FROM volumes"
}

func namespaceUpsertSQL() string {
	return "INSERT INTO namespaces (" + strings.Join(namespaceColumns, ", ") + ") VALUES (" + placeholders(1, len(namespaceColumns)) + ") " +
		"ON CONFLICT (namespace_id) DO UPDATE SET " +
		"status = CASE WHEN namespaces.status = 'disabled' THEN namespaces.status ELSE EXCLUDED.status END, " +
		"disabled_reason = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_reason ELSE EXCLUDED.disabled_reason END, " +
		"disabled_at = CASE WHEN namespaces.status = 'disabled' THEN namespaces.disabled_at ELSE EXCLUDED.disabled_at END, " +
		"updated_at = EXCLUDED.updated_at"
}

func namespaceDisableSQL() string {
	return "UPDATE namespaces SET " +
		"status = 'disabled', " +
		"disabled_reason = CASE WHEN status = 'disabled' THEN disabled_reason ELSE $2 END, " +
		"disabled_at = CASE WHEN status = 'disabled' THEN disabled_at ELSE $3 END, " +
		"updated_at = $4 " +
		"WHERE namespace_id = $1 " +
		"RETURNING " + strings.Join(namespaceColumns, ", ")
}

func namespaceSelectSQL() string {
	return "SELECT " + strings.Join(namespaceColumns, ", ") + " FROM namespaces"
}

func namespaceVolumeBindingUpsertSQL() string {
	return "INSERT INTO namespace_volume_bindings (" + strings.Join(namespaceVolumeBindingColumns, ", ") + ") VALUES (" + placeholders(1, len(namespaceVolumeBindingColumns)) + ") " +
		"ON CONFLICT (namespace_id) DO UPDATE SET " +
		"default_volume_id = EXCLUDED.default_volume_id, " +
		"allowed_callers = EXCLUDED.allowed_callers, " +
		"quota_bytes_default = EXCLUDED.quota_bytes_default, " +
		"export_policy = EXCLUDED.export_policy, " +
		"lifecycle_policy = EXCLUDED.lifecycle_policy, " +
		"mount_policy = EXCLUDED.mount_policy, " +
		"template_policy = EXCLUDED.template_policy, " +
		"status = EXCLUDED.status, " +
		"updated_at = EXCLUDED.updated_at"
}

func namespaceVolumeBindingSelectSQL() string {
	return "SELECT " + strings.Join(namespaceVolumeBindingColumns, ", ") + " FROM namespace_volume_bindings"
}

func repoInsertSQL() string {
	return "INSERT INTO repos (" + strings.Join(repoColumns, ", ") + ") VALUES (" + placeholders(1, len(repoColumns)) + ")"
}

func repoSelectSQL() string {
	return "SELECT " + strings.Join(repoColumns, ", ") + " FROM repos"
}

func repoRecoveryInspectionCandidatesSQL() string {
	return repoSelectSQL() +
		" WHERE lifecycle_status IN (" + placeholders(1, len(repoRecoveryInspectionCandidateStatuses())) + ")" +
		" ORDER BY updated_at, repo_id"
}

func repoRecoveryInspectionCandidateStatuses() []resources.RepoStatus {
	return []resources.RepoStatus{
		resources.RepoStatusArchiving,
		resources.RepoStatusRestoringArchived,
		resources.RepoStatusDeleting,
		resources.RepoStatusRestoringTombstoned,
		resources.RepoStatusPurging,
		resources.RepoStatusOperatorInterventionRequired,
	}
}

func repoLifecycleUpdateSQL() string {
	return "UPDATE repos SET " +
		"status = $1, " +
		"lifecycle_status = $2, " +
		"retention_expires_at = $3, " +
		"last_lifecycle_operation_id = $4, " +
		"pre_delete_status = $5, " +
		"updated_at = $6 " +
		"WHERE repo_id = $7 " +
		"RETURNING " + strings.Join(repoColumns, ", ")
}

func volumeArgs(volume resources.Volume) ([]any, error) {
	capabilities, err := marshalObject(volume.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("marshal capabilities: %w", err)
	}
	return []any{
		volume.ID,
		string(volume.Backend),
		string(volume.IsolationClass),
		string(volume.Status),
		capabilities,
		volume.CreatedAt,
		volume.UpdatedAt,
	}, nil
}

func namespaceArgs(namespace resources.Namespace) []any {
	return []any{
		namespace.ID,
		string(namespace.Status),
		nullableStringArg(namespace.DisabledReason),
		timePtrArg(namespace.DisabledAt),
		namespace.CreatedAt,
		namespace.UpdatedAt,
	}
}

func namespaceVolumeBindingArgs(binding resources.NamespaceVolumeBinding) ([]any, error) {
	allowedCallers, err := json.Marshal(binding.AllowedCallers)
	if err != nil {
		return nil, fmt.Errorf("marshal allowed_callers: %w", err)
	}
	exportPolicy, err := marshalObject(binding.ExportPolicy)
	if err != nil {
		return nil, fmt.Errorf("marshal export_policy: %w", err)
	}
	lifecyclePolicy, err := marshalObject(binding.LifecyclePolicy)
	if err != nil {
		return nil, fmt.Errorf("marshal lifecycle_policy: %w", err)
	}
	mountPolicy, err := marshalObject(binding.MountPolicy)
	if err != nil {
		return nil, fmt.Errorf("marshal mount_policy: %w", err)
	}
	templatePolicy, err := marshalObject(binding.TemplatePolicy)
	if err != nil {
		return nil, fmt.Errorf("marshal template_policy: %w", err)
	}
	return []any{
		binding.NamespaceID,
		binding.DefaultVolumeID,
		allowedCallers,
		binding.QuotaBytesDefault,
		exportPolicy,
		lifecyclePolicy,
		mountPolicy,
		templatePolicy,
		string(binding.Status),
		binding.CreatedAt,
		binding.UpdatedAt,
	}, nil
}

func repoArgs(repo resources.Repo) []any {
	return []any{
		repo.ID,
		repo.NamespaceID,
		repo.VolumeID,
		repo.JVSRepoID,
		string(repo.Kind),
		string(repo.Status),
		repo.ControlVolumeSubdir,
		repo.PayloadVolumeSubdir,
		string(repo.Lifecycle.Status),
		timePtrArg(repo.Lifecycle.RetentionExpiresAt),
		nullableStringArg(repo.Lifecycle.LastLifecycleOperationID),
		nullableStringArg(string(repo.Lifecycle.PreDeleteStatus)),
		repo.CreatedAt,
		repo.UpdatedAt,
	}
}

func scanVolume(row rowScanner) (resources.Volume, error) {
	var volume resources.Volume
	var backend, isolationClass, status string
	var capabilitiesJSON []byte
	if err := row.Scan(
		&volume.ID,
		&backend,
		&isolationClass,
		&status,
		&capabilitiesJSON,
		&volume.CreatedAt,
		&volume.UpdatedAt,
	); err != nil {
		return resources.Volume{}, err
	}
	volume.Backend = resources.VolumeBackend(backend)
	volume.IsolationClass = resources.VolumeIsolationClass(isolationClass)
	volume.Status = resources.VolumeStatus(status)
	if err := unmarshalObject(capabilitiesJSON, &volume.Capabilities); err != nil {
		return resources.Volume{}, fmt.Errorf("unmarshal capabilities: %w", err)
	}
	if err := volume.Validate(); err != nil {
		return resources.Volume{}, err
	}
	return volume, nil
}

func scanNamespace(row rowScanner) (resources.Namespace, error) {
	var namespace resources.Namespace
	var status string
	var disabledReason sql.NullString
	var disabledAt sql.NullTime
	if err := row.Scan(
		&namespace.ID,
		&status,
		&disabledReason,
		&disabledAt,
		&namespace.CreatedAt,
		&namespace.UpdatedAt,
	); err != nil {
		return resources.Namespace{}, err
	}
	namespace.Status = resources.NamespaceStatus(status)
	namespace.DisabledReason = nullStringValue(disabledReason)
	namespace.DisabledAt = nullTimePtr(disabledAt)
	if err := namespace.Validate(); err != nil {
		return resources.Namespace{}, err
	}
	return namespace, nil
}

func scanNamespaceVolumeBinding(row rowScanner) (resources.NamespaceVolumeBinding, error) {
	var binding resources.NamespaceVolumeBinding
	var allowedCallersJSON, exportPolicyJSON, lifecyclePolicyJSON, mountPolicyJSON, templatePolicyJSON []byte
	var status string
	if err := row.Scan(
		&binding.NamespaceID,
		&binding.DefaultVolumeID,
		&allowedCallersJSON,
		&binding.QuotaBytesDefault,
		&exportPolicyJSON,
		&lifecyclePolicyJSON,
		&mountPolicyJSON,
		&templatePolicyJSON,
		&status,
		&binding.CreatedAt,
		&binding.UpdatedAt,
	); err != nil {
		return resources.NamespaceVolumeBinding{}, err
	}
	if err := json.Unmarshal(allowedCallersJSON, &binding.AllowedCallers); err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("unmarshal allowed_callers: %w", err)
	}
	if err := unmarshalObject(exportPolicyJSON, &binding.ExportPolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("unmarshal export_policy: %w", err)
	}
	if err := unmarshalObject(lifecyclePolicyJSON, &binding.LifecyclePolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("unmarshal lifecycle_policy: %w", err)
	}
	if err := unmarshalObject(mountPolicyJSON, &binding.MountPolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("unmarshal mount_policy: %w", err)
	}
	if err := unmarshalObject(templatePolicyJSON, &binding.TemplatePolicy); err != nil {
		return resources.NamespaceVolumeBinding{}, fmt.Errorf("unmarshal template_policy: %w", err)
	}
	binding.Status = resources.NamespaceStatus(status)
	if err := binding.Validate(); err != nil {
		return resources.NamespaceVolumeBinding{}, err
	}
	return binding, nil
}

func scanRepo(row rowScanner) (resources.Repo, error) {
	var repo resources.Repo
	var kind, status, lifecycleStatus string
	var retentionExpiresAt sql.NullTime
	var lastLifecycleOperationID, preDeleteStatus sql.NullString
	if err := row.Scan(
		&repo.ID,
		&repo.NamespaceID,
		&repo.VolumeID,
		&repo.JVSRepoID,
		&kind,
		&status,
		&repo.ControlVolumeSubdir,
		&repo.PayloadVolumeSubdir,
		&lifecycleStatus,
		&retentionExpiresAt,
		&lastLifecycleOperationID,
		&preDeleteStatus,
		&repo.CreatedAt,
		&repo.UpdatedAt,
	); err != nil {
		return resources.Repo{}, err
	}
	repo.Kind = resources.RepoKind(kind)
	repo.Status = resources.RepoStatus(status)
	repo.Lifecycle = resources.RepoLifecycle{
		Status:                   resources.RepoStatus(lifecycleStatus),
		RetentionExpiresAt:       nullTimePtr(retentionExpiresAt),
		LastLifecycleOperationID: nullStringValue(lastLifecycleOperationID),
		PreDeleteStatus:          resources.RepoStatus(nullStringValue(preDeleteStatus)),
	}
	if err := repo.Validate(); err != nil {
		return resources.Repo{}, err
	}
	return repo, nil
}
