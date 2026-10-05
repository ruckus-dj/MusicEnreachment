-- Normalized, independently applicable source-analysis state. Existing rows are
-- deliberately left without invented digest, probe, or fingerprint results.
ALTER TABLE media_variant
    ADD COLUMN source_sha256 bytea,
    ADD COLUMN sha256_calculated_at timestamptz,
    ADD COLUMN sha256_algorithm text,
    ADD COLUMN sha256_applied_operation_id uuid,
    ADD COLUMN audio_stream_count integer;

ALTER TABLE media_variant
    ALTER COLUMN ffprobe_version DROP NOT NULL,
    ALTER COLUMN ffprobe_json DROP NOT NULL,
    ALTER COLUMN analysis_policy_version DROP NOT NULL,
    ALTER COLUMN observed_tags DROP NOT NULL,
    ALTER COLUMN inspected_at DROP NOT NULL,
    ALTER COLUMN applied_operation_id DROP NOT NULL,
    ALTER COLUMN analysis_policy_version DROP DEFAULT,
    ALTER COLUMN observed_tags DROP DEFAULT;

ALTER TABLE media_variant
    ADD CONSTRAINT media_variant_sha256_length CHECK (source_sha256 IS NULL OR octet_length(source_sha256) = 32),
    ADD CONSTRAINT media_variant_sha256_provenance CHECK (
        (source_sha256 IS NULL AND sha256_calculated_at IS NULL AND sha256_algorithm IS NULL AND sha256_applied_operation_id IS NULL)
        OR (source_sha256 IS NOT NULL AND sha256_calculated_at IS NOT NULL AND sha256_algorithm IS NOT NULL AND sha256_algorithm <> '' AND sha256_applied_operation_id IS NOT NULL)
    ),
    ADD CONSTRAINT media_variant_audio_stream_count_nonnegative CHECK (audio_stream_count IS NULL OR audio_stream_count >= 0),
    ADD CONSTRAINT media_variant_probe_group_all_or_none CHECK (
        (ffprobe_version IS NULL AND ffprobe_json IS NULL AND analysis_policy_version IS NULL AND observed_tags IS NULL AND inspected_at IS NULL AND applied_operation_id IS NULL)
        OR (ffprobe_version IS NOT NULL AND ffprobe_json IS NOT NULL AND analysis_policy_version IS NOT NULL AND observed_tags IS NOT NULL AND inspected_at IS NOT NULL AND applied_operation_id IS NOT NULL)
    ),
    ADD CONSTRAINT media_variant_probe_values_valid CHECK (
        ffprobe_version IS NULL OR (ffprobe_version <> '' AND analysis_policy_version >= 1 AND jsonb_typeof(observed_tags) = 'object')
    );
ALTER TABLE media_variant ADD CONSTRAINT media_variant_source_sha256_unique UNIQUE (source_sha256);

ALTER TABLE source_location ADD CONSTRAINT source_location_id_root_unique UNIQUE (id, source_root_id);

ALTER TABLE source_scan_candidate
    ADD COLUMN source_sha256 bytea,
    ADD COLUMN sha256_calculated_at timestamptz,
    ADD COLUMN sha256_applied_operation_id uuid,
    ADD COLUMN audio_stream_count integer,
    ADD COLUMN ffprobe_version text,
    ADD COLUMN ffprobe_json jsonb,
    ADD COLUMN analysis_policy_version integer,
    ADD COLUMN observed_tags jsonb,
    ADD COLUMN inspected_at timestamptz,
    ADD COLUMN probe_applied_operation_id uuid;
ALTER TABLE source_scan_candidate ADD CONSTRAINT source_scan_candidate_sha256_length CHECK (source_sha256 IS NULL OR octet_length(source_sha256) = 32);
ALTER TABLE source_scan_candidate ADD CONSTRAINT source_scan_candidate_sha256_provenance CHECK (
    (source_sha256 IS NULL AND sha256_calculated_at IS NULL AND sha256_applied_operation_id IS NULL)
    OR (source_sha256 IS NOT NULL AND sha256_calculated_at IS NOT NULL AND sha256_applied_operation_id IS NOT NULL)
);
ALTER TABLE source_scan_candidate ADD CONSTRAINT source_scan_candidate_probe_group_all_or_none CHECK (
    (ffprobe_version IS NULL AND ffprobe_json IS NULL AND analysis_policy_version IS NULL AND observed_tags IS NULL AND inspected_at IS NULL AND probe_applied_operation_id IS NULL)
    OR (ffprobe_version IS NOT NULL AND ffprobe_json IS NOT NULL AND analysis_policy_version IS NOT NULL AND analysis_policy_version >= 1 AND observed_tags IS NOT NULL AND inspected_at IS NOT NULL AND probe_applied_operation_id IS NOT NULL)
);

CREATE TABLE media_fingerprint_result (
    id uuid PRIMARY KEY,
    fpcalc_version text NOT NULL CHECK (fpcalc_version <> ''),
    version_banner text NOT NULL CHECK (version_banner <> ''),
    algorithm_namespace text NOT NULL CHECK (algorithm_namespace <> ''),
    algorithm_id smallint NOT NULL CHECK (algorithm_id BETWEEN 0 AND 255),
    fingerprint text NOT NULL CHECK (fingerprint <> ''),
    reported_duration double precision NOT NULL CHECK (reported_duration >= 0 AND reported_duration < 'Infinity'::float8),
    calculated_at timestamptz NOT NULL,
    applied_operation_id uuid NOT NULL,
    parser_contract_version integer NOT NULL CHECK (parser_contract_version > 0),
    UNIQUE (id, fpcalc_version)
);

