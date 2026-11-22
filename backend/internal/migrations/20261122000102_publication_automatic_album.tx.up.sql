CREATE TABLE public.publication_automatic_album (
    apply_operation_id uuid PRIMARY KEY REFERENCES public.matching_apply_receipt(apply_operation_id) ON DELETE RESTRICT,
    output_epoch uuid NOT NULL,
    release_id uuid NOT NULL,
    scope jsonb NOT NULL CHECK ((jsonb_typeof(scope) = 'array' AND jsonb_array_length(scope) > 0) IS TRUE)
);
