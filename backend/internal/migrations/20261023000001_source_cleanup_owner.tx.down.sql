DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_owner) THEN
        RAISE EXCEPTION 'cannot roll back source cleanup owner schema while owner rows exist';
    END IF;
END $$;

DROP TABLE public.source_cleanup_owner;
ALTER TABLE public.source_cleanup_item
    DROP CONSTRAINT source_cleanup_item_id_operation_key;
