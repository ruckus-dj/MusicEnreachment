-- Materialize the complete digest/result/winner and loser mapping before removing
-- the legacy cache, its foreign keys, or any result rows.
CREATE TEMP TABLE fingerprint_result_collapse ON COMMIT DROP AS
WITH candidates AS (
    SELECT cache.source_sha256, cache.result_id
    FROM media_fingerprint_cache cache
    UNION
    SELECT variant.source_sha256, step.success_fingerprint_result_id
    FROM source_analysis_step step
    JOIN source_analysis_work work ON work.id = step.work_id
    JOIN source_analysis_step sha ON sha.work_id = work.id AND sha.step = 'sha256'
    JOIN media_variant variant ON variant.id = sha.success_sha_variant_id
    WHERE step.step = 'fingerprint'
      AND step.success_fingerprint_result_id IS NOT NULL
      AND variant.source_sha256 IS NOT NULL
), ranked AS (
    SELECT candidates.source_sha256, candidates.result_id,
           first_value(candidates.result_id) OVER (
               PARTITION BY candidates.source_sha256
               ORDER BY result.calculated_at DESC, result.id DESC
           ) AS winner_id
    FROM candidates
    JOIN media_fingerprint_result result ON result.id = candidates.result_id
)
SELECT DISTINCT source_sha256, result_id, winner_id FROM ranked;

-- The legacy schema allowed a result to be attached to unrelated digests. Such
-- ambiguous provenance cannot be canonicalized safely by choosing a UUID.
DO $$
BEGIN
    IF EXISTS (
        SELECT result_id FROM fingerprint_result_collapse
        GROUP BY result_id HAVING count(DISTINCT source_sha256) > 1
    ) THEN
        RAISE EXCEPTION 'cannot migrate fingerprint results associated with multiple source digests';
    END IF;
END $$;

-- A result row has one stable UUID. This global map is intentionally used for
-- every step reference, including references whose work has no digest step.
CREATE TEMP TABLE fingerprint_result_id_map ON COMMIT DROP AS
SELECT result_id, max(winner_id::text)::uuid AS winner_id
FROM fingerprint_result_collapse
GROUP BY result_id;

-- Remove cache foreign keys and immutable guards before deleting or updating
-- result rows, and before removing the legacy result uniqueness constraint.
DROP TRIGGER media_fingerprint_cache_immutable ON media_fingerprint_cache;
DROP TABLE media_fingerprint_cache;
DROP TRIGGER media_fingerprint_result_immutable ON media_fingerprint_result;

ALTER TABLE media_fingerprint_result
    DROP CONSTRAINT media_fingerprint_result_id_fpcalc_version_key,
    ADD COLUMN source_sha256 bytea,
    ADD COLUMN winning_result_id uuid;

UPDATE media_fingerprint_result result
SET source_sha256 = collapse.source_sha256,
    winning_result_id = collapse.winner_id
FROM fingerprint_result_collapse collapse
WHERE result.id = collapse.winner_id;

UPDATE media_fingerprint_result result
SET winning_result_id = result.id
WHERE result.winning_result_id IS NULL;

UPDATE source_analysis_step step
SET success_fingerprint_result_id = mapping.winner_id
FROM fingerprint_result_id_map mapping
WHERE step.success_fingerprint_result_id = mapping.result_id;

DELETE FROM media_fingerprint_result discarded
USING fingerprint_result_id_map mapping
WHERE discarded.id = mapping.result_id
  AND mapping.result_id <> mapping.winner_id;

ALTER TABLE media_fingerprint_result
    ALTER COLUMN winning_result_id SET NOT NULL,
    ADD CONSTRAINT media_fingerprint_result_source_sha256_length
        CHECK (source_sha256 IS NULL OR octet_length(source_sha256) = 32),
    ADD CONSTRAINT media_fingerprint_result_source_sha256_fkey
        FOREIGN KEY (source_sha256) REFERENCES media_variant(source_sha256) ON DELETE RESTRICT,
    ADD CONSTRAINT media_fingerprint_result_source_sha256_unique UNIQUE (source_sha256);

-- Canonical row ID and digest remain stable; winner provenance tracks the
-- incoming candidate used to arbitrate equal calculation timestamps.
CREATE FUNCTION source_fingerprint_result_identity_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR (OLD.source_sha256 IS NOT NULL AND NEW.source_sha256 IS DISTINCT FROM OLD.source_sha256) THEN
        RAISE EXCEPTION 'source fingerprint result identity is immutable';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER media_fingerprint_result_identity_immutable
    BEFORE UPDATE ON media_fingerprint_result
    FOR EACH ROW EXECUTE FUNCTION source_fingerprint_result_identity_immutable();
