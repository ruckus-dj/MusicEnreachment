DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.genre_catalog
        WHERE refresh_request ? 'configuration_identity'
          AND refresh_request->>'status' IN ('queued', 'running')
    ) THEN
        RAISE EXCEPTION 'cannot roll back while a pinned Setup genre catalog refresh is active';
    END IF;
END $$;

ALTER TABLE public.genre_catalog
    DROP COLUMN published_configuration_identity;
