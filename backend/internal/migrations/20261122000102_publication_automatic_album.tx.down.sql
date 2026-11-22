LOCK TABLE public.publication_automatic_album IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_automatic_album) THEN
        RAISE EXCEPTION 'cannot roll back while automatic album publication requests are retained';
    END IF;
END;
$$;
DROP TABLE public.publication_automatic_album;
