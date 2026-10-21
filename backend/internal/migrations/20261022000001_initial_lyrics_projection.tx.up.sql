ALTER TABLE track_publication
    ADD COLUMN initial_lyrics jsonb;

ALTER TABLE track_publication
    ADD CONSTRAINT track_publication_initial_lyrics_object
    CHECK (initial_lyrics IS NULL OR jsonb_typeof(initial_lyrics) = 'object');
