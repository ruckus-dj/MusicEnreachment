DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_acquisition_phase) THEN
        RAISE EXCEPTION 'cannot roll back matching acquisition ledger with recorded phases';
    END IF;
END;
$$;

DROP TRIGGER matching_acquisition_phase_append_only ON matching_acquisition_phase;
DROP FUNCTION reject_matching_acquisition_phase_mutation();
DROP TABLE matching_acquisition_phase;
DROP TABLE matching_acquisition;
