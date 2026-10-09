CREATE TABLE provider (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE CHECK (length(btrim(code)) > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE provider_source (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES provider(id) ON DELETE RESTRICT,
    namespace text NOT NULL CHECK (length(btrim(namespace)) > 0),
    endpoint text NOT NULL,
    configuration_identity text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider_id, id),
    UNIQUE (provider_id, namespace)
);

CREATE TABLE provider_artist (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES provider(id) ON DELETE RESTRICT,
    provider_key text NOT NULL,
    name text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    raw_source jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(raw_source) IN ('object','array')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider_id, id),
    UNIQUE (provider_id, provider_key)
);

CREATE TABLE provider_release (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES provider(id) ON DELETE RESTRICT,
    provider_key text NOT NULL,
    title text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    raw_source jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(raw_source) IN ('object','array')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider_id, id),
    UNIQUE (provider_id, provider_key)
);

CREATE TABLE provider_recording (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL REFERENCES provider(id) ON DELETE RESTRICT,
    provider_key text NOT NULL,
    title text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    raw_source jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(raw_source) IN ('object','array')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider_id, id),
    UNIQUE (provider_id, provider_key)
);

CREATE TABLE provider_release_artist_credit (
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    artist_id uuid NOT NULL,
    name_join_phrase text NOT NULL DEFAULT '',
    present boolean NOT NULL DEFAULT true,
    PRIMARY KEY (provider_id, release_id, position),
    FOREIGN KEY (provider_id, release_id) REFERENCES provider_release(provider_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, artist_id) REFERENCES provider_artist(provider_id, id) ON DELETE RESTRICT
);

CREATE TABLE provider_release_track (
    id uuid PRIMARY KEY,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    recording_id uuid NOT NULL,
    medium integer CHECK (medium >= 0),
    position integer CHECK (position >= 0),
    displayed_number text NOT NULL,
    present boolean NOT NULL DEFAULT true,
    CHECK (present = (medium IS NOT NULL AND position IS NOT NULL)),
    FOREIGN KEY (provider_id, release_id) REFERENCES provider_release(provider_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, recording_id) REFERENCES provider_recording(provider_id, id) ON DELETE RESTRICT,
    UNIQUE (provider_id, id),
    UNIQUE (provider_id, release_id, medium, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE provider_response_cache (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    cache_key text NOT NULL,
    payload jsonb,
    fetched_at timestamptz,
    revision text,
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    PRIMARY KEY (provider_source_id, cache_key),
    FOREIGN KEY (provider_id, provider_source_id) REFERENCES provider_source(provider_id, id) ON DELETE CASCADE,
    CHECK (payload IS NULL OR jsonb_typeof(payload) IN ('object','array')),
    CHECK ((payload IS NULL) = (fetched_at IS NULL))
);
