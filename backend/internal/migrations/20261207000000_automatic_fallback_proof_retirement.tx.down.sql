DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_run_retirement_authorization) THEN
        RAISE EXCEPTION 'cannot roll back fallback retirement while authorizations remain';
    END IF;
END;
$$;

DROP FUNCTION reject_matching_fallback_authority_seal_mutation() CASCADE;
CREATE FUNCTION reject_matching_fallback_authority_seal_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'matching fallback authority seals are write-once'; END; $$;
CREATE TRIGGER matching_fallback_authority_seal_write_once BEFORE UPDATE OR DELETE ON matching_fallback_authority_seal
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_authority_seal_mutation();

DROP FUNCTION reject_matching_fallback_evaluation_handoff_mutation() CASCADE;
CREATE FUNCTION reject_matching_fallback_evaluation_handoff_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'matching fallback evaluation handoffs are write-once'; END; $$;
CREATE TRIGGER matching_fallback_evaluation_handoff_write_once BEFORE UPDATE OR DELETE ON matching_fallback_evaluation_handoff
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_evaluation_handoff_mutation();

DROP FUNCTION reject_matching_fallback_calculation_receipt_mutation() CASCADE;
CREATE FUNCTION reject_matching_fallback_calculation_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'matching fallback calculation receipts are immutable';
END; $$;
CREATE TRIGGER matching_fallback_calculation_receipt_immutable BEFORE UPDATE OR DELETE ON matching_fallback_calculation_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_calculation_receipt_mutation();

DROP FUNCTION reject_matching_fallback_search_receipt_mutation() CASCADE;
CREATE FUNCTION reject_matching_fallback_search_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'matching fallback search receipts are immutable';
END; $$;
CREATE TRIGGER matching_fallback_search_receipt_immutable BEFORE UPDATE OR DELETE ON matching_fallback_search_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_search_receipt_mutation();

DROP FUNCTION reject_matching_fallback_lookup_receipt_mutation() CASCADE;
CREATE FUNCTION reject_matching_fallback_lookup_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'matching fallback lookup receipts are immutable';
END; $$;
CREATE TRIGGER matching_fallback_lookup_receipt_immutable BEFORE UPDATE OR DELETE ON matching_fallback_lookup_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_lookup_receipt_mutation();

DROP FUNCTION reject_matching_fallback_request_registration_mutation() CASCADE;
CREATE FUNCTION reject_matching_fallback_request_registration_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'matching fallback request registrations are immutable';
END; $$;
CREATE TRIGGER matching_fallback_request_registration_immutable BEFORE UPDATE OR DELETE ON matching_fallback_request_registration
    FOR EACH ROW EXECUTE FUNCTION reject_matching_fallback_request_registration_mutation();

DROP FUNCTION reject_matching_apply_receipt_mutation() CASCADE;
CREATE FUNCTION reject_matching_apply_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'matching apply receipts are immutable'; END; $$;
CREATE TRIGGER matching_apply_receipt_write_once BEFORE UPDATE OR DELETE ON matching_apply_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_matching_apply_receipt_mutation();

DROP FUNCTION reject_frozen_matching_apply_request_mutation() CASCADE;
CREATE FUNCTION reject_frozen_matching_apply_request_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
    IF OLD.schema_version = 2 THEN RAISE EXCEPTION 'frozen matching apply requests are immutable'; END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END; $$;
CREATE TRIGGER frozen_matching_apply_request_write_once BEFORE UPDATE OR DELETE ON matching_apply_request
    FOR EACH ROW EXECUTE FUNCTION reject_frozen_matching_apply_request_mutation();

CREATE OR REPLACE FUNCTION reject_matching_acquisition_phase_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'matching acquisition phase records are append-only'; END; $$;
CREATE OR REPLACE FUNCTION reject_matching_acquisition_seal_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'matching acquisition seals are write-once'; END; $$;

DROP FUNCTION matching_run_retirement_is_authorized(uuid);
DROP TRIGGER matching_run_retirement_authorization_validate ON matching_run_retirement_authorization;
DROP TRIGGER matching_run_retirement_authorization_immutable ON matching_run_retirement_authorization;
DROP FUNCTION validate_matching_run_retirement_authorization();
DROP FUNCTION reject_matching_run_retirement_authorization_mutation();
DROP TABLE matching_run_retirement_authorization;
