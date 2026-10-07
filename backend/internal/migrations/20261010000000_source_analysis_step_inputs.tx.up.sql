ALTER TABLE source_analysis_step
    ADD CONSTRAINT source_analysis_step_active_input_snapshot CHECK (
        state NOT IN ('queued', 'running') OR (
            input_snapshot IS NOT NULL AND
            jsonb_typeof(input_snapshot) = 'object' AND
            jsonb_typeof(input_snapshot -> 'sha256_enabled') IS NOT DISTINCT FROM 'boolean' AND
            jsonb_typeof(input_snapshot -> 'cache_only_reuse') IS NOT DISTINCT FROM 'boolean' AND
            jsonb_typeof(input_snapshot -> 'rerun_target') IS NOT DISTINCT FROM 'boolean'
        )
    );
