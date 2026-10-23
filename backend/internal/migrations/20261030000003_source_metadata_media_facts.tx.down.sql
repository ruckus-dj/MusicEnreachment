DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.media_metadata_result
        WHERE cover_observed OR cover_data IS NOT NULL OR properties IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM public.track_source WHERE source_metadata_result_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'refusing to remove populated source metadata media facts or references';
    END IF;
END;
$$;

DROP INDEX public.track_source_source_metadata_result_idx;
ALTER TABLE public.track_source
    DROP CONSTRAINT track_source_source_metadata_result_fk,
    DROP COLUMN source_metadata_result_id;
ALTER TABLE public.media_metadata_result
    DROP CONSTRAINT media_metadata_result_cover_facts_check,
    DROP COLUMN cover_observed,
    DROP COLUMN cover_data,
    DROP COLUMN cover_mime,
    DROP COLUMN cover_sha256,
    DROP COLUMN cover_size_bytes,
    DROP COLUMN properties;
