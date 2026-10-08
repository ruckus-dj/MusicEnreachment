DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM source_location WHERE probe_status = 'not_analyzed')
       OR EXISTS (SELECT 1 FROM source_scan_candidate WHERE probe_status = 'not_analyzed') THEN
        RAISE EXCEPTION 'cannot remove not_analyzed status while rows use it';
    END IF;
END $$;

ALTER TABLE source_scan_candidate
    DROP CONSTRAINT source_scan_candidate_probe_status_allowed,
    ADD CONSTRAINT source_scan_candidate_probe_status_allowed
        CHECK (probe_status IN ('audio', 'no_audio', 'probe_error'));

ALTER TABLE source_location
    DROP CONSTRAINT source_location_probe_status_allowed,
    ADD CONSTRAINT source_location_probe_status_allowed
        CHECK (probe_status IN ('audio', 'no_audio', 'probe_error'));
