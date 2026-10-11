CREATE TABLE matching_acquisition_seal (
    run_id uuid PRIMARY KEY REFERENCES matching_acquisition(run_id) ON DELETE CASCADE,
    acquisition_revision bigint NOT NULL CHECK (acquisition_revision >= 0),
    input jsonb NOT NULL CHECK (jsonb_typeof(input) = 'object'),
    input_digest bytea NOT NULL CHECK (octet_length(input_digest) = 32),
    catalog_digest bytea NOT NULL CHECK (octet_length(catalog_digest) = 32),
    river_job_id bigint NOT NULL UNIQUE,
    operation_attempt integer NOT NULL CHECK (operation_attempt > 0),
    sealed_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION reject_matching_acquisition_seal_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'matching acquisition seals are write-once';
END;
$$;

CREATE TRIGGER matching_acquisition_seal_write_once
    BEFORE UPDATE OR DELETE ON matching_acquisition_seal
    FOR EACH ROW EXECUTE FUNCTION reject_matching_acquisition_seal_mutation();
