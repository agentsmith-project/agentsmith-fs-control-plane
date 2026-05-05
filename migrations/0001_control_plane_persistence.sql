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

COMMIT;
