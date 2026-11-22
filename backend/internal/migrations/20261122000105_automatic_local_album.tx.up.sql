ALTER TABLE public.publication_automatic_album
    DROP CONSTRAINT publication_automatic_album_apply_operation_id_fkey,
    ADD COLUMN local_layout jsonb,
    ADD CONSTRAINT publication_automatic_album_local_layout_check CHECK (
        local_layout IS NULL OR (
            jsonb_typeof(local_layout) = 'object'
            AND jsonb_typeof(local_layout->'layout_target') = 'object'
            AND (local_layout->'layout_target') ?& ARRAY[
                'schema_version', 'target', 'captures', 'grouping_revision', 'pair_threshold', 'policy_version'
            ]
            AND jsonb_typeof(local_layout->'catalog_digest') = 'string'
            AND local_layout->>'catalog_digest' ~ '^[0-9a-f]{64}$'
            AND CASE WHEN jsonb_typeof(local_layout->'sources') = 'array'
                THEN jsonb_array_length(local_layout->'sources') > 0
                     AND NOT jsonb_path_exists(local_layout, '$.sources[*] ? (@.type() != "object")')
                ELSE false END
        ) IS TRUE
    ),
    ADD COLUMN receipt_operation_id uuid GENERATED ALWAYS AS (
        CASE WHEN local_layout IS NULL THEN apply_operation_id END
    ) STORED REFERENCES public.matching_apply_receipt(apply_operation_id) ON DELETE RESTRICT,
    ADD COLUMN local_operation_id uuid GENERATED ALWAYS AS (
        CASE WHEN local_layout IS NOT NULL THEN apply_operation_id END
    ) STORED REFERENCES public.operation(id) ON DELETE RESTRICT;

CREATE INDEX publication_automatic_album_receipt_operation_idx
    ON public.publication_automatic_album (receipt_operation_id) WHERE receipt_operation_id IS NOT NULL;
CREATE INDEX publication_automatic_album_local_operation_idx
    ON public.publication_automatic_album (local_operation_id) WHERE local_operation_id IS NOT NULL;
