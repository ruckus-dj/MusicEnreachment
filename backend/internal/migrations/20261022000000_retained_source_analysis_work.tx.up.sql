-- Keep the origin identity of analyzed work after inventory locations go away.
-- current_location_id is the only attachment to live inventory.
ALTER TABLE source_analysis_work
    DROP CONSTRAINT source_analysis_work_location_id_source_root_id_fkey,
    DROP CONSTRAINT source_analysis_work_location_id_key,
    ADD COLUMN current_location_id uuid,
    ADD CONSTRAINT source_analysis_work_current_location_fk
        FOREIGN KEY (current_location_id, source_root_id)
        REFERENCES source_location(id, source_root_id)
        ON DELETE SET NULL (current_location_id),
    ADD CONSTRAINT source_analysis_work_current_location_origin_check
        CHECK (current_location_id IS NULL OR current_location_id = location_id);

UPDATE source_analysis_work SET current_location_id=location_id;
CREATE UNIQUE INDEX source_analysis_work_current_location_id_key
    ON source_analysis_work(current_location_id) WHERE current_location_id IS NOT NULL;

CREATE FUNCTION source_analysis_work_attachment_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.current_location_id IS NULL THEN
            NEW.current_location_id := NEW.location_id;
        END IF;
    ELSIF OLD.current_location_id IS NULL AND NEW.current_location_id IS NOT NULL THEN
        RAISE EXCEPTION 'retired source analysis work cannot be reattached';
    ELSIF OLD.current_location_id IS NOT NULL AND NEW.current_location_id IS NULL AND EXISTS (
        SELECT 1 FROM operation_source_work_hold hold
        JOIN operation operation ON operation.id=hold.operation_id
        WHERE hold.work_id=OLD.id AND operation.state IN ('queued','running')
    ) THEN
        RAISE EXCEPTION 'active source analysis work cannot be retired';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER source_analysis_work_attachment_guard
    BEFORE INSERT OR UPDATE OF current_location_id ON source_analysis_work
    FOR EACH ROW EXECUTE FUNCTION source_analysis_work_attachment_guard();

CREATE FUNCTION source_analysis_work_hold_requires_current_attachment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM source_analysis_work
        WHERE id=NEW.work_id AND current_location_id IS NOT NULL
        FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'retired source analysis work cannot be held';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER source_analysis_work_hold_requires_current_attachment
    BEFORE INSERT OR UPDATE ON operation_source_work_hold
    FOR EACH ROW EXECUTE FUNCTION source_analysis_work_hold_requires_current_attachment();
