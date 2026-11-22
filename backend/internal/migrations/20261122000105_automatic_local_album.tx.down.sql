LOCK TABLE public.publication_automatic_album IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_automatic_album WHERE local_layout IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back automatic Local album publication while requests remain';
    END IF;
END
$$;

ALTER TABLE public.publication_automatic_album
    DROP COLUMN receipt_operation_id,
    DROP COLUMN local_operation_id;

ALTER TABLE public.publication_automatic_album
    DROP COLUMN local_layout,
    ADD CONSTRAINT publication_automatic_album_apply_operation_id_fkey
        FOREIGN KEY (apply_operation_id) REFERENCES public.matching_apply_receipt(apply_operation_id) ON DELETE RESTRICT;
