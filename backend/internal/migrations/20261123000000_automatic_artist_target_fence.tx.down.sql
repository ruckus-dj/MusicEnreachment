DROP TABLE incoming_matching_automatic_admission;
ALTER TABLE matching_run_target
    DROP CONSTRAINT matching_run_target_capture_digest_length,
    DROP CONSTRAINT matching_run_target_capture_pair,
    DROP COLUMN target_capture_digest,
    DROP COLUMN target_capture;
ALTER TABLE matching_run DROP COLUMN fence_version;
