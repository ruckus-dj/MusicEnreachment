CREATE TABLE matching_fallback_calculation_receipt (
    run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE RESTRICT,
    acquisition_revision bigint NOT NULL CHECK (acquisition_revision > 0),
    stage_index integer NOT NULL CHECK (stage_index = 0),
    schema_version integer NOT NULL CHECK (schema_version = 2),
    calculator_version integer NOT NULL CHECK (calculator_version = 1),
    digest_version integer NOT NULL CHECK (digest_version = 2),
    input_digest bytea NOT NULL CHECK (octet_length(input_digest) = 32),
    context jsonb NOT NULL CHECK (jsonb_typeof(context) = 'object'),
    edge_calculations jsonb NOT NULL CHECK (jsonb_typeof(edge_calculations) = 'array'),
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, acquisition_revision),
    FOREIGN KEY (run_id, acquisition_revision)
        REFERENCES matching_acquisition_phase(run_id, revision) ON DELETE RESTRICT
);

CREATE FUNCTION reject_matching_fallback_calculation_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'matching fallback calculation receipts are immutable';
END;
$$;

CREATE TRIGGER matching_fallback_calculation_receipt_immutable
    BEFORE UPDATE OR DELETE ON matching_fallback_calculation_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_calculation_receipt_mutation();
