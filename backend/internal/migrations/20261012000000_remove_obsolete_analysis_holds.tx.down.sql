ALTER TABLE operation
    ADD COLUMN analysis_installation_id uuid,
    ADD COLUMN analysis_media_variant_id uuid;

ALTER TABLE operation
    ADD CONSTRAINT operation_analysis_installation_id_fkey
        FOREIGN KEY (analysis_installation_id) REFERENCES tool_installation(id) ON DELETE RESTRICT,
    ADD CONSTRAINT operation_analysis_media_variant_id_fkey
        FOREIGN KEY (analysis_media_variant_id) REFERENCES media_variant(id) ON DELETE RESTRICT;

CREATE INDEX operation_analysis_installation_idx
    ON operation (analysis_installation_id)
    WHERE analysis_installation_id IS NOT NULL;

ALTER TABLE operation
    DROP CONSTRAINT operation_normalized_analysis_shape,
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
