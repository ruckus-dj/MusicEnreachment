DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM operation WHERE source_analysis_mode IS NOT NULL OR target_work_id IS NOT NULL OR target_step IS NOT NULL OR tools_read_required OR rerun_target)
       OR EXISTS (SELECT 1 FROM operation_source_work_hold)
       OR EXISTS (SELECT 1 FROM operation_tool_read_hold)
       OR EXISTS (SELECT 1 FROM source_analysis_step WHERE execution_operation_id IS NOT NULL OR execution_job_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot rollback normalized source analysis while normalized operations, holds, or execution claims exist';
    END IF;
END $$;

ALTER TABLE operation
    DROP CONSTRAINT operation_normalized_analysis_shape,
    ADD CONSTRAINT operation_active_analysis_has_target_location CHECK (
        kind <> 'analyze_source' OR state NOT IN ('queued','running') OR target_source_location_id IS NOT NULL
    ),
    ADD CONSTRAINT operation_active_analysis_has_installation CHECK (
        kind <> 'analyze_source' OR state NOT IN ('queued','running') OR analysis_installation_id IS NOT NULL
    );

ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;
ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind IN ('scan_source', 'analyze_source') THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind IN ('scan_source', 'analyze_source') THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR kind IN ('scan_source', 'analyze_source')
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
        )
    );
