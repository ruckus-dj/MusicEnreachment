CREATE TABLE matching_fallback_evaluation_handoff (
    run_id uuid NOT NULL,
    wait_revision bigint NOT NULL CHECK (wait_revision > 0),
    provider_authority_version integer NOT NULL CHECK (provider_authority_version = 3),
    seal_digest bytea NOT NULL CHECK (octet_length(seal_digest) = 32),
    river_job_id bigint NOT NULL,
    operation_attempt integer NOT NULL CHECK (operation_attempt > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, wait_revision),
    UNIQUE (river_job_id),
    FOREIGN KEY (run_id) REFERENCES matching_run(id) ON DELETE RESTRICT,
    FOREIGN KEY (run_id, wait_revision)
        REFERENCES matching_fallback_authority_seal(run_id, wait_revision) ON DELETE RESTRICT
);

CREATE FUNCTION reject_matching_fallback_evaluation_handoff_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'matching fallback evaluation handoffs are write-once';
END;
$$;

CREATE TRIGGER matching_fallback_evaluation_handoff_write_once
    BEFORE UPDATE OR DELETE ON matching_fallback_evaluation_handoff
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_evaluation_handoff_mutation();
