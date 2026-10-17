ALTER TABLE release_track
    ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT release_track_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object');
