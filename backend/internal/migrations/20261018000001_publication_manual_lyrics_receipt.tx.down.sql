DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_manual_lyrics_receipt) THEN
        RAISE EXCEPTION 'refusing to drop populated publication_manual_lyrics_receipt';
    END IF;
END;
$$;

DROP TABLE public.publication_manual_lyrics_receipt;
