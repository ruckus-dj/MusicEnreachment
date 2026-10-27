LOCK TABLE public.track_publication_attempt IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.track_publication_attempt
        WHERE mutation_journal IS DISTINCT FROM '[]'::jsonb
           OR phase NOT IN ('queued', 'preparing', 'staged', 'validated', 'exposing')
    ) THEN
        RAISE EXCEPTION 'cannot retire publication mutation journal while attempts retain journal or retired phases';
    END IF;
END;
$$;

ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_mutation_journal_array_check,
    DROP CONSTRAINT track_publication_attempt_frozen_inputs_phase_check,
    DROP CONSTRAINT track_publication_attempt_phase_check,
    DROP COLUMN mutation_journal,
    ADD CONSTRAINT track_publication_attempt_phase_check
        CHECK (phase = ANY (ARRAY['queued', 'preparing', 'staged', 'validated', 'exposing'])),
    ADD CONSTRAINT track_publication_attempt_frozen_inputs_phase_check
        CHECK (frozen_inputs IS NOT NULL AND jsonb_typeof(frozen_inputs) = 'object');
