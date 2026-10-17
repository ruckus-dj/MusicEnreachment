DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM local_release WHERE primary_artist_id IS NOT NULL)
       OR EXISTS (SELECT 1 FROM release_track WHERE primary_artist_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back library primary artist selections while selections exist';
    END IF;
END
$$;

ALTER TABLE release_track DROP COLUMN primary_artist_id, DROP COLUMN metadata_revision;
ALTER TABLE local_recording DROP COLUMN metadata_revision;
ALTER TABLE local_track DROP COLUMN metadata_revision;
ALTER TABLE local_release DROP COLUMN primary_artist_id, DROP COLUMN metadata_revision;
