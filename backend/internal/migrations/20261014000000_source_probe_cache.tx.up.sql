-- Probe cache ownership is independent of the canonical digest row's selected
-- probe provenance. A result may be shared by digest, ffprobe version and policy.
CREATE TABLE media_probe_cache (
    source_sha256 bytea NOT NULL REFERENCES media_variant(source_sha256) ON DELETE RESTRICT,
    ffprobe_version text NOT NULL CHECK (ffprobe_version <> ''),
    ffprobe_version_sha256 bytea NOT NULL,
    analysis_policy_version integer NOT NULL CHECK (analysis_policy_version >= 1),
    result_id uuid NOT NULL REFERENCES media_variant(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_sha256, ffprobe_version_sha256, analysis_policy_version)
);

CREATE FUNCTION validate_media_probe_cache_association() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    canonical_size bigint;
    result_size bigint;
    result_version text;
    result_policy integer;
BEGIN
    NEW.ffprobe_version_sha256 := sha256(convert_to(NEW.ffprobe_version, 'UTF8'));
    SELECT size_bytes INTO canonical_size FROM media_variant WHERE source_sha256 = NEW.source_sha256;
    SELECT size_bytes, ffprobe_version, analysis_policy_version
      INTO result_size, result_version, result_policy
      FROM media_variant WHERE id = NEW.result_id;
    IF canonical_size IS NULL OR result_size IS NULL OR canonical_size <> result_size
       OR result_version IS DISTINCT FROM NEW.ffprobe_version
       OR result_policy IS DISTINCT FROM NEW.analysis_policy_version THEN
        RAISE EXCEPTION 'probe cache association does not match digest size, version and policy';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER media_probe_cache_association_valid
    BEFORE INSERT OR UPDATE ON media_probe_cache
    FOR EACH ROW EXECUTE FUNCTION validate_media_probe_cache_association();

-- Preserve pre-existing canonical probe results as cache winners, without
-- inventing SHA identities or changing selected results.
INSERT INTO media_probe_cache(source_sha256, ffprobe_version, analysis_policy_version, result_id)
SELECT source_sha256, ffprobe_version, analysis_policy_version, id
  FROM media_variant
 WHERE source_sha256 IS NOT NULL AND ffprobe_version IS NOT NULL
ON CONFLICT (source_sha256, ffprobe_version_sha256, analysis_policy_version) DO NOTHING;
