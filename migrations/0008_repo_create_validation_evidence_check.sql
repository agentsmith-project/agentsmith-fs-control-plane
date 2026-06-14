-- Enforce repo_create terminal validation evidence at the durable operation
-- boundary. NOT VALID avoids blocking upgrade on historical rows while still
-- rejecting new or updated rows with stripped {repo_id}-only evidence.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'operations_repo_create_terminal_validation_evidence_check'
            AND conrelid = 'operations'::regclass
    ) THEN
        ALTER TABLE operations
        ADD CONSTRAINT operations_repo_create_terminal_validation_evidence_check
        CHECK (
            NOT (
                operation_type = 'repo_create'
                AND phase = 'validate_repo_create'
                AND operation_state IN ('failed', 'operator_intervention_required')
                AND COALESCE(error_json->>'code', '') IN (
                    'REPO_CREATE_VALIDATION_FAILED',
                    'REPO_CREATE_VALIDATION_FAILED_WITH_FENCE'
                )
            )
            OR COALESCE((
                jsonb_typeof(error_json) = 'object'
                AND jsonb_typeof(error_json->'details') = 'object'
                AND jsonb_typeof(verification_result) = 'object'
                AND btrim(error_json#>>'{details,repo_id}') = repo_id
                AND btrim(verification_result->>'repo_id') = repo_id
                AND btrim(error_json#>>'{details,validation_reason}') ~ '^[a-z0-9_]+$'
                AND btrim(error_json#>>'{details,metadata_stage}') ~ '^[a-z0-9_]+$'
                AND btrim(verification_result->>'validation_reason') = btrim(error_json#>>'{details,validation_reason}')
                AND btrim(verification_result->>'metadata_stage') = btrim(error_json#>>'{details,metadata_stage}')
                AND (
                    (
                        NULLIF(btrim(error_json#>>'{details,volume_id}'), '') IS NULL
                        AND NULLIF(btrim(verification_result->>'volume_id'), '') IS NULL
                    )
                    OR btrim(verification_result->>'volume_id') = btrim(error_json#>>'{details,volume_id}')
                )
                AND (
                    btrim(error_json#>>'{details,validation_reason}') <> 'volume_root_config_missing'
                    OR (
                        error_json#>'{details,configured_volume_root_ids}' IS NOT NULL
                        AND verification_result->'configured_volume_root_ids' = error_json#>'{details,configured_volume_root_ids}'
                    )
                )
            ), false)
        ) NOT VALID;
    END IF;
END $$;
