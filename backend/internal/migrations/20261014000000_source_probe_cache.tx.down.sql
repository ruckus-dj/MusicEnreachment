-- Cache associations are redundant only when the canonical row itself is the
-- winner. Refuse rollback rather than discard independently retained results.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM media_probe_cache cache
        JOIN media_variant canonical ON canonical.source_sha256 = cache.source_sha256
        WHERE cache.result_id <> canonical.id
    ) THEN
        RAISE EXCEPTION 'cannot roll back source probe cache while non-canonical probe results are cached';
    END IF;
END;
$$;

DROP TRIGGER media_probe_cache_association_valid ON media_probe_cache;
DROP FUNCTION validate_media_probe_cache_association();
DROP TABLE media_probe_cache;
