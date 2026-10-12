ALTER TABLE incoming_match_evaluation
    ALTER COLUMN provider_namespace DROP NOT NULL,
    ALTER COLUMN provider_endpoint DROP NOT NULL,
    ALTER COLUMN cache_key DROP NOT NULL,
    ALTER COLUMN provider_snapshot_revision DROP NOT NULL;

ALTER TABLE matching_fallback_authority_seal
    ADD CONSTRAINT matching_fallback_authority_seal_run_wait_digest_key
        UNIQUE (run_id, wait_revision, authority_digest);

ALTER TABLE incoming_match_evaluation
    ADD COLUMN frozen_authority_version integer,
    ADD COLUMN frozen_run_id uuid,
    ADD COLUMN frozen_wait_revision bigint,
    ADD COLUMN frozen_seal_digest bytea,
    ADD CONSTRAINT incoming_match_evaluation_frozen_authority_all_or_none CHECK (
        (frozen_authority_version IS NULL AND frozen_run_id IS NULL AND frozen_wait_revision IS NULL AND frozen_seal_digest IS NULL)
        OR
        (frozen_authority_version IS NOT NULL AND frozen_run_id IS NOT NULL AND frozen_wait_revision IS NOT NULL AND frozen_seal_digest IS NOT NULL
            AND frozen_authority_version = 3 AND frozen_wait_revision > 0 AND octet_length(frozen_seal_digest) = 32
            AND target_run_id IS NOT NULL AND fence_version = 2 AND matching_authority_mode = 'automatic')
    ),
    ADD CONSTRAINT incoming_match_evaluation_frozen_target_run CHECK (
        frozen_run_id IS NULL OR target_run_id = frozen_run_id
    ),
    ADD CONSTRAINT incoming_match_evaluation_frozen_seal_fk
        FOREIGN KEY (frozen_run_id, frozen_wait_revision, frozen_seal_digest)
        REFERENCES matching_fallback_authority_seal(run_id, wait_revision, authority_digest) ON DELETE RESTRICT;
