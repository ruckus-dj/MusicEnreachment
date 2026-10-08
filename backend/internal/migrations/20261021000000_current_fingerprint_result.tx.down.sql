DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM media_fingerprint_result result
        WHERE result.source_sha256 IS NOT NULL
          AND NOT EXISTS (
              SELECT 1 FROM media_variant variant
              WHERE variant.source_sha256 = result.source_sha256
          )
    ) THEN
        RAISE EXCEPTION 'cannot roll back current fingerprint results with a missing SHA identity';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM source_analysis_step fingerprint
        JOIN source_analysis_work work ON work.id = fingerprint.work_id
        JOIN source_analysis_step sha ON sha.work_id = work.id AND sha.step = 'sha256'
        JOIN media_variant variant ON variant.id = sha.success_sha_variant_id
        JOIN media_fingerprint_result result ON result.id = fingerprint.success_fingerprint_result_id
        WHERE fingerprint.step = 'fingerprint'
          AND fingerprint.success_fingerprint_result_id IS NOT NULL
          AND variant.source_sha256 IS NOT NULL
          AND result.source_sha256 IS DISTINCT FROM variant.source_sha256
    ) THEN
        RAISE EXCEPTION 'cannot roll back fingerprint selections detached from their SHA identity';
    END IF;
END $$;

DROP TRIGGER media_fingerprint_result_identity_immutable ON media_fingerprint_result;
DROP FUNCTION source_fingerprint_result_identity_immutable();

CREATE TEMP TABLE fingerprint_cache_restore ON COMMIT DROP AS
SELECT result.source_sha256, result.fpcalc_version, result.id AS result_id
FROM media_fingerprint_result result
WHERE result.source_sha256 IS NOT NULL;

ALTER TABLE media_fingerprint_result
    DROP CONSTRAINT media_fingerprint_result_source_sha256_unique,
    DROP CONSTRAINT media_fingerprint_result_source_sha256_fkey,
    DROP CONSTRAINT media_fingerprint_result_source_sha256_length,
    DROP COLUMN winning_result_id,
    DROP COLUMN source_sha256,
    ADD CONSTRAINT media_fingerprint_result_id_fpcalc_version_key UNIQUE (id, fpcalc_version);

CREATE TABLE media_fingerprint_cache (
    source_sha256 bytea NOT NULL REFERENCES media_variant(source_sha256) ON DELETE RESTRICT,
    fpcalc_version text NOT NULL,
    result_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_sha256, fpcalc_version),
    FOREIGN KEY (result_id, fpcalc_version)
        REFERENCES media_fingerprint_result(id, fpcalc_version) ON DELETE RESTRICT
);

INSERT INTO media_fingerprint_cache(source_sha256, fpcalc_version, result_id)
SELECT restore.source_sha256, restore.fpcalc_version, restore.result_id
FROM fingerprint_cache_restore restore;

CREATE TRIGGER media_fingerprint_result_immutable BEFORE UPDATE ON media_fingerprint_result
    FOR EACH ROW EXECUTE FUNCTION source_analysis_immutable_result();
CREATE TRIGGER media_fingerprint_cache_immutable BEFORE UPDATE ON media_fingerprint_cache
    FOR EACH ROW EXECUTE FUNCTION source_analysis_immutable_result();
