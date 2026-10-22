DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.release_publication_cover) THEN
        RAISE EXCEPTION 'refusing to discard current release cover authority or an active acquisition';
    END IF;
END
$$;

DROP TABLE public.release_publication_cover;
