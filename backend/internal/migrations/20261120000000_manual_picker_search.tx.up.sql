CREATE TABLE manual_picker_search (
    request_id uuid PRIMARY KEY,
    group_id uuid NOT NULL,
    operation_id uuid NOT NULL UNIQUE REFERENCES operation(id) ON DELETE CASCADE,
    search_kind text NOT NULL CHECK (search_kind IN ('artist_search', 'release_search', 'artist_release_browse')),
    canonical_digest bytea NOT NULL CHECK (octet_length(canonical_digest) = 32),
    query_text text NOT NULL,
    page_offset integer NOT NULL CHECK (page_offset >= 0),
    page_limit integer NOT NULL CHECK (page_limit > 0),
    provider_source_id uuid NOT NULL REFERENCES provider_source(id),
    configuration_identity text NOT NULL,
    cache_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX manual_picker_search_group_idx ON manual_picker_search(group_id, operation_id);

CREATE TABLE manual_picker_search_result (
    operation_id uuid PRIMARY KEY REFERENCES manual_picker_search(operation_id) ON DELETE CASCADE,
    group_id uuid NOT NULL,
    search_kind text NOT NULL CHECK (search_kind IN ('artist_search', 'release_search', 'artist_release_browse')),
    provider_source_id uuid NOT NULL REFERENCES provider_source(id),
    configuration_identity text NOT NULL,
    cache_key text NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    -- SHA-256 of the compact JSONB canonical text returned by the API.
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    cache_revision text,
    accepted_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION reject_manual_picker_search_result_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'manual picker search results are immutable';
END;
$$;
CREATE TRIGGER manual_picker_search_result_immutable
    BEFORE UPDATE ON manual_picker_search_result
    FOR EACH ROW EXECUTE FUNCTION reject_manual_picker_search_result_mutation();
