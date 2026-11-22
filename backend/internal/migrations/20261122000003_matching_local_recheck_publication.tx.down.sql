LOCK TABLE public.matching_local_recheck_publication IN ACCESS EXCLUSIVE MODE;

DO $guard$
BEGIN
    IF EXISTS (SELECT 1 FROM public.matching_local_recheck_publication) THEN
        RAISE EXCEPTION 'cannot roll back Local recheck publication membership while retained owners remain';
    END IF;
END;
$guard$;

DROP TRIGGER matching_local_recheck_publication_immutable ON public.matching_local_recheck_publication;
DROP FUNCTION public.guard_matching_local_recheck_publication();
DROP TABLE public.matching_local_recheck_publication;
