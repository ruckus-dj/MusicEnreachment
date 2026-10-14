CREATE TABLE matching_run_retirement_authorization (
    run_id uuid PRIMARY KEY REFERENCES matching_run(id) ON DELETE CASCADE,
    transaction_id xid8 NOT NULL DEFAULT pg_current_xact_id(),
    authorized_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    cleanup_cutoff timestamptz NOT NULL DEFAULT (transaction_timestamp() - interval '24 hours')
);

CREATE FUNCTION validate_matching_run_retirement_authorization() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    operation_row operation%ROWTYPE;
BEGIN
    -- Match admission writers: global gates precede run/operation row locks.
    PERFORM pg_advisory_xact_lock_shared(1297371734, 3);
    PERFORM pg_advisory_xact_lock(1297371734, 4);
    PERFORM pg_advisory_xact_lock_shared(hashtext('musicbrainz-config'));
    IF NEW.transaction_id <> pg_current_xact_id() THEN
        RAISE EXCEPTION 'matching run retirement authorization must belong to this transaction';
    END IF;
    IF NEW.authorized_at <> transaction_timestamp() THEN
        RAISE EXCEPTION 'matching run retirement authorization timestamp must equal transaction_timestamp()';
    END IF;
    IF NEW.cleanup_cutoff > transaction_timestamp() - interval '24 hours' THEN
        RAISE EXCEPTION 'matching run retirement cleanup cutoff exceeds the 24-hour eligibility boundary';
    END IF;
    SELECT operation.* INTO operation_row
      FROM matching_run run JOIN operation ON operation.id = run.operation_id
     WHERE run.id = NEW.run_id FOR UPDATE OF run, operation;
    IF NOT FOUND OR operation_row.kind <> 'matching_evaluation_v1' OR operation_row.state <> 'succeeded'
       OR operation_row.finished_at IS NULL OR operation_row.finished_at >= NEW.cleanup_cutoff
       OR NOT EXISTS (SELECT 1 FROM matching_acquisition acquisition
                       WHERE acquisition.run_id = NEW.run_id AND acquisition.intent_version = 3)
       OR EXISTS (SELECT 1 FROM matching_run run WHERE run.id = NEW.run_id AND run.state <> 'succeeded')
        OR EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id = NEW.run_id)
        OR EXISTS (SELECT 1 FROM incoming_match_evaluation evaluation
                    WHERE evaluation.target_run_id = NEW.run_id OR evaluation.frozen_run_id = NEW.run_id)
       OR EXISTS (SELECT 1 FROM matching_apply_request request JOIN operation child ON child.id = request.apply_operation_id
                   WHERE request.parent_run_id = NEW.run_id AND
                      (child.state <> 'succeeded' OR child.finished_at IS NULL OR child.finished_at >= NEW.cleanup_cutoff
                       OR child.state IN ('queued','running') OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                           AND job.state IN ('available','pending','running','retryable','scheduled'))))
        OR EXISTS (SELECT 1 FROM matching_provider_dependency dependency JOIN operation child ON child.id = dependency.request_id
                    WHERE dependency.parent_run_id = NEW.run_id AND
                      (dependency.terminal_outcome IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                         OR child.finished_at >= NEW.cleanup_cutoff
                        OR child.state IN ('queued','running')
                       OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                                  AND job.state IN ('available','pending','running','retryable','scheduled'))))
        OR EXISTS (SELECT 1 FROM matching_run parent_run JOIN operation parent ON parent.id=parent_run.operation_id
                    JOIN river_job job ON job.id=parent.river_job_id WHERE parent_run.id=NEW.run_id
                      AND job.state IN ('available','pending','running','retryable','scheduled'))
       OR EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id = NEW.run_id AND delivery.consumed_at IS NULL)
        THEN
        RAISE EXCEPTION 'matching run is not eligible for proof retirement';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER matching_run_retirement_authorization_validate
    BEFORE INSERT ON matching_run_retirement_authorization
    FOR EACH ROW EXECUTE FUNCTION validate_matching_run_retirement_authorization();

CREATE FUNCTION reject_matching_run_retirement_authorization_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.transaction_id = pg_current_xact_id() THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching run retirement authorizations are write-once';
END;
$$;
CREATE TRIGGER matching_run_retirement_authorization_immutable
    BEFORE UPDATE OR DELETE ON matching_run_retirement_authorization
    FOR EACH ROW EXECUTE FUNCTION reject_matching_run_retirement_authorization_mutation();

CREATE FUNCTION matching_run_retirement_is_authorized(target_run uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM matching_run_retirement_authorization
                    WHERE run_id = target_run AND transaction_id = pg_current_xact_id())
$$;

CREATE OR REPLACE FUNCTION reject_matching_fallback_authority_seal_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback authority seals are write-once';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_acquisition_phase_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching acquisition phase records are append-only';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_acquisition_seal_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching acquisition seals are write-once';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_fallback_evaluation_handoff_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback evaluation handoffs are write-once';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_fallback_calculation_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback calculation receipts are immutable';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_fallback_search_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback search receipts are immutable';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_fallback_lookup_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback lookup receipts are immutable';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_fallback_request_registration_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback request registrations are immutable';
END;
$$;
CREATE OR REPLACE FUNCTION reject_matching_apply_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(
        (SELECT parent_run_id FROM matching_apply_request WHERE apply_operation_id = OLD.apply_operation_id)) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching apply receipts are immutable';
END;
$$;
CREATE OR REPLACE FUNCTION reject_frozen_matching_apply_request_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.schema_version = 2 AND matching_run_retirement_is_authorized(OLD.parent_run_id) THEN RETURN OLD; END IF;
    IF OLD.schema_version = 2 THEN RAISE EXCEPTION 'frozen matching apply requests are immutable'; END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
