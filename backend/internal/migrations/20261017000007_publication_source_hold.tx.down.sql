DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_source_hold) THEN
        RAISE EXCEPTION 'cannot roll back publication source holds while holds exist';
    END IF;
END;
$$;

DROP TABLE public.publication_source_hold;
ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_operation_epoch_unique;
