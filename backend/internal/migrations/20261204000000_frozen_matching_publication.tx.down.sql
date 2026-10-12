DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM incoming_match_evaluation
        WHERE frozen_authority_version IS NOT NULL
           OR frozen_run_id IS NOT NULL
           OR frozen_wait_revision IS NOT NULL
           OR frozen_seal_digest IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back frozen matching publication with stored frozen authority headers';
    END IF;
END;
$$;

ALTER TABLE incoming_match_evaluation
    DROP CONSTRAINT incoming_match_evaluation_frozen_seal_fk,
    DROP CONSTRAINT incoming_match_evaluation_frozen_target_run,
    DROP CONSTRAINT incoming_match_evaluation_frozen_authority_all_or_none,
    DROP COLUMN frozen_seal_digest,
    DROP COLUMN frozen_wait_revision,
    DROP COLUMN frozen_run_id,
    DROP COLUMN frozen_authority_version,
    ALTER COLUMN provider_snapshot_revision SET NOT NULL,
    ALTER COLUMN cache_key SET NOT NULL,
    ALTER COLUMN provider_endpoint SET NOT NULL,
    ALTER COLUMN provider_namespace SET NOT NULL;

ALTER TABLE matching_fallback_authority_seal
    DROP CONSTRAINT matching_fallback_authority_seal_run_wait_digest_key;
