DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.track_publication)
       OR EXISTS (SELECT 1 FROM public.track_publication_attempt)
       OR EXISTS (SELECT 1 FROM public.publication_execution_claim)
       OR EXISTS (SELECT 1 FROM public.publication_namespace_claim)
       OR EXISTS (SELECT 1 FROM public.publication_namespace_reservation)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_item) THEN
        RAISE EXCEPTION 'cannot roll back file publication state while it owns live state';
    END IF;
END;
$$;

DROP TABLE public.source_cleanup_item;
DROP TABLE public.publication_namespace_reservation;
DROP TABLE public.publication_namespace_claim;
DROP TABLE public.publication_execution_claim;
DROP TABLE public.track_publication_attempt;
DROP TABLE public.track_publication;
DROP TABLE public.publication_namespace_state;
