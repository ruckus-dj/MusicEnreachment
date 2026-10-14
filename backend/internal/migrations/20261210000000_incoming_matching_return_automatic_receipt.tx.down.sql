DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM incoming_group_return_automatic_receipt) THEN
        RAISE EXCEPTION 'cannot roll back return-to-automatic receipts while rows remain';
    END IF;
END;
$$;

DROP TABLE incoming_group_return_automatic_receipt;
