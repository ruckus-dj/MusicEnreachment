DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM release_track WHERE metadata <> '{}'::jsonb) THEN
        RAISE EXCEPTION 'cannot roll back position metadata while desired metadata is populated';
    END IF;
END;
$$;

ALTER TABLE release_track
    DROP CONSTRAINT release_track_metadata_object_check,
    DROP COLUMN metadata;
