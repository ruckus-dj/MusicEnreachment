-- The former key cannot represent two media with the same position. Refuse a
-- lossy rollback rather than deleting or rewriting local release assignments.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM release_track
        GROUP BY release_id, position
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot restore release_track(release_id, position) key while medium positions collide';
    END IF;
END;
$$;

ALTER TABLE release_track DROP CONSTRAINT release_track_pkey;
ALTER TABLE release_track ADD PRIMARY KEY (release_id, position);
