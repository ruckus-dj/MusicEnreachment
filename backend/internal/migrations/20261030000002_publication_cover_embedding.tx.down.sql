DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.track_publication WHERE cover_embedding IS NOT NULL) THEN
        RAISE EXCEPTION 'refusing to remove populated current publication cover embedding summaries';
    END IF;
END;
$$;

ALTER TABLE public.track_publication
    DROP CONSTRAINT track_publication_cover_embedding_projection_check,
    DROP COLUMN cover_embedding;