CREATE TABLE media_fingerprint_cache (
    source_sha256 bytea NOT NULL REFERENCES media_variant(source_sha256) ON DELETE RESTRICT,
    fpcalc_version text NOT NULL,
    result_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_sha256, fpcalc_version),
    FOREIGN KEY (result_id, fpcalc_version) REFERENCES media_fingerprint_result(id, fpcalc_version) ON DELETE RESTRICT
);

CREATE TABLE source_analysis_work (
    id uuid PRIMARY KEY,
    location_id uuid NOT NULL UNIQUE,
    source_root_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL CHECK (relative_path <> ''),
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    mtime timestamptz NOT NULL,
    sha256_enabled boolean NOT NULL,
    origin_scan_operation_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (location_id, source_root_id) REFERENCES source_location(id, source_root_id) ON DELETE CASCADE
);

ALTER TABLE operation ADD CONSTRAINT operation_id_attempt_job_unique UNIQUE (id, attempt, river_job_id);

CREATE TABLE source_analysis_step (
    work_id uuid NOT NULL REFERENCES source_analysis_work(id) ON DELETE CASCADE,
    step text NOT NULL CHECK (step IN ('sha256','probe','fingerprint')),
    state text NOT NULL CHECK (state IN ('not_requested','pending','queued','running','succeeded','failed','skipped')),
    step_attempt integer NOT NULL DEFAULT 0 CHECK (step_attempt >= 0),
    safe_error text,
    skip_reason text,
    updated_at timestamptz NOT NULL DEFAULT now(),
    execution_operation_id uuid,
    execution_operation_attempt integer,
    execution_job_id bigint,
    last_operation_id uuid,
    success_sha_variant_id uuid REFERENCES media_variant(id) ON DELETE RESTRICT,
    success_probe_variant_id uuid REFERENCES media_variant(id) ON DELETE RESTRICT,
    success_fingerprint_result_id uuid REFERENCES media_fingerprint_result(id) ON DELETE RESTRICT,
    success_reuse_origin text CHECK (success_reuse_origin IS NULL OR success_reuse_origin IN ('executed','sha256','work')),
    PRIMARY KEY (work_id, step),
    FOREIGN KEY (execution_operation_id, execution_operation_attempt, execution_job_id)
        REFERENCES operation(id, attempt, river_job_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT source_analysis_step_execution_all_or_none CHECK (
        (execution_operation_id IS NULL AND execution_operation_attempt IS NULL AND execution_job_id IS NULL)
        OR (execution_operation_id IS NOT NULL AND execution_operation_attempt > 0 AND execution_job_id IS NOT NULL)
    ),
    CONSTRAINT source_analysis_step_active_has_execution CHECK (state NOT IN ('queued','running') OR execution_operation_id IS NOT NULL),
    CONSTRAINT source_analysis_step_result_matches_step CHECK (
        (success_sha_variant_id IS NULL OR step = 'sha256') AND
        (success_probe_variant_id IS NULL OR step = 'probe') AND
        (success_fingerprint_result_id IS NULL OR step = 'fingerprint')
    ),
    CONSTRAINT source_analysis_step_success_has_result CHECK (
        state <> 'succeeded' OR
        (step = 'sha256' AND success_sha_variant_id IS NOT NULL) OR
        (step = 'probe' AND success_probe_variant_id IS NOT NULL) OR
        (step = 'fingerprint' AND success_fingerprint_result_id IS NOT NULL)
    ),
    CONSTRAINT source_analysis_step_reuse_origin_shape CHECK (state <> 'succeeded' OR success_reuse_origin IS NOT NULL),
    CONSTRAINT source_analysis_step_error_shape CHECK (
        (state = 'failed' AND safe_error IS NOT NULL AND safe_error <> '') OR (state <> 'failed' AND safe_error IS NULL)
    ),
    CONSTRAINT source_analysis_step_skip_shape CHECK (
        (state = 'skipped' AND skip_reason IS NOT NULL AND skip_reason <> '') OR (state <> 'skipped' AND skip_reason IS NULL)
    )
);
CREATE INDEX source_analysis_step_pending_idx ON source_analysis_step(work_id) WHERE state = 'pending';

CREATE TABLE operation_source_work_hold (
    operation_id uuid NOT NULL REFERENCES operation(id) ON DELETE RESTRICT,
    work_id uuid NOT NULL REFERENCES source_analysis_work(id) ON DELETE RESTRICT,
    PRIMARY KEY (operation_id, work_id)
);
ALTER TABLE source_analysis_step ADD CONSTRAINT source_analysis_step_work_hold_fkey
    FOREIGN KEY (execution_operation_id, work_id) REFERENCES operation_source_work_hold(operation_id, work_id)
    DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE operation_tool_read_hold (
    operation_id uuid NOT NULL REFERENCES operation(id) ON DELETE RESTRICT,
    installation_id uuid NOT NULL REFERENCES tool_installation(id) ON DELETE RESTRICT,
    PRIMARY KEY (operation_id, installation_id)
);
CREATE FUNCTION source_analysis_immutable_result() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'source analysis result rows are immutable';
END $$;
CREATE TRIGGER media_fingerprint_result_immutable BEFORE UPDATE ON media_fingerprint_result FOR EACH ROW EXECUTE FUNCTION source_analysis_immutable_result();
CREATE TRIGGER media_fingerprint_cache_immutable BEFORE UPDATE ON media_fingerprint_cache FOR EACH ROW EXECUTE FUNCTION source_analysis_immutable_result();
