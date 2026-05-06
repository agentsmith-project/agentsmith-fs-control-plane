BEGIN;

CREATE TABLE IF NOT EXISTS operations (
    operation_id text PRIMARY KEY,
    operation_type text NOT NULL,
    operation_state text NOT NULL,
    phase text NOT NULL,
    attempt integer NOT NULL DEFAULT 0,
    lease_owner text,
    lease_expires_at timestamp with time zone,
    idempotency_scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    correlation_id text NOT NULL,
    caller_service text NOT NULL,
    authorized_actor_type text NOT NULL,
    authorized_actor_id text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    namespace_id text NOT NULL DEFAULT '',
    repo_id text,
    template_id text,
    export_id text,
    mount_binding_id text,
    session_fence_id text,
    external_resource_ids jsonb NOT NULL DEFAULT '{}'::jsonb,
    input_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    jvs_json_output jsonb,
    verification_result jsonb,
    compensation_status text,
    error_json jsonb,
    created_at timestamp with time zone NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT operations_operation_type_check CHECK (
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
    ),
    CONSTRAINT operations_operation_state_check CHECK (
        operation_state IN (
            'queued',
            'running',
            'succeeded',
            'failed',
            'cancel_requested',
            'cancelled',
            'operator_intervention_required'
        )
    ),
    CONSTRAINT operations_attempt_non_negative CHECK (attempt >= 0),
    CONSTRAINT operations_phase_non_empty CHECK (phase <> ''),
    CONSTRAINT operations_idempotency_boundary_present CHECK (
        caller_service <> ''
        AND operation_type <> ''
        AND idempotency_key <> ''
        AND request_hash <> ''
    ),
    CONSTRAINT operations_external_resource_ids_object CHECK (jsonb_typeof(external_resource_ids) = 'object'),
    CONSTRAINT operations_input_summary_object CHECK (jsonb_typeof(input_summary) = 'object'),
    CONSTRAINT operations_lease_pair_check CHECK (
        (lease_owner IS NULL AND lease_expires_at IS NULL)
        OR (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT operations_idempotency_unique UNIQUE (
        caller_service,
        namespace_id,
        operation_type,
        idempotency_key
    )
);

CREATE INDEX IF NOT EXISTS operations_state_lease_idx
    ON operations (operation_state, lease_expires_at);

CREATE INDEX IF NOT EXISTS operations_correlation_idx
    ON operations (correlation_id);

CREATE INDEX IF NOT EXISTS operations_repo_idx
    ON operations (repo_id)
    WHERE repo_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS operations_one_non_terminal_jvs_mutation_per_repo_idx
    ON operations (repo_id)
    WHERE repo_id IS NOT NULL
        AND operation_type IN (
            'save_point_create',
            'restore_preview',
            'restore_preview_discard',
            'restore_run',
            'template_create',
            'template_clone'
        )
        AND operation_state NOT IN ('succeeded', 'failed', 'cancelled');

CREATE UNIQUE INDEX IF NOT EXISTS operations_restore_run_one_per_preview_idx
    ON operations (namespace_id, repo_id, (input_summary->>'preview_operation_id'))
    WHERE operation_type = 'restore_run'
        AND operation_state NOT IN ('failed', 'cancelled')
        AND (input_summary->>'preview_operation_id') IS NOT NULL
        AND btrim(input_summary->>'preview_operation_id') <> '';

CREATE TABLE IF NOT EXISTS audit_outbox (
    audit_event_id text PRIMARY KEY,
    event_type text NOT NULL,
    event_time timestamp with time zone NOT NULL,
    payload_json jsonb NOT NULL,
    delivery_status text NOT NULL DEFAULT 'pending',
    delivery_attempt integer NOT NULL DEFAULT 0,
    next_retry_at timestamp with time zone,
    last_error text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    delivered_at timestamp with time zone,
    CONSTRAINT audit_outbox_payload_object CHECK (jsonb_typeof(payload_json) = 'object'),
    CONSTRAINT audit_outbox_delivery_status_check CHECK (
        delivery_status IN (
            'pending',
            'delivering',
            'delivered',
            'retry_wait',
            'failed'
        )
    ),
    CONSTRAINT audit_outbox_delivery_attempt_non_negative CHECK (delivery_attempt >= 0)
);

CREATE INDEX IF NOT EXISTS audit_outbox_delivery_idx
    ON audit_outbox (delivery_status, next_retry_at, event_time);

CREATE TABLE IF NOT EXISTS repo_fences (
    fence_id text PRIMARY KEY,
    repo_id text NOT NULL,
    fence_kind text NOT NULL,
    holder_operation_id text NOT NULL REFERENCES operations (operation_id),
    status text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    released_at timestamp with time zone,
    recovery_operation_id text REFERENCES operations (operation_id),
    recovery_reason text,
    recovery_started_at timestamp with time zone,
    recovered_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT repo_fences_kind_check CHECK (
        fence_kind IN (
            'writer_session',
            'lifecycle'
        )
    ),
    CONSTRAINT repo_fences_status_check CHECK (
        status IN (
            'active',
            'released',
            'expired',
            'recovery_required',
            'recovered'
        )
    ),
    CONSTRAINT repo_fences_lifecycle_check CHECK (
        (
            status IN ('active', 'expired', 'recovery_required')
            AND released_at IS NULL
            AND recovered_at IS NULL
        )
        OR (
            status = 'released'
            AND released_at IS NOT NULL
            AND recovered_at IS NULL
        )
        OR (
            status = 'recovered'
            AND released_at IS NOT NULL
            AND recovered_at IS NOT NULL
            AND recovery_started_at IS NOT NULL
        )
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS repo_fences_held_unique
    ON repo_fences (repo_id, fence_kind)
    WHERE released_at IS NULL;

CREATE INDEX IF NOT EXISTS repo_fences_holder_operation_idx
    ON repo_fences (holder_operation_id);

CREATE INDEX IF NOT EXISTS repo_fences_expiry_idx
    ON repo_fences (expires_at)
    WHERE released_at IS NULL;

CREATE TABLE IF NOT EXISTS volumes (
    volume_id text PRIMARY KEY,
    backend text NOT NULL,
    isolation_class text NOT NULL,
    status text NOT NULL,
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT volumes_backend_check CHECK (
        backend IN ('juicefs')
    ),
    CONSTRAINT volumes_isolation_class_check CHECK (
        isolation_class IN ('shared', 'dedicated')
    ),
    CONSTRAINT volumes_status_check CHECK (
        status IN ('active', 'disabled', 'degraded')
    ),
    CONSTRAINT volumes_capabilities_object CHECK (jsonb_typeof(capabilities) = 'object')
);

CREATE TABLE IF NOT EXISTS namespaces (
    namespace_id text PRIMARY KEY,
    status text NOT NULL DEFAULT 'active',
    disabled_reason text,
    disabled_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT namespaces_status_check CHECK (
        status IN ('active', 'disabled')
    ),
    CONSTRAINT namespaces_disable_metadata_check CHECK (
        (
            status = 'active'
            AND disabled_at IS NULL
        )
        OR (
            status = 'disabled'
            AND disabled_at IS NOT NULL
        )
    )
);

CREATE TABLE IF NOT EXISTS namespace_volume_bindings (
    namespace_id text PRIMARY KEY REFERENCES namespaces (namespace_id),
    default_volume_id text NOT NULL REFERENCES volumes (volume_id),
    allowed_callers jsonb NOT NULL DEFAULT '[]'::jsonb,
    quota_bytes_default bigint NOT NULL DEFAULT 0,
    export_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    lifecycle_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    mount_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    template_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT namespace_volume_bindings_status_check CHECK (
        status IN ('active', 'disabled')
    ),
    CONSTRAINT namespace_volume_bindings_allowed_callers_array CHECK (jsonb_typeof(allowed_callers) = 'array'),
    CONSTRAINT namespace_volume_bindings_export_policy_object CHECK (jsonb_typeof(export_policy) = 'object'),
    CONSTRAINT namespace_volume_bindings_lifecycle_policy_object CHECK (jsonb_typeof(lifecycle_policy) = 'object'),
    CONSTRAINT namespace_volume_bindings_mount_policy_object CHECK (jsonb_typeof(mount_policy) = 'object'),
    CONSTRAINT namespace_volume_bindings_template_policy_object CHECK (jsonb_typeof(template_policy) = 'object'),
    CONSTRAINT namespace_volume_bindings_quota_non_negative CHECK (quota_bytes_default >= 0)
);

CREATE TABLE IF NOT EXISTS repos (
    repo_id text PRIMARY KEY,
    namespace_id text NOT NULL REFERENCES namespaces (namespace_id),
    volume_id text NOT NULL REFERENCES volumes (volume_id),
    jvs_repo_id text NOT NULL,
    repo_kind text NOT NULL,
    status text NOT NULL,
    control_volume_subdir text NOT NULL,
    payload_volume_subdir text NOT NULL,
    lifecycle_status text NOT NULL,
    retention_expires_at timestamp with time zone,
    last_lifecycle_operation_id text REFERENCES operations (operation_id),
    pre_delete_status text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT repos_namespace_repo_unique UNIQUE (namespace_id, repo_id),
    CONSTRAINT repos_kind_check CHECK (
        repo_kind IN ('repo', 'template')
    ),
    CONSTRAINT repos_status_check CHECK (
        status IN (
            'active',
            'archiving',
            'archived',
            'restoring_archived',
            'deleting',
            'tombstoned',
            'restoring_tombstoned',
            'purging',
            'purged',
            'operator_intervention_required'
        )
    ),
    CONSTRAINT repos_lifecycle_status_check CHECK (
        lifecycle_status IN (
            'active',
            'archiving',
            'archived',
            'restoring_archived',
            'deleting',
            'tombstoned',
            'restoring_tombstoned',
            'purging',
            'purged',
            'operator_intervention_required'
        )
    ),
    CONSTRAINT repos_pre_delete_status_check CHECK (
        pre_delete_status IS NULL
        OR pre_delete_status IN (
            'active',
            'archived'
        )
    ),
    CONSTRAINT repos_status_lifecycle_match CHECK (status = lifecycle_status),
    CONSTRAINT repos_canonical_subdir_check CHECK (
        (
            repo_kind = 'repo'
            AND control_volume_subdir = 'afscp/namespaces/' || namespace_id || '/repos/' || repo_id || '/control'
            AND payload_volume_subdir = 'afscp/namespaces/' || namespace_id || '/repos/' || repo_id || '/payload'
        )
        OR (
            repo_kind = 'template'
            AND control_volume_subdir = 'afscp/namespaces/' || namespace_id || '/templates/' || repo_id || '/control'
            AND payload_volume_subdir = 'afscp/namespaces/' || namespace_id || '/templates/' || repo_id || '/payload'
        )
    ),
    CONSTRAINT repos_pre_delete_required_check CHECK (
        (
            status IN ('deleting', 'tombstoned', 'restoring_tombstoned', 'purging', 'purged')
            AND pre_delete_status IS NOT NULL
        )
        OR (
            status IN ('active', 'archiving', 'archived', 'restoring_archived')
            AND pre_delete_status IS NULL
        )
        OR status = 'operator_intervention_required'
    ),
    CONSTRAINT repos_retention_metadata_check CHECK (
        (
            status IN ('tombstoned', 'restoring_tombstoned', 'purging')
            AND retention_expires_at IS NOT NULL
        )
        OR (
            status IN ('active', 'archiving', 'archived', 'restoring_archived')
            AND retention_expires_at IS NULL
        )
        OR status IN ('deleting', 'purged', 'operator_intervention_required')
    )
);

CREATE INDEX IF NOT EXISTS repos_namespace_idx
    ON repos (namespace_id, created_at, repo_id);

CREATE INDEX IF NOT EXISTS repos_volume_idx
    ON repos (volume_id);

CREATE TABLE IF NOT EXISTS restore_plans (
    restore_plan_id text PRIMARY KEY,
    namespace_id text NOT NULL,
    repo_id text NOT NULL,
    preview_operation_id text NOT NULL REFERENCES operations (operation_id),
    source_save_point_id text NOT NULL,
    status text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT restore_plans_preview_operation_unique UNIQUE (preview_operation_id),
    CONSTRAINT restore_plans_repo_fk FOREIGN KEY (namespace_id, repo_id)
        REFERENCES repos (namespace_id, repo_id),
    CONSTRAINT restore_plans_status_check CHECK (
        status IN (
            'pending',
            'consuming',
            'consumed',
            'discarding',
            'discarded',
            'operator_intervention_required'
        )
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS restore_plans_one_active_per_repo_idx
    ON restore_plans (repo_id)
    WHERE status IN ('pending', 'consuming', 'discarding', 'operator_intervention_required');

CREATE TABLE IF NOT EXISTS export_sessions (
    export_id text PRIMARY KEY,
    namespace_id text NOT NULL,
    repo_id text NOT NULL,
    access_mode text NOT NULL,
    status text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT export_sessions_repo_fk FOREIGN KEY (namespace_id, repo_id)
        REFERENCES repos (namespace_id, repo_id),
    CONSTRAINT export_sessions_access_mode_check CHECK (
        access_mode IN ('read_only', 'read_write')
    ),
    CONSTRAINT export_sessions_status_check CHECK (
        status IN ('active', 'revoking', 'revoked', 'expired', 'failed')
    )
);

CREATE INDEX IF NOT EXISTS export_sessions_repo_read_idx
    ON export_sessions (repo_id, created_at, export_id);

CREATE TABLE IF NOT EXISTS workload_mount_bindings (
    mount_binding_id text PRIMARY KEY,
    namespace_id text NOT NULL,
    repo_id text NOT NULL,
    read_only boolean NOT NULL,
    status text NOT NULL,
    lease_expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT workload_mount_bindings_repo_fk FOREIGN KEY (namespace_id, repo_id)
        REFERENCES repos (namespace_id, repo_id),
    CONSTRAINT workload_mount_bindings_status_check CHECK (
        status IN ('issued', 'pending', 'active', 'releasing', 'released', 'revoked', 'expired', 'failed')
    )
);

CREATE INDEX IF NOT EXISTS workload_mount_bindings_repo_read_idx
    ON workload_mount_bindings (repo_id, created_at, mount_binding_id);

COMMIT;
