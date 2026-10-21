DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry_resolution) THEN
        RAISE EXCEPTION 'cannot roll back explicit source cleanup retry resolution while resolution authority exists';
    END IF;
END $$;

DROP TRIGGER source_cleanup_retry_resolution_immutable ON public.source_cleanup_explicit_retry_resolution;
DROP FUNCTION public.protect_source_cleanup_retry_resolution();
DROP TABLE public.source_cleanup_explicit_retry_resolution;
ALTER TABLE public.source_cleanup_explicit_retry
    DROP CONSTRAINT source_cleanup_explicit_retry_consumed_resolution_key;
