ALTER TABLE public.track_publication_attempt
    ADD COLUMN resume_mode text NOT NULL DEFAULT 'prepare',
    ADD CONSTRAINT track_publication_attempt_resume_mode_check
        CHECK (resume_mode = ANY (ARRAY['prepare', 'inspect_source_republish_target']));
