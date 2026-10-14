ALTER TABLE matching_run
    ADD COLUMN quiesced_at timestamptz NULL;

ALTER TABLE matching_run
    ADD CONSTRAINT matching_run_quiescence_has_no_active_owner
    CHECK (quiesced_at IS NULL OR
        (active_river_job_id IS NULL AND active_river_attempt IS NULL AND active_execution_epoch IS NULL));
