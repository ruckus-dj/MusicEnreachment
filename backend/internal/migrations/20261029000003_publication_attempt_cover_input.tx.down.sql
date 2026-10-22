DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_attempt_cover_input) THEN
        RAISE EXCEPTION 'refusing to drop populated publication_attempt_cover_input';
    END IF;
END
$$;

DROP TRIGGER publication_attempt_cover_input_immutable ON public.publication_attempt_cover_input;
DROP FUNCTION public.reject_publication_attempt_cover_input_mutation();
DROP TABLE public.publication_attempt_cover_input;
ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_operation_position_key;
ALTER TABLE public.release_track
    DROP CONSTRAINT release_track_id_release_key;
