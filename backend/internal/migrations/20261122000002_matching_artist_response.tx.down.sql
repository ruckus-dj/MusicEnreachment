LOCK TABLE public.matching_provider_dependency IN ACCESS EXCLUSIVE MODE;

DO $rollback$
BEGIN
    IF EXISTS (SELECT 1 FROM public.matching_provider_dependency WHERE accepted_artist_response IS NOT NULL) THEN
        RAISE EXCEPTION 'accepted artist matching responses remain';
    END IF;
END;
$rollback$;

DROP TRIGGER matching_artist_response_guard ON public.matching_provider_dependency;
DROP FUNCTION public.guard_matching_artist_response();
ALTER TABLE public.matching_provider_dependency DROP COLUMN accepted_artist_response;
