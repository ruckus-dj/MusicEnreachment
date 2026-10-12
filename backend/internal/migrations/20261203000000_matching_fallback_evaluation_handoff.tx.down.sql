DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_fallback_evaluation_handoff) THEN
        RAISE EXCEPTION 'cannot roll back populated matching fallback evaluation handoff migration';
    END IF;
END;
$$;

DROP TRIGGER matching_fallback_evaluation_handoff_write_once ON matching_fallback_evaluation_handoff;
DROP FUNCTION reject_matching_fallback_evaluation_handoff_mutation();
DROP TABLE matching_fallback_evaluation_handoff;
