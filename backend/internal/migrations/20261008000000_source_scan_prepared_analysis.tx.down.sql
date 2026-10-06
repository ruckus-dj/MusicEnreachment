DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM source_scan_candidate WHERE prepared_analysis IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot drop prepared source scan analysis while prepared data exists';
    END IF;
END $$;

ALTER TABLE source_scan_candidate
    DROP COLUMN prepared_analysis;
