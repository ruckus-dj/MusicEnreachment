DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM matching_fallback_search_receipt) THEN
		RAISE EXCEPTION 'cannot roll back matching fallback receipts while accepted authority is retained';
	END IF;
END $$;

DROP TRIGGER IF EXISTS matching_fallback_search_receipt_immutable ON matching_fallback_search_receipt;
DROP FUNCTION IF EXISTS reject_matching_fallback_search_receipt_mutation();
DROP TABLE IF EXISTS matching_fallback_search_receipt;
