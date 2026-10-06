DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM operation WHERE source_analysis_mode IS NOT NULL OR target_work_id IS NOT NULL OR target_step IS NOT NULL OR rerun_target OR tools_read_required)
       OR EXISTS (SELECT 1 FROM operation_source_work_hold)
       OR EXISTS (SELECT 1 FROM operation_tool_read_hold)
       OR EXISTS (SELECT 1 FROM source_analysis_step WHERE execution_operation_id IS NOT NULL OR execution_job_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot rollback normalized source-analysis operations: operation snapshots, jobs, holds, or execution claims exist';
    END IF;
END $$;

DROP TRIGGER operation_tool_read_hold_guard ON operation_tool_read_hold;
DROP TRIGGER operation_source_work_hold_guard ON operation_source_work_hold;
DROP TRIGGER operation_source_analysis_holds_guard ON operation;
DROP TRIGGER source_analysis_step_membership_guard ON source_analysis_step;
DROP FUNCTION check_source_analysis_step_membership();
DROP FUNCTION check_source_analysis_operation_holds();
DROP INDEX operation_active_source_analysis_work_idx;
ALTER TABLE operation DROP CONSTRAINT operation_target_work_id_fkey;
ALTER TABLE operation
    DROP CONSTRAINT operation_source_analysis_selectors_kind,
    DROP CONSTRAINT operation_source_analysis_target_shape,
    DROP CONSTRAINT operation_source_analysis_mode_valid,
    DROP COLUMN rerun_target,
    DROP COLUMN tools_read_required,
    DROP COLUMN target_step,
    DROP COLUMN target_work_id,
    DROP COLUMN source_analysis_mode;
