DO $guard$
BEGIN
    IF EXISTS (SELECT 1 FROM public.matching_local_recheck) THEN
        RAISE EXCEPTION 'cannot roll back Local recheck source veto while durable recheck owners remain';
    END IF;
END;
$guard$;

DROP TRIGGER matching_local_recheck_incumbent_guard ON public.matching_local_recheck;
DROP FUNCTION public.guard_matching_local_recheck_incumbent();
DROP TABLE public.matching_local_recheck_source;
DROP TABLE public.matching_local_recheck;
