CREATE UNIQUE INDEX IF NOT EXISTS export_sessions_export_namespace_repo_idx
    ON export_sessions (export_id, namespace_id, repo_id);

CREATE TABLE IF NOT EXISTS export_runtime_requests (
    runtime_request_id text PRIMARY KEY,
    export_id text NOT NULL,
    namespace_id text NOT NULL,
    repo_id text NOT NULL,
    request_state text NOT NULL,
    write_request boolean NOT NULL,
    started_at timestamp with time zone NOT NULL,
    last_heartbeat_at timestamp with time zone NOT NULL,
    heartbeat_expires_at timestamp with time zone NOT NULL,
    closed_at timestamp with time zone,
    close_reason text NOT NULL DEFAULT '',
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT export_runtime_requests_session_fk FOREIGN KEY (export_id, namespace_id, repo_id)
        REFERENCES export_sessions (export_id, namespace_id, repo_id),
    CONSTRAINT export_runtime_requests_repo_fk FOREIGN KEY (namespace_id, repo_id)
        REFERENCES repos (namespace_id, repo_id),
    CONSTRAINT export_runtime_requests_state_check CHECK (
        request_state IN ('open', 'closed', 'recovered')
    ),
    CONSTRAINT export_runtime_requests_time_check CHECK (
        last_heartbeat_at >= started_at
        AND heartbeat_expires_at >= last_heartbeat_at
        AND (closed_at IS NULL OR closed_at >= started_at)
        AND (closed_at IS NULL OR closed_at >= last_heartbeat_at)
    ),
    CONSTRAINT export_runtime_requests_terminal_check CHECK (
        (
            request_state = 'open'
            AND closed_at IS NULL
            AND close_reason = ''
        )
        OR (
            request_state IN ('closed', 'recovered')
            AND closed_at IS NOT NULL
            AND btrim(close_reason) <> ''
        )
    )
);

CREATE INDEX IF NOT EXISTS export_runtime_requests_export_state_idx
    ON export_runtime_requests (export_id, request_state);

CREATE INDEX IF NOT EXISTS export_runtime_requests_stale_open_idx
    ON export_runtime_requests (heartbeat_expires_at, runtime_request_id)
    WHERE request_state = 'open';
