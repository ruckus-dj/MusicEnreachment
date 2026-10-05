DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM source_analysis_work)
       OR EXISTS (SELECT 1 FROM source_analysis_step)
       OR EXISTS (SELECT 1 FROM operation_source_work_hold)
       OR EXISTS (SELECT 1 FROM operation_tool_read_hold)
       OR EXISTS (SELECT 1 FROM media_fingerprint_result)
       OR EXISTS (SELECT 1 FROM media_fingerprint_cache)
       OR EXISTS (SELECT 1 FROM media_variant WHERE source_sha256 IS NOT NULL
                    OR sha256_calculated_at IS NOT NULL OR sha256_algorithm IS NOT NULL
                    OR sha256_applied_operation_id IS NOT NULL OR audio_stream_count IS NOT NULL
                    OR ffprobe_version IS NULL OR ffprobe_json IS NULL OR analysis_policy_version IS NULL
                    OR observed_tags IS NULL OR inspected_at IS NULL OR applied_operation_id IS NULL)
       OR EXISTS (SELECT 1 FROM source_scan_candidate WHERE source_sha256 IS NOT NULL OR sha256_calculated_at IS NOT NULL
                    OR sha256_applied_operation_id IS NOT NULL OR audio_stream_count IS NOT NULL
                    OR ffprobe_version IS NOT NULL OR ffprobe_json IS NOT NULL OR analysis_policy_version IS NOT NULL
                    OR observed_tags IS NOT NULL OR inspected_at IS NOT NULL OR probe_applied_operation_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot rollback automatic source analysis: source-analysis artifacts exist';
    END IF;
END $$;

DROP TRIGGER media_fingerprint_cache_immutable ON media_fingerprint_cache;
DROP TRIGGER media_fingerprint_result_immutable ON media_fingerprint_result;
DROP FUNCTION source_analysis_immutable_result();

ALTER TABLE source_analysis_step DROP CONSTRAINT source_analysis_step_work_hold_fkey;
DROP TABLE source_analysis_step;
DROP TABLE operation_tool_read_hold;
DROP TABLE operation_source_work_hold;
ALTER TABLE operation DROP CONSTRAINT operation_id_attempt_job_unique;
DROP TABLE source_analysis_work;
DROP TABLE media_fingerprint_cache;
DROP TABLE media_fingerprint_result;

ALTER TABLE source_scan_candidate
    DROP CONSTRAINT source_scan_candidate_probe_group_all_or_none,
    DROP CONSTRAINT source_scan_candidate_sha256_provenance,
    DROP CONSTRAINT source_scan_candidate_sha256_length,
    DROP COLUMN probe_applied_operation_id,
    DROP COLUMN inspected_at,
    DROP COLUMN observed_tags,
    DROP COLUMN analysis_policy_version,
    DROP COLUMN ffprobe_json,
    DROP COLUMN ffprobe_version,
    DROP COLUMN audio_stream_count,
    DROP COLUMN sha256_applied_operation_id,
    DROP COLUMN sha256_calculated_at,
    DROP COLUMN source_sha256;

ALTER TABLE source_location DROP CONSTRAINT source_location_id_root_unique;
ALTER TABLE media_variant
    DROP CONSTRAINT media_variant_source_sha256_unique,
    DROP CONSTRAINT media_variant_probe_values_valid,
    DROP CONSTRAINT media_variant_probe_group_all_or_none,
    DROP CONSTRAINT media_variant_audio_stream_count_nonnegative,
    DROP CONSTRAINT media_variant_sha256_provenance,
    DROP CONSTRAINT media_variant_sha256_length,
    ALTER COLUMN ffprobe_version SET NOT NULL,
    ALTER COLUMN ffprobe_json SET NOT NULL,
    ALTER COLUMN analysis_policy_version SET NOT NULL,
    ALTER COLUMN observed_tags SET NOT NULL,
    ALTER COLUMN inspected_at SET NOT NULL,
    ALTER COLUMN applied_operation_id SET NOT NULL,
    ALTER COLUMN analysis_policy_version SET DEFAULT 1,
    ALTER COLUMN observed_tags SET DEFAULT '{}'::jsonb,
    DROP COLUMN audio_stream_count,
    DROP COLUMN sha256_applied_operation_id,
    DROP COLUMN sha256_algorithm,
    DROP COLUMN sha256_calculated_at,
    DROP COLUMN source_sha256;
