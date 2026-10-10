ALTER TABLE matching_run
    DROP CONSTRAINT matching_run_active_claim_complete,
    DROP COLUMN active_execution_epoch,
    DROP COLUMN active_river_attempt,
    DROP COLUMN active_river_job_id;

ALTER TABLE matching_delivery
    DROP CONSTRAINT matching_delivery_attempt_positive,
    DROP COLUMN operation_attempt;
