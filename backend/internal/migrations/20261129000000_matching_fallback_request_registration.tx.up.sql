CREATE TABLE matching_fallback_request_registration (
    run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE CASCADE,
    stage_index integer NOT NULL CHECK (stage_index >= 0),
    role text NOT NULL CHECK (role IN ('search', 'lookup')),
    request_id uuid NOT NULL UNIQUE REFERENCES operation(id) ON DELETE RESTRICT,
    provider_source_id uuid NOT NULL REFERENCES provider_source(id) ON DELETE RESTRICT,
    configuration_identity text NOT NULL CHECK (length(btrim(configuration_identity)) > 0),
    kind text NOT NULL CHECK (kind IN ('provider_release_search', 'provider_recording_search', 'provider_release_lookup')),
    cache_key text NOT NULL CHECK (length(btrim(cache_key)) > 0),
    canonical_input jsonb NOT NULL CHECK (jsonb_typeof(canonical_input) = 'object'),
    input_sha256 bytea NOT NULL CHECK (octet_length(input_sha256) = 32),
    registered_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, stage_index, role, request_id),
    FOREIGN KEY (run_id, request_id)
        REFERENCES matching_provider_dependency(parent_run_id, request_id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    CHECK ((role = 'search' AND kind IN ('provider_release_search', 'provider_recording_search')) OR
           (role = 'lookup' AND kind = 'provider_release_lookup'))
);

CREATE UNIQUE INDEX matching_fallback_request_registration_one_search
    ON matching_fallback_request_registration(run_id, stage_index) WHERE role = 'search';
CREATE UNIQUE INDEX matching_fallback_request_registration_lookup_key
    ON matching_fallback_request_registration(run_id, stage_index, cache_key) WHERE role = 'lookup';

CREATE FUNCTION reject_matching_fallback_request_registration_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'matching fallback request registrations are immutable';
END;
$$;

CREATE TRIGGER matching_fallback_request_registration_immutable
    BEFORE UPDATE OR DELETE ON matching_fallback_request_registration
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_request_registration_mutation();
