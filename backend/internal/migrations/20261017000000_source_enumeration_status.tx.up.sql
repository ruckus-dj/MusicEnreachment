ALTER TABLE source_location
    DROP CONSTRAINT source_location_probe_status_allowed,
    ADD CONSTRAINT source_location_probe_status_allowed
        CHECK (probe_status IN ('not_analyzed', 'audio', 'no_audio', 'probe_error'));

ALTER TABLE source_scan_candidate
    DROP CONSTRAINT source_scan_candidate_probe_status_allowed,
    ADD CONSTRAINT source_scan_candidate_probe_status_allowed
        CHECK (probe_status IN ('not_analyzed', 'audio', 'no_audio', 'probe_error'));
