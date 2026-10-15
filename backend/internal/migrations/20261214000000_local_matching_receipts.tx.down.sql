DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM local_matching_receipt) THEN
        RAISE EXCEPTION 'local matching receipts contain confirmation data; refusing destructive rollback';
    END IF;
END $$;

DROP TABLE local_matching_receipt;
