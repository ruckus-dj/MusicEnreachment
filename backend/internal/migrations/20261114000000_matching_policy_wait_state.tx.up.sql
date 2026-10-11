ALTER TABLE matching_run DROP CONSTRAINT matching_run_state_check;
ALTER TABLE matching_run ADD CONSTRAINT matching_run_state_check
    CHECK (state IN ('queued', 'running', 'waiting_provider', 'waiting_policy', 'succeeded', 'failed'));

DROP INDEX matching_run_active_idx;
CREATE INDEX matching_run_active_idx ON matching_run(state)
    WHERE state IN ('queued', 'running', 'waiting_provider', 'waiting_policy');
