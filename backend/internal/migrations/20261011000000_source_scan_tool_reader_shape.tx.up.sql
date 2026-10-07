ALTER TABLE operation
    DROP CONSTRAINT operation_source_analysis_target_shape,
    ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
        ((source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target AND
          (NOT tools_read_required OR kind = 'scan_source')) OR
        (source_analysis_mode = 'batch' AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target) OR
        (source_analysis_mode = 'single_step' AND
            ((state IN ('queued', 'running') AND target_work_id IS NOT NULL AND target_step IN ('sha256', 'probe', 'fingerprint')) OR
            (state IN ('succeeded', 'failed') AND target_work_id IS NULL AND target_step IS NULL)) AND
            (NOT rerun_target OR (state IN ('queued', 'running') AND target_step = 'fingerprint') OR
             (state IN ('succeeded', 'failed') AND target_work_id IS NULL AND target_step IS NULL)))) IS TRUE
    );
