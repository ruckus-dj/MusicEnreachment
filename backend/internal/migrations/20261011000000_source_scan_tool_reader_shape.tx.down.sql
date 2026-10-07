DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM operation WHERE kind = 'scan_source' AND tools_read_required)
       OR EXISTS (
           SELECT 1 FROM operation_tool_read_hold hold
           JOIN operation ON operation.id = hold.operation_id
           WHERE operation.kind = 'scan_source'
       ) THEN
        RAISE EXCEPTION 'cannot rollback source scan tool reader shape while scan operations retain reader flags or holds';
    END IF;
END $$;

ALTER TABLE operation
    DROP CONSTRAINT operation_source_analysis_target_shape,
    ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
        ((source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target AND NOT tools_read_required) OR
        (source_analysis_mode = 'batch' AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target) OR
        (source_analysis_mode = 'single_step' AND
            ((state IN ('queued', 'running') AND target_work_id IS NOT NULL AND target_step IN ('sha256', 'probe', 'fingerprint')) OR
            (state IN ('succeeded', 'failed') AND target_work_id IS NULL AND target_step IS NULL)) AND
            (NOT rerun_target OR (state IN ('queued', 'running') AND target_step = 'fingerprint') OR
             (state IN ('succeeded', 'failed') AND target_work_id IS NULL AND target_step IS NULL)))) IS TRUE
    );
