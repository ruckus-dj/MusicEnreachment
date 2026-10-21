DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_completion) THEN
        RAISE EXCEPTION 'cannot roll back current source cleanup completion markers';
    END IF;
END $$;
DROP TABLE public.source_cleanup_completion;
