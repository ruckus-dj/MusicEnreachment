CREATE SEQUENCE provider_fetch_order_seq AS bigint;

ALTER TABLE provider_response_cache
    ADD COLUMN fetch_order bigint NOT NULL DEFAULT 0 CHECK (fetch_order >= 0);

CREATE TABLE provider_source_entity (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    entity_kind text NOT NULL CHECK (entity_kind IN ('artist', 'release', 'recording')),
    provider_key text NOT NULL CHECK (length(btrim(provider_key)) > 0),
    name text NOT NULL DEFAULT '',
    fields jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(fields) = 'object'),
    field_evidence jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(field_evidence) = 'object'),
    raw_source jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(raw_source) IN ('object', 'array')),
    authority smallint NOT NULL CHECK (authority >= 0),
    complete boolean NOT NULL DEFAULT false,
    fetch_order bigint NOT NULL CHECK (fetch_order >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_source_id, entity_kind, provider_key),
    FOREIGN KEY (provider_id, provider_source_id)
        REFERENCES provider_source(provider_id, id) ON DELETE CASCADE
);

CREATE INDEX provider_source_entity_provider_key_idx
    ON provider_source_entity (provider_id, entity_kind, provider_key);

ALTER TABLE provider_release_artist_credit
    ADD COLUMN authority smallint NOT NULL DEFAULT 0 CHECK (authority >= 0),
    ADD COLUMN fetch_order bigint NOT NULL DEFAULT 0 CHECK (fetch_order >= 0);
ALTER TABLE provider_release_track
    ADD COLUMN authority smallint NOT NULL DEFAULT 0 CHECK (authority >= 0),
    ADD COLUMN fetch_order bigint NOT NULL DEFAULT 0 CHECK (fetch_order >= 0);
