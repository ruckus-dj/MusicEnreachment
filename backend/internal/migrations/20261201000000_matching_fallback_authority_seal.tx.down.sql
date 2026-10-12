DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_fallback_authority_seal) THEN
        RAISE EXCEPTION 'cannot remove populated matching fallback authority seals';
    END IF;
END;
$$;

DROP TRIGGER matching_fallback_authority_seal_write_once ON matching_fallback_authority_seal;
DROP FUNCTION reject_matching_fallback_authority_seal_mutation();
DROP TABLE matching_fallback_authority_seal;
