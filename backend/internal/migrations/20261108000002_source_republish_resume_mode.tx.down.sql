DO $guard$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.track_publication_attempt
        WHERE resume_mode = 'inspect_source_republish_target'
    ) THEN
        RAISE EXCEPTION 'cannot roll back source republish resume mode while inspection recovery authority is retained';
    END IF;
END;
$guard$;

ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_resume_mode_check,
    DROP COLUMN resume_mode;
