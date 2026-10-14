DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_apply_receipt WHERE input_sha256 IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back schema-2 matching apply receipt bindings while receipts exist';
    END IF;
END;
$$;

ALTER TABLE matching_apply_receipt
    DROP CONSTRAINT matching_apply_receipt_request_input_fk,
    DROP CONSTRAINT matching_apply_receipt_input_sha256_length,
    DROP COLUMN input_sha256;

DROP TRIGGER matching_apply_receipt_input_binding ON matching_apply_receipt;
DROP FUNCTION validate_matching_apply_receipt_input_binding();

ALTER TABLE matching_apply_request
    DROP CONSTRAINT matching_apply_request_operation_input_unique;
