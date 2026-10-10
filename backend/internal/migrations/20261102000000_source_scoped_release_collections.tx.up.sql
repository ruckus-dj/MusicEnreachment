CREATE TABLE provider_source_release_artist_credit (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    artist_id uuid NOT NULL,
    name text NOT NULL DEFAULT '',
    name_join_phrase text NOT NULL DEFAULT '',
    present boolean NOT NULL DEFAULT true,
    authority smallint NOT NULL CHECK (authority >= 0),
    fetch_order bigint NOT NULL CHECK (fetch_order >= 0),
    PRIMARY KEY (provider_source_id, release_id, position),
    FOREIGN KEY (provider_id, provider_source_id)
        REFERENCES provider_source(provider_id, id) ON DELETE CASCADE,
    FOREIGN KEY (provider_id, release_id)
        REFERENCES provider_release(provider_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, artist_id)
        REFERENCES provider_artist(provider_id, id) ON DELETE RESTRICT
);

CREATE TABLE provider_source_release_track (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    track_id uuid NOT NULL,
    release_id uuid NOT NULL,
    recording_id uuid NOT NULL,
    medium integer NOT NULL CHECK (medium >= 0),
    position integer NOT NULL CHECK (position >= 0),
    displayed_number text NOT NULL,
    present boolean NOT NULL DEFAULT true,
    authority smallint NOT NULL CHECK (authority >= 0),
    fetch_order bigint NOT NULL CHECK (fetch_order >= 0),
    PRIMARY KEY (provider_source_id, track_id),
    FOREIGN KEY (provider_id, provider_source_id)
        REFERENCES provider_source(provider_id, id) ON DELETE CASCADE,
    FOREIGN KEY (provider_id, track_id)
        REFERENCES provider_release_track(provider_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, release_id)
        REFERENCES provider_release(provider_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, recording_id)
        REFERENCES provider_recording(provider_id, id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX provider_source_release_track_position_idx
    ON provider_source_release_track(provider_source_id, release_id, medium, position)
    WHERE present;
