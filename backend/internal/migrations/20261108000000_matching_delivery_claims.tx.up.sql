ALTER TABLE matching_run
    ADD COLUMN active_river_job_id bigint,
    ADD COLUMN active_river_attempt integer,
    ADD COLUMN active_execution_epoch bigint,
    ADD CONSTRAINT matching_run_active_claim_complete CHECK (
        (active_river_job_id IS NULL AND active_river_attempt IS NULL AND active_execution_epoch IS NULL)
        OR (active_river_job_id IS NOT NULL AND active_river_attempt IS NOT NULL AND active_execution_epoch IS NOT NULL)
    );

ALTER TABLE matching_delivery ADD COLUMN operation_attempt integer;
UPDATE matching_delivery delivery SET operation_attempt=operation.attempt
FROM matching_run run JOIN operation ON operation.id=run.operation_id
WHERE delivery.run_id=run.id;
ALTER TABLE matching_delivery ALTER COLUMN operation_attempt SET NOT NULL,
    ADD CONSTRAINT matching_delivery_attempt_positive CHECK (operation_attempt > 0);
