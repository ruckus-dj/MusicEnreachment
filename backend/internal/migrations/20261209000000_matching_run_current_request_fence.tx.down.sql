DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_run_target WHERE reconciliation_generation IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back current-request run fences while referenced matching runs remain';
    END IF;
END
$$;

ALTER TABLE matching_run_target DROP COLUMN reconciliation_generation;
