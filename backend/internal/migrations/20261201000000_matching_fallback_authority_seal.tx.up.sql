CREATE TABLE matching_fallback_authority_seal (
    run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE CASCADE,
    wait_revision bigint NOT NULL CHECK (wait_revision > 0),
    schema_version integer NOT NULL CHECK (schema_version = 3),
    authority jsonb NOT NULL CHECK (jsonb_typeof(authority) = 'object'),
    authority_digest bytea NOT NULL CHECK (octet_length(authority_digest) = 32),
    sealed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, wait_revision)
);

CREATE FUNCTION reject_matching_fallback_authority_seal_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'matching fallback authority seals are write-once';
END;
$$;

CREATE TRIGGER matching_fallback_authority_seal_write_once
    BEFORE UPDATE OR DELETE ON matching_fallback_authority_seal
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_authority_seal_mutation();
