DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM track_publication WHERE initial_lyrics IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back while current publication lyrics evidence exists';
    END IF;
END $$;

ALTER TABLE track_publication
    DROP CONSTRAINT track_publication_initial_lyrics_object,
    DROP COLUMN initial_lyrics;
