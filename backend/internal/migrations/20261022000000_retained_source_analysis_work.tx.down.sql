DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM source_analysis_work WHERE current_location_id IS NULL) THEN
        RAISE EXCEPTION 'cannot roll back retained source analysis work while tombstones exist';
    END IF;
END $$;

DROP TRIGGER source_analysis_work_hold_requires_current_attachment ON operation_source_work_hold;
DROP FUNCTION source_analysis_work_hold_requires_current_attachment();
DROP TRIGGER source_analysis_work_attachment_guard ON source_analysis_work;
DROP FUNCTION source_analysis_work_attachment_guard();

DROP INDEX source_analysis_work_current_location_id_key;
ALTER TABLE source_analysis_work
    DROP CONSTRAINT source_analysis_work_current_location_origin_check,
    DROP CONSTRAINT source_analysis_work_current_location_fk,
    DROP COLUMN current_location_id,
    ADD CONSTRAINT source_analysis_work_location_id_key UNIQUE (location_id),
    ADD CONSTRAINT source_analysis_work_location_id_source_root_id_fkey
        FOREIGN KEY (location_id, source_root_id)
        REFERENCES source_location(id, source_root_id) ON DELETE CASCADE;
