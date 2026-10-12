DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM matching_fallback_search_receipt
        WHERE payload_digest_version = 2
    ) THEN
        RAISE EXCEPTION 'cannot roll back search digest version while version-2 receipts exist';
    END IF;
END
$$;

ALTER TABLE matching_fallback_search_receipt
    DROP COLUMN payload_digest_version;
