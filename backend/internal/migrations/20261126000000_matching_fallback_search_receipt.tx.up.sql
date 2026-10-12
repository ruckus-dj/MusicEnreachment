-- Preserve the exact accepted provider response for the first bounded fallback
-- search. Cache refreshes are intentionally independent from this authority.
CREATE TABLE matching_fallback_search_receipt (
    run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE CASCADE,
    request_id uuid NOT NULL UNIQUE REFERENCES operation(id) ON DELETE RESTRICT,
    stage_index integer NOT NULL CHECK (stage_index = 0),
    provider_source_id uuid NOT NULL REFERENCES provider_source(id) ON DELETE RESTRICT,
    configuration_identity text NOT NULL CHECK (length(btrim(configuration_identity)) > 0),
    cache_key text NOT NULL CHECK (length(btrim(cache_key)) > 0),
    request_input jsonb NOT NULL CHECK (jsonb_typeof(request_input) = 'object'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    cache_revision text,
    cache_generation bigint NOT NULL CHECK (cache_generation > 0),
    fetch_order bigint NOT NULL CHECK (fetch_order > 0),
    execution_epoch bigint NOT NULL CHECK (execution_epoch > 0),
    provider_attempt integer NOT NULL CHECK (provider_attempt > 0),
    river_job_id bigint NOT NULL CHECK (river_job_id > 0),
    accepted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, request_id),
    FOREIGN KEY (run_id, request_id)
        REFERENCES matching_provider_dependency(parent_run_id, request_id)
        ON DELETE CASCADE
);

CREATE FUNCTION reject_matching_fallback_search_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'matching fallback search receipts are immutable';
END;
$$;

CREATE TRIGGER matching_fallback_search_receipt_immutable
    BEFORE UPDATE OR DELETE ON matching_fallback_search_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_search_receipt_mutation();
