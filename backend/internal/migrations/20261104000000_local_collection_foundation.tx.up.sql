-- Local catalog rows are deliberately independent of provider projections and
-- incoming-analysis work. Provider identity links are explicit user decisions.
CREATE TABLE collection (
    id uuid PRIMARY KEY,
    title text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    metadata_version integer NOT NULL DEFAULT 1 CHECK (metadata_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE local_artist (
    id uuid PRIMARY KEY,
    name text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    metadata_version integer NOT NULL DEFAULT 1 CHECK (metadata_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE local_release (
    id uuid PRIMARY KEY,
    title text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    metadata_version integer NOT NULL DEFAULT 1 CHECK (metadata_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE local_recording (
    id uuid PRIMARY KEY,
    title text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    metadata_version integer NOT NULL DEFAULT 1 CHECK (metadata_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE local_track (
    id uuid PRIMARY KEY,
    title text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    metadata_version integer NOT NULL DEFAULT 1 CHECK (metadata_version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Credit positions are the identity of ordered credit rows. Repeated artists
-- are retained as distinct positions.
CREATE TABLE collection_artist (
    collection_id uuid NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    artist_id uuid NOT NULL REFERENCES local_artist(id) ON DELETE RESTRICT,
    name text NOT NULL DEFAULT '',
    name_join_phrase text NOT NULL DEFAULT '',
    PRIMARY KEY (collection_id, position)
);

CREATE TABLE release_artist (
    release_id uuid NOT NULL REFERENCES local_release(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    artist_id uuid NOT NULL REFERENCES local_artist(id) ON DELETE RESTRICT,
    name text NOT NULL DEFAULT '',
    name_join_phrase text NOT NULL DEFAULT '',
    PRIMARY KEY (release_id, position)
);

CREATE TABLE recording_artist (
    recording_id uuid NOT NULL REFERENCES local_recording(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    artist_id uuid NOT NULL REFERENCES local_artist(id) ON DELETE RESTRICT,
    name text NOT NULL DEFAULT '',
    name_join_phrase text NOT NULL DEFAULT '',
    PRIMARY KEY (recording_id, position)
);

CREATE TABLE track_artist (
    track_id uuid NOT NULL REFERENCES local_track(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    artist_id uuid NOT NULL REFERENCES local_artist(id) ON DELETE RESTRICT,
    name text NOT NULL DEFAULT '',
    name_join_phrase text NOT NULL DEFAULT '',
    PRIMARY KEY (track_id, position)
);

CREATE TABLE collection_release (
    collection_id uuid NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    release_id uuid NOT NULL REFERENCES local_release(id) ON DELETE RESTRICT,
    PRIMARY KEY (collection_id, position)
);

CREATE TABLE release_track (
    release_id uuid NOT NULL REFERENCES local_release(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    track_id uuid NOT NULL REFERENCES local_track(id) ON DELETE RESTRICT,
    recording_id uuid REFERENCES local_recording(id) ON DELETE RESTRICT,
    medium integer NOT NULL DEFAULT 0 CHECK (medium >= 0),
    displayed_number text NOT NULL DEFAULT '',
    PRIMARY KEY (release_id, position)
);

-- Provider identities have both directions unique within their provider.
-- Provider IDs are never globally unique across providers by assumption.
CREATE TABLE provider_local_artist (
    provider_id uuid NOT NULL,
    provider_artist_id uuid NOT NULL,
    local_artist_id uuid NOT NULL REFERENCES local_artist(id) ON DELETE RESTRICT,
    method text NOT NULL CHECK (method IN ('manual', 'automatic')),
    policy_version text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_id, provider_artist_id),
    UNIQUE (provider_id, local_artist_id),
    FOREIGN KEY (provider_id, provider_artist_id) REFERENCES provider_artist(provider_id, id) ON DELETE RESTRICT,
    CHECK ((method = 'manual' AND policy_version IS NULL) OR
           (method = 'automatic' AND policy_version IS NOT NULL AND btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000') <> ''))
);

CREATE TABLE provider_local_release (
    provider_id uuid NOT NULL,
    provider_release_id uuid NOT NULL,
    local_release_id uuid NOT NULL REFERENCES local_release(id) ON DELETE RESTRICT,
    method text NOT NULL CHECK (method IN ('manual', 'automatic')),
    policy_version text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_id, provider_release_id),
    UNIQUE (provider_id, local_release_id),
    FOREIGN KEY (provider_id, provider_release_id) REFERENCES provider_release(provider_id, id) ON DELETE RESTRICT,
    CHECK ((method = 'manual' AND policy_version IS NULL) OR
           (method = 'automatic' AND policy_version IS NOT NULL AND btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000') <> ''))
);

CREATE TABLE provider_local_recording (
    provider_id uuid NOT NULL,
    provider_recording_id uuid NOT NULL,
    local_recording_id uuid NOT NULL REFERENCES local_recording(id) ON DELETE RESTRICT,
    method text NOT NULL CHECK (method IN ('manual', 'automatic')),
    policy_version text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_id, provider_recording_id),
    UNIQUE (provider_id, local_recording_id),
    FOREIGN KEY (provider_id, provider_recording_id) REFERENCES provider_recording(provider_id, id) ON DELETE RESTRICT,
    CHECK ((method = 'manual' AND policy_version IS NULL) OR
           (method = 'automatic' AND policy_version IS NOT NULL AND btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000') <> ''))
);

CREATE TABLE provider_local_track (
    provider_id uuid NOT NULL,
    provider_track_id uuid NOT NULL,
    local_track_id uuid NOT NULL REFERENCES local_track(id) ON DELETE RESTRICT,
    method text NOT NULL CHECK (method IN ('manual', 'automatic')),
    policy_version text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_id, provider_track_id),
    UNIQUE (provider_id, local_track_id),
    FOREIGN KEY (provider_id, provider_track_id) REFERENCES provider_release_track(provider_id, id) ON DELETE RESTRICT,
    CHECK ((method = 'manual' AND policy_version IS NULL) OR
           (method = 'automatic' AND policy_version IS NOT NULL AND btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000') <> ''))
);

-- No provider position/global variant uniqueness: a source variant may be
-- attached to different local tracks; a track may have several variants.
CREATE TABLE track_source (
    id uuid PRIMARY KEY,
    track_id uuid NOT NULL REFERENCES local_track(id) ON DELETE CASCADE,
    media_variant_id uuid NOT NULL REFERENCES media_variant(id) ON DELETE RESTRICT,
    selected boolean NOT NULL DEFAULT false,
    compatible boolean NOT NULL DEFAULT true,
    decision_method text NOT NULL CHECK (decision_method IN ('manual', 'automatic')),
    policy_version text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (track_id, media_variant_id),
    CHECK ((decision_method = 'manual' AND policy_version IS NULL) OR
     (decision_method = 'automatic' AND policy_version IS NOT NULL AND btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000') <> ''))
);

CREATE UNIQUE INDEX track_source_one_selected_idx ON track_source(track_id) WHERE selected;
