DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.source_cleanup_item
        WHERE mutation_claim_uuid IS NOT NULL
           OR mutation_started_at IS NOT NULL
           OR mutation_completed_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back source cleanup mutation frontier while frontier fields are populated';
    END IF;
END $$;

ALTER TABLE public.source_cleanup_item
    DROP CONSTRAINT source_cleanup_item_mutation_completion_check,
    DROP CONSTRAINT source_cleanup_item_mutation_frontier_check,
    DROP CONSTRAINT source_cleanup_item_mutation_claim_fkey,
    DROP COLUMN mutation_completed_at,
    DROP COLUMN mutation_started_at,
    DROP COLUMN mutation_claim_uuid;

ALTER TABLE public.publication_execution_claim
    DROP CONSTRAINT publication_execution_claim_operation_claim_key;
