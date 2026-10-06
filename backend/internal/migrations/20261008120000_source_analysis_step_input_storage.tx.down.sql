DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM source_analysis_step WHERE input_snapshot IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back source-analysis step input storage while inputs exist';
    END IF;
END
$$;

ALTER TABLE source_analysis_step
    DROP COLUMN input_snapshot;
