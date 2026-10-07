-- The normalized worker is rooted in immutable work/step rows. The legacy
-- single-location installation selectors are not required for an active
-- normalized analysis; source_root exclusivity continues to cover every kind.
ALTER TABLE operation
    DROP CONSTRAINT operation_active_analysis_has_target_location,
    DROP CONSTRAINT operation_active_analysis_has_installation,
    ADD CONSTRAINT operation_normalized_analysis_shape CHECK (
        (kind <> 'analyze_source' OR state NOT IN ('queued','running') OR source_analysis_mode IS NOT NULL) AND
        (source_analysis_mode IS NULL OR (
            kind = 'analyze_source' AND analysis_installation_id IS NULL AND analysis_media_variant_id IS NULL AND
            ((state IN ('queued','running') AND target_source_root_id IS NOT NULL AND
                ((source_analysis_mode = 'batch' AND target_source_location_id IS NULL AND target_work_id IS NULL AND target_step IS NULL) OR
                 (source_analysis_mode = 'single_step' AND target_source_location_id IS NOT NULL AND target_work_id IS NOT NULL AND target_step IN ('sha256','probe','fingerprint')))) OR
             (state IN ('succeeded','failed') AND target_source_root_id IS NULL AND target_source_location_id IS NULL AND target_work_id IS NULL AND target_step IS NULL))
        )) IS TRUE
    );

-- SHA-only and explicit cache-only operations do not read tools and therefore
-- do not participate in tools-operation exclusion. Tool-reading normalized
-- analysis still participates; root exclusivity remains independently enforced
-- by operation_one_active_source_root_operation for every source operation.
ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;
ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind IN ('scan_source', 'analyze_source') AND tools_read_required THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind IN ('scan_source', 'analyze_source') AND tools_read_required THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR (kind IN ('scan_source', 'analyze_source') AND tools_read_required)
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
        )
    );
