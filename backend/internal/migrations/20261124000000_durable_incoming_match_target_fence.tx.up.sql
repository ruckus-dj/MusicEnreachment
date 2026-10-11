ALTER TABLE incoming_match_evaluation
    ADD COLUMN fence_version integer NOT NULL DEFAULT 1 CHECK (fence_version IN (1, 2)),
    ADD COLUMN target_run_id uuid,
    ADD COLUMN target_capture jsonb,
    ADD COLUMN target_capture_digest bytea,
    ADD CONSTRAINT incoming_match_evaluation_target_capture_pair CHECK (
        (target_capture IS NULL) = (target_capture_digest IS NULL)
    ),
    ADD CONSTRAINT incoming_match_evaluation_target_capture_digest_length CHECK (
        target_capture_digest IS NULL OR octet_length(target_capture_digest) = 32
    ),
    ADD CONSTRAINT incoming_match_evaluation_target_fence CHECK (
        (fence_version = 1 AND target_run_id IS NULL AND target_capture IS NULL AND target_capture_digest IS NULL)
        OR (fence_version = 2 AND target_run_id IS NOT NULL AND target_capture IS NOT NULL AND target_capture_digest IS NOT NULL)
    );
