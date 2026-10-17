ALTER TABLE local_release
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1),
    ADD COLUMN primary_artist_id uuid REFERENCES local_artist(id) ON DELETE RESTRICT;

ALTER TABLE local_track
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1);

ALTER TABLE local_recording
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1);

ALTER TABLE release_track
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1),
    ADD COLUMN primary_artist_id uuid REFERENCES local_artist(id) ON DELETE RESTRICT;
