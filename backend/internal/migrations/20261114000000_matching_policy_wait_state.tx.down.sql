DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_run WHERE state='waiting_policy') THEN
        RAISE EXCEPTION 'cannot roll back matching policy wait state while matching runs are waiting for policy';
    END IF;
END;
$$;

DROP INDEX matching_run_active_idx;
CREATE INDEX matching_run_active_idx ON matching_run(state)
    WHERE state IN ('queued', 'running', 'waiting_provider');

ALTER TABLE matching_run DROP CONSTRAINT matching_run_state_check;
ALTER TABLE matching_run ADD CONSTRAINT matching_run_state_check
    CHECK (state IN ('queued', 'running', 'waiting_provider', 'succeeded', 'failed'));
