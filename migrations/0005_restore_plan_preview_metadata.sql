ALTER TABLE restore_plans ADD COLUMN IF NOT EXISTS base_revision text;
ALTER TABLE restore_plans ADD COLUMN IF NOT EXISTS head_revision text;
ALTER TABLE restore_plans ADD COLUMN IF NOT EXISTS generation text;
ALTER TABLE restore_plans ADD COLUMN IF NOT EXISTS fence_marker text;
ALTER TABLE restore_plans ADD COLUMN IF NOT EXISTS summary_json jsonb;
ALTER TABLE restore_plans ADD COLUMN IF NOT EXISTS blockers_json jsonb;
ALTER TABLE restore_plans ADD COLUMN IF NOT EXISTS stale boolean;

UPDATE restore_plans p
SET
    base_revision = COALESCE(
        p.base_revision,
        NULLIF(o.verification_result->>'base_revision', ''),
        NULLIF(o.jvs_json_output->>'base_revision', ''),
        p.source_save_point_id
    ),
    head_revision = COALESCE(
        p.head_revision,
        NULLIF(o.verification_result->>'head_revision', ''),
        NULLIF(o.jvs_json_output->>'head_revision', ''),
        p.source_save_point_id
    ),
    generation = COALESCE(
        p.generation,
        NULLIF(o.verification_result->>'generation', ''),
        NULLIF(o.jvs_json_output->>'generation', ''),
        'pre_ga_restore_plan_metadata_missing'
    ),
    fence_marker = COALESCE(
        p.fence_marker,
        NULLIF(o.verification_result->>'fence_marker', ''),
        NULLIF(o.jvs_json_output->>'fence_marker', ''),
        'pre_ga_restore_plan_metadata_missing'
    ),
    summary_json = COALESCE(
        p.summary_json,
        CASE WHEN jsonb_typeof(o.verification_result->'summary') = 'object' THEN o.verification_result->'summary' END,
        CASE WHEN jsonb_typeof(o.jvs_json_output->'summary') = 'object' THEN o.jvs_json_output->'summary' END,
        '{"added":{"count":0,"samples":[]},"changed":{"count":0,"samples":[]},"removed":{"count":0,"samples":[]},"destructive":false}'::jsonb
    ),
    blockers_json = COALESCE(
        p.blockers_json,
        CASE
            WHEN COALESCE(
                NULLIF(o.verification_result->>'base_revision', ''),
                NULLIF(o.jvs_json_output->>'base_revision', '')
            ) IS NULL THEN '[{"code":"pre_ga_restore_plan_metadata_missing","message":"Restore plan was created before durable preview metadata was recorded; discard it and create a new restore preview."}]'::jsonb
            WHEN jsonb_typeof(o.verification_result->'blockers') = 'array' THEN o.verification_result->'blockers'
            WHEN jsonb_typeof(o.jvs_json_output->'blockers') = 'array' THEN o.jvs_json_output->'blockers'
            ELSE '[]'::jsonb
        END
    ),
    stale = COALESCE(
        p.stale,
        COALESCE(
            NULLIF(o.verification_result->>'base_revision', ''),
            NULLIF(o.jvs_json_output->>'base_revision', '')
        ) IS NULL
    )
FROM operations o
WHERE p.preview_operation_id = o.operation_id
    AND (
        p.base_revision IS NULL
        OR p.head_revision IS NULL
        OR p.generation IS NULL
        OR p.fence_marker IS NULL
        OR p.summary_json IS NULL
        OR p.blockers_json IS NULL
        OR p.stale IS NULL
    );

ALTER TABLE restore_plans ALTER COLUMN base_revision SET NOT NULL;
ALTER TABLE restore_plans ALTER COLUMN head_revision SET NOT NULL;
ALTER TABLE restore_plans ALTER COLUMN generation SET NOT NULL;
ALTER TABLE restore_plans ALTER COLUMN fence_marker SET NOT NULL;
ALTER TABLE restore_plans ALTER COLUMN summary_json SET NOT NULL;
ALTER TABLE restore_plans ALTER COLUMN blockers_json SET NOT NULL;
ALTER TABLE restore_plans ALTER COLUMN stale SET DEFAULT false;
ALTER TABLE restore_plans ALTER COLUMN stale SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'restore_plans_summary_json_object'
            AND conrelid = 'restore_plans'::regclass
    ) THEN
        ALTER TABLE restore_plans
            ADD CONSTRAINT restore_plans_summary_json_object CHECK (jsonb_typeof(summary_json) = 'object');
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'restore_plans_blockers_json_array'
            AND conrelid = 'restore_plans'::regclass
    ) THEN
        ALTER TABLE restore_plans
            ADD CONSTRAINT restore_plans_blockers_json_array CHECK (jsonb_typeof(blockers_json) = 'array');
    END IF;
END $$;
