CREATE TABLE IF NOT EXISTS restore_reconciliation_runs (
    run_id text PRIMARY KEY,
    mode text NOT NULL,
    reason text NOT NULL DEFAULT '',
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone,
    CONSTRAINT restore_reconciliation_runs_mode_check CHECK (
        mode IN ('reconciling', 'blocked_operator_intervention', 'completed')
    )
);

CREATE TABLE IF NOT EXISTS restore_reconciliation_targets (
    run_id text NOT NULL REFERENCES restore_reconciliation_runs (run_id),
    repo_id text NOT NULL,
    namespace_id text NOT NULL,
    expected_repo_status text NOT NULL,
    expected_storage_generation text NOT NULL,
    expected_snapshot_id text NOT NULL,
    expected_tombstone_marker text NOT NULL,
    expected_purge_marker text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    PRIMARY KEY (run_id, repo_id),
    CONSTRAINT restore_reconciliation_targets_repo_fk FOREIGN KEY (namespace_id, repo_id)
        REFERENCES repos (namespace_id, repo_id)
);

CREATE TABLE IF NOT EXISTS restore_reconciliation_observations (
    run_id text NOT NULL REFERENCES restore_reconciliation_runs (run_id),
    repo_id text NOT NULL,
    namespace_id text NOT NULL,
    expected_repo_status text NOT NULL,
    observed_storage_present boolean NOT NULL,
    observed_generation text NOT NULL DEFAULT '',
    observed_snapshot_id text NOT NULL DEFAULT '',
    observed_tombstone_marker text NOT NULL DEFAULT '',
    observed_purge_marker text NOT NULL DEFAULT '',
    result text NOT NULL,
    reason text NOT NULL DEFAULT '',
    evidence_ref text NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    PRIMARY KEY (run_id, repo_id),
    CONSTRAINT restore_reconciliation_observations_repo_fk FOREIGN KEY (namespace_id, repo_id)
        REFERENCES repos (namespace_id, repo_id),
    CONSTRAINT restore_reconciliation_observations_result_check CHECK (
        result IN ('clean', 'mismatch')
    )
);

CREATE INDEX IF NOT EXISTS restore_reconciliation_runs_active_idx
    ON restore_reconciliation_runs (mode, created_at)
    WHERE mode IN ('reconciling', 'blocked_operator_intervention');
