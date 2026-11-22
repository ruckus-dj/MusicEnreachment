LOCK TABLE public.track_publication IN ACCESS EXCLUSIVE MODE;
LOCK TABLE public.track_publication_attempt IN ACCESS EXCLUSIVE MODE;

DO $rollback$
BEGIN
    IF EXISTS (SELECT 1 FROM public.track_publication WHERE matching_confidence IS NOT NULL)
       OR EXISTS (SELECT 1 FROM public.track_publication_attempt WHERE matching_confidence IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back publication matching confidence while retained scores exist';
    END IF;
END;
$rollback$;

ALTER TABLE public.track_publication DROP COLUMN matching_confidence;
ALTER TABLE public.track_publication_attempt DROP COLUMN matching_confidence;
