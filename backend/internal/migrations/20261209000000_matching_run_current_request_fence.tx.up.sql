ALTER TABLE matching_run_target
    ADD COLUMN reconciliation_generation bigint NULL
        CHECK (reconciliation_generation IS NULL OR reconciliation_generation > 0);
