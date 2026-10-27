LOCK TABLE public.track_publication_attempt IN ACCESS EXCLUSIVE MODE;

ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_frozen_inputs_phase_check,
    DROP CONSTRAINT track_publication_attempt_phase_check,
    ADD COLUMN mutation_journal jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE public.track_publication_attempt
    ALTER COLUMN mutation_journal DROP DEFAULT,
    ADD CONSTRAINT track_publication_attempt_phase_check CHECK (phase = ANY (ARRAY['queued', 'preparing', 'staged', 'validated', 'exposing', 'committed', 'cleaning', 'cleanup_pending', 'recovery_required'])),
    ADD CONSTRAINT track_publication_attempt_frozen_inputs_phase_check CHECK (
        (phase IN ('queued', 'preparing', 'staged', 'validated', 'exposing', 'recovery_required')
            AND frozen_inputs IS NOT NULL AND jsonb_typeof(frozen_inputs) = 'object')
        OR (phase IN ('committed', 'cleaning', 'cleanup_pending') AND frozen_inputs IS NULL)
    ),
    ADD CONSTRAINT track_publication_attempt_mutation_journal_array_check CHECK (jsonb_typeof(mutation_journal) = 'array');
