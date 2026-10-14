ALTER TABLE matching_apply_request
    ADD COLUMN schema_version integer NOT NULL DEFAULT 1,
    ADD COLUMN input_sha256 bytea,
    ADD COLUMN frozen_authority_version integer,
    ADD COLUMN parent_execution_epoch bigint,
    ADD COLUMN seal_wait_revision bigint,
    ADD COLUMN seal_digest bytea,
    ADD COLUMN target_capture_sha256 bytea;

ALTER TABLE matching_apply_request
    ADD CONSTRAINT matching_apply_request_schema_version CHECK (schema_version IN (1, 2)),
    ADD CONSTRAINT matching_apply_request_v2_binding CHECK (
        (schema_version = 1 AND input_sha256 IS NULL AND frozen_authority_version IS NULL AND parent_execution_epoch IS NULL
            AND seal_wait_revision IS NULL AND seal_digest IS NULL AND target_capture_sha256 IS NULL)
        OR (schema_version = 2 AND input_sha256 IS NOT NULL AND octet_length(input_sha256) = 32
            AND frozen_authority_version IS NOT NULL AND frozen_authority_version = 3
            AND parent_execution_epoch IS NOT NULL AND parent_execution_epoch > 0
            AND seal_wait_revision IS NOT NULL AND seal_wait_revision > 0
            AND seal_digest IS NOT NULL AND octet_length(seal_digest) = 32
            AND target_capture_sha256 IS NOT NULL AND octet_length(target_capture_sha256) = 32)
    ),
    ADD CONSTRAINT matching_apply_request_frozen_seal_fk
        FOREIGN KEY (parent_run_id, seal_wait_revision, seal_digest)
        REFERENCES matching_fallback_authority_seal(run_id, wait_revision, authority_digest)
        DEFERRABLE INITIALLY IMMEDIATE;

CREATE FUNCTION reject_frozen_matching_apply_request_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.schema_version = 2 THEN
        RAISE EXCEPTION 'frozen matching apply requests are immutable';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER frozen_matching_apply_request_write_once
    BEFORE UPDATE OR DELETE ON matching_apply_request
    FOR EACH ROW EXECUTE FUNCTION reject_frozen_matching_apply_request_mutation();
