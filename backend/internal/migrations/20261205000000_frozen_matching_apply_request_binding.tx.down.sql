DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_apply_request WHERE schema_version = 2)
       OR EXISTS (SELECT 1 FROM matching_apply_receipt receipt
                  JOIN matching_apply_request request USING (apply_operation_id)
                  WHERE request.schema_version = 2) THEN
        RAISE EXCEPTION 'cannot roll back frozen matching apply request bindings while schema-2 requests or receipts exist';
    END IF;
END;
$$;

DROP TRIGGER frozen_matching_apply_request_write_once ON matching_apply_request;
DROP FUNCTION reject_frozen_matching_apply_request_mutation();
ALTER TABLE matching_apply_request
    DROP CONSTRAINT matching_apply_request_frozen_seal_fk,
    DROP CONSTRAINT matching_apply_request_v2_binding,
    DROP CONSTRAINT matching_apply_request_schema_version,
    DROP COLUMN target_capture_sha256,
    DROP COLUMN seal_digest,
    DROP COLUMN seal_wait_revision,
    DROP COLUMN parent_execution_epoch,
    DROP COLUMN frozen_authority_version,
    DROP COLUMN input_sha256,
    DROP COLUMN schema_version;
