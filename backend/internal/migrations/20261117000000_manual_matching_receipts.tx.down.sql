DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM manual_matching_receipt) THEN
        RAISE EXCEPTION 'manual matching receipts contain durable confirmed mappings';
    END IF;
END;
$$;

DROP TRIGGER manual_matching_receipt_write_once ON manual_matching_receipt;
DROP FUNCTION reject_manual_matching_receipt_mutation();
DROP TABLE manual_matching_receipt;
