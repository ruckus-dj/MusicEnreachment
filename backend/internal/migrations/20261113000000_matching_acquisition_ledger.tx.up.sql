-- Keep the acquisition contract and every phase handoff separate from the
-- immutable v2/v3 evaluation plan. Existing plans remain byte-for-byte intact.
CREATE TABLE matching_acquisition (
    run_id uuid PRIMARY KEY REFERENCES matching_run(id) ON DELETE CASCADE,
    intent_version integer NOT NULL CHECK (intent_version > 0),
    intent jsonb NOT NULL CHECK (jsonb_typeof(intent) = 'object'),
    intent_digest bytea NOT NULL CHECK (octet_length(intent_digest) = 32),
    current_phase text NOT NULL CHECK (length(btrim(current_phase)) > 0),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0)
);

CREATE TABLE matching_acquisition_phase (
    run_id uuid NOT NULL REFERENCES matching_acquisition(run_id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision >= 0),
    phase text NOT NULL CHECK (length(btrim(phase)) > 0),
    input jsonb NOT NULL CHECK (jsonb_typeof(input) = 'object'),
    output jsonb NOT NULL CHECK (jsonb_typeof(output) = 'object'),
    response_provenance jsonb NOT NULL CHECK (jsonb_typeof(response_provenance) = 'array'),
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, revision)
);

CREATE FUNCTION reject_matching_acquisition_phase_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	-- Permit FK cascades from operation/run cleanup while rejecting direct edits.
	IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
		RETURN OLD;
	END IF;
    RAISE EXCEPTION 'matching acquisition phase records are append-only';
END;
$$;

CREATE TRIGGER matching_acquisition_phase_append_only
    BEFORE UPDATE OR DELETE ON matching_acquisition_phase
    FOR EACH ROW EXECUTE FUNCTION reject_matching_acquisition_phase_mutation();
