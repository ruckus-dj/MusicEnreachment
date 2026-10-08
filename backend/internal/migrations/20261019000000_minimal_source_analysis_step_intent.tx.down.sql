DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM source_analysis_step
        WHERE state IN ('queued', 'running')
          AND (
              input_snapshot IS NULL
              OR jsonb_typeof(input_snapshot) IS DISTINCT FROM 'object'
              OR jsonb_typeof(input_snapshot->'sha256_enabled') IS DISTINCT FROM 'boolean'
              OR jsonb_typeof(input_snapshot->'cache_only_reuse') IS DISTINCT FROM 'boolean'
              OR jsonb_typeof(input_snapshot->'rerun_target') IS DISTINCT FROM 'boolean'
          )
    ) THEN
        RAISE EXCEPTION 'cannot restore the prior source-analysis step snapshot constraint while incompatible active steps exist';
    END IF;
END $$;

ALTER TABLE source_analysis_step
    DROP CONSTRAINT source_analysis_step_active_input_snapshot,
    ADD CONSTRAINT source_analysis_step_active_input_snapshot CHECK (
        state NOT IN ('queued', 'running') OR (
            input_snapshot IS NOT NULL
            AND jsonb_typeof(input_snapshot) = 'object'
            AND jsonb_typeof(input_snapshot->'sha256_enabled') IS NOT DISTINCT FROM 'boolean'
            AND jsonb_typeof(input_snapshot->'cache_only_reuse') IS NOT DISTINCT FROM 'boolean'
            AND jsonb_typeof(input_snapshot->'rerun_target') IS NOT DISTINCT FROM 'boolean'
        )
    );

ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;
ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind IN ('move_tools_root', 'scan_source', 'analyze_source') THEN NULL
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot->>'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind IN ('move_tools_root', 'scan_source', 'analyze_source') THEN NULL
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot->>'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot->>'target_identity', '') <> ''
        )
    );
