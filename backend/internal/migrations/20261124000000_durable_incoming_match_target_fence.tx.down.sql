DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM incoming_match_evaluation WHERE fence_version = 2) THEN
        RAISE EXCEPTION 'cannot downgrade while version-2 incoming match evaluation headers exist';
    END IF;
END $$;

ALTER TABLE incoming_match_evaluation
    DROP CONSTRAINT incoming_match_evaluation_target_fence,
    DROP CONSTRAINT incoming_match_evaluation_target_capture_digest_length,
    DROP CONSTRAINT incoming_match_evaluation_target_capture_pair,
    DROP COLUMN target_capture_digest,
    DROP COLUMN target_capture,
    DROP COLUMN target_run_id,
    DROP COLUMN fence_version;
