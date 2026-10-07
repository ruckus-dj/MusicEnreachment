DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM operation
        WHERE analysis_installation_id IS NOT NULL OR analysis_media_variant_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot remove obsolete analysis holds while legacy operation holds exist';
    END IF;
END $$;

ALTER TABLE operation DROP CONSTRAINT operation_analysis_installation_id_fkey;
ALTER TABLE operation DROP CONSTRAINT operation_analysis_media_variant_id_fkey;
DROP INDEX operation_analysis_installation_idx;

ALTER TABLE operation
    DROP CONSTRAINT operation_normalized_analysis_shape,
    ADD CONSTRAINT operation_normalized_analysis_shape CHECK (
        (kind <> 'analyze_source' OR state NOT IN ('queued','running') OR source_analysis_mode IS NOT NULL) AND
        (source_analysis_mode IS NULL OR (
            kind = 'analyze_source' AND
            ((state IN ('queued','running') AND target_source_root_id IS NOT NULL AND
                ((source_analysis_mode = 'batch' AND target_source_location_id IS NULL AND target_work_id IS NULL AND target_step IS NULL) OR
                 (source_analysis_mode = 'single_step' AND target_source_location_id IS NOT NULL AND target_work_id IS NOT NULL AND target_step IN ('sha256','probe','fingerprint')))) OR
              (state IN ('succeeded','failed') AND target_source_root_id IS NULL AND target_source_location_id IS NULL AND target_work_id IS NULL AND target_step IS NULL))
        )) IS TRUE
    );

ALTER TABLE operation
    DROP COLUMN analysis_installation_id,
    DROP COLUMN analysis_media_variant_id;
