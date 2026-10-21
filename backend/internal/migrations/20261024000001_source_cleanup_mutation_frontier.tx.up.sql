ALTER TABLE public.publication_execution_claim
    ADD CONSTRAINT publication_execution_claim_operation_claim_key UNIQUE (operation_id, claim_uuid);

ALTER TABLE public.source_cleanup_item
    ADD COLUMN mutation_claim_uuid uuid,
    ADD COLUMN mutation_started_at timestamptz,
    ADD COLUMN mutation_completed_at timestamptz,
    ADD CONSTRAINT source_cleanup_item_mutation_claim_fkey
        FOREIGN KEY (operation_id, mutation_claim_uuid)
        REFERENCES public.publication_execution_claim(operation_id, claim_uuid) ON DELETE RESTRICT,
    ADD CONSTRAINT source_cleanup_item_mutation_frontier_check CHECK (
        (mutation_claim_uuid IS NULL AND mutation_started_at IS NULL)
        OR (mutation_claim_uuid IS NOT NULL AND mutation_started_at IS NOT NULL)
    ),
    ADD CONSTRAINT source_cleanup_item_mutation_completion_check CHECK (
        mutation_completed_at IS NULL
        OR (mutation_started_at IS NOT NULL AND mutation_completed_at >= mutation_started_at)
    );
