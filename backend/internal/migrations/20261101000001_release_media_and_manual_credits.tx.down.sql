DO $guard$
BEGIN
    IF EXISTS (
        SELECT 1 FROM release_artist WHERE role <> ''
        UNION ALL SELECT 1 FROM track_artist WHERE role <> ''
        UNION ALL SELECT 1 FROM recording_artist WHERE role <> ''
        UNION ALL SELECT 1 FROM release_track WHERE duration_ms IS NOT NULL
        UNION ALL SELECT 1 FROM release_medium WHERE title <> '' OR format <> ''
        UNION ALL SELECT 1 FROM release_medium medium
            WHERE NOT EXISTS (
                SELECT 1 FROM release_track track
                WHERE track.release_id = medium.release_id AND track.medium = medium.medium
            )
    ) THEN
        RAISE EXCEPTION 'cannot roll back release media and manual credits: populated additive fields would be lost';
    END IF;
END;
$guard$;

DROP TRIGGER release_track_ensure_medium ON release_track;
DROP FUNCTION ensure_release_medium_for_track();
ALTER TABLE release_track DROP CONSTRAINT release_track_medium_fk;
ALTER TABLE release_track DROP COLUMN duration_ms;
ALTER TABLE recording_artist DROP COLUMN role;
ALTER TABLE track_artist DROP COLUMN role;
ALTER TABLE release_artist DROP COLUMN role;
DROP TABLE release_medium;
