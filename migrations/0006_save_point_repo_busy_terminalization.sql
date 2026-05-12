-- E_REPO_BUSY means the repo was actively accessed before JVS save side effects
-- were created. Older workers incorrectly left those save point operations in
-- operator_intervention_required, which permanently blocked save point history.
WITH repo_busy_candidates AS (
    SELECT
        operation_id,
        repo_id,
        correlation_id,
        COALESCE(
            NULLIF(error_json#>>'{details,jvs_exit_code}', ''),
            NULLIF(verification_result->>'jvs_exit_code', ''),
            NULLIF(jvs_json_output->>'jvs_exit_code', '')
        ) AS jvs_exit_code_text,
        CASE
            WHEN jsonb_typeof(error_json->'details') = 'object' THEN error_json->'details'
            ELSE '{}'::jsonb
        END AS existing_details
    FROM operations
    WHERE operation_type = 'save_point_create'
        AND operation_state = 'operator_intervention_required'
        AND lease_owner IS NULL
        AND lease_expires_at IS NULL
        AND COALESCE(session_fence_id, '') = ''
        AND (
            error_json#>>'{details,jvs_error_code}' = 'E_REPO_BUSY'
            OR verification_result#>>'{jvs_error_code}' = 'E_REPO_BUSY'
            OR jvs_json_output#>>'{jvs_error_code}' = 'E_REPO_BUSY'
        )
),
repo_busy_errors AS (
    SELECT
        operation_id,
        jsonb_build_object(
            'code', 'JVS_COMMAND_FAILED',
            'message', 'jvs save blocked by active repo access',
            'retryable', true,
            'correlation_id', correlation_id,
            'operation_id', operation_id,
            'details', jsonb_strip_nulls(
                (existing_details - 'jvs_exit_code')
                || jsonb_build_object(
                    'repo_id', repo_id,
                    'jvs_error_code', 'E_REPO_BUSY',
                    'jvs_command', 'save'
                )
                || CASE
                    WHEN jvs_exit_code_text ~ '^-?[0-9]+$' THEN jsonb_build_object('jvs_exit_code', jvs_exit_code_text::integer)
                    ELSE '{}'::jsonb
                END
            )
        ) AS normalized_error_json
    FROM repo_busy_candidates
)
UPDATE operations AS o
SET
    operation_state = 'failed',
    error_json = repo_busy_errors.normalized_error_json,
    finished_at = COALESCE(o.finished_at, o.updated_at, o.created_at),
    updated_at = CURRENT_TIMESTAMP
FROM repo_busy_errors
WHERE o.operation_id = repo_busy_errors.operation_id;
