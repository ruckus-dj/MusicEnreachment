DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_fallback_lookup_receipt) THEN
        RAISE EXCEPTION 'cannot roll back while fallback lookup receipts remain';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS matching_fallback_lookup_receipt_immutable ON matching_fallback_lookup_receipt;
DROP FUNCTION IF EXISTS reject_matching_fallback_lookup_receipt_mutation();
DROP TABLE IF EXISTS matching_fallback_lookup_receipt;
