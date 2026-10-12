DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_fallback_calculation_receipt) THEN
        RAISE EXCEPTION 'cannot roll back populated matching fallback calculation receipts';
    END IF;
END;
$$;

DROP TRIGGER matching_fallback_calculation_receipt_immutable ON matching_fallback_calculation_receipt;
DROP FUNCTION reject_matching_fallback_calculation_receipt_mutation();
DROP TABLE matching_fallback_calculation_receipt;
