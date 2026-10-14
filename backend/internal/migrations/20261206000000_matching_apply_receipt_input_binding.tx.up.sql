ALTER TABLE matching_apply_request
    ADD CONSTRAINT matching_apply_request_operation_input_unique UNIQUE (apply_operation_id, input_sha256);

ALTER TABLE matching_apply_receipt
    ADD COLUMN input_sha256 bytea,
    ADD CONSTRAINT matching_apply_receipt_input_sha256_length CHECK (
        input_sha256 IS NULL OR octet_length(input_sha256) = 32
    ),
    ADD CONSTRAINT matching_apply_receipt_request_input_fk
        FOREIGN KEY (apply_operation_id, input_sha256)
        REFERENCES matching_apply_request(apply_operation_id, input_sha256)
        DEFERRABLE INITIALLY IMMEDIATE;

CREATE FUNCTION validate_matching_apply_receipt_input_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE request_schema integer;
BEGIN
    SELECT schema_version INTO request_schema
    FROM matching_apply_request WHERE apply_operation_id = NEW.apply_operation_id;
    IF request_schema = 2 AND (NEW.input_sha256 IS NULL OR octet_length(NEW.input_sha256) <> 32) THEN
        RAISE EXCEPTION 'schema-2 matching apply receipt requires its input digest';
    END IF;
    IF request_schema = 1 AND NEW.input_sha256 IS NOT NULL THEN
        RAISE EXCEPTION 'schema-1 matching apply receipt cannot carry an input digest';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER matching_apply_receipt_input_binding
    BEFORE INSERT ON matching_apply_receipt
    FOR EACH ROW EXECUTE FUNCTION validate_matching_apply_receipt_input_binding();
