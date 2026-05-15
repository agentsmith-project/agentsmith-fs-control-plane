BEGIN;

ALTER TABLE operations
    DROP CONSTRAINT IF EXISTS operations_operation_type_check;

ALTER TABLE operations
    ADD CONSTRAINT operations_operation_type_check CHECK (
        operation_type IN (
            'volume_ensure',
            'namespace_upsert',
            'namespace_disable',
            'namespace_volume_binding_put',
            'repo_create',
            'repo_archive',
            'repo_restore_archived',
            'repo_delete',
            'repo_restore_tombstoned',
            'repo_purge',
            'save_point_create',
            'restore',
            'restore_preview',
            'restore_preview_discard',
            'restore_run',
            'template_create',
            'template_clone',
            'export_create',
            'export_revoke',
            'export_session_reconcile',
            'mount_binding_create',
            'mount_binding_status_update',
            'mount_binding_heartbeat',
            'mount_binding_release',
            'mount_binding_revoke',
            'migration_cutover'
        )
    );

DROP INDEX IF EXISTS operations_one_non_terminal_jvs_mutation_per_repo_idx;

CREATE UNIQUE INDEX IF NOT EXISTS operations_one_non_terminal_jvs_mutation_per_repo_idx
    ON operations (repo_id)
    WHERE repo_id IS NOT NULL
        AND operation_type IN (
            'save_point_create',
            'restore',
            'restore_preview',
            'restore_preview_discard',
            'restore_run',
            'template_create',
            'template_clone'
        )
        AND operation_state NOT IN ('succeeded', 'failed', 'cancelled');

COMMIT;
