LOCK TABLE public.publication_source_hold IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_source_hold WHERE receipt_track_source_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back receipt source pin while live publication holds retain it';
    END IF;
END
$$;

ALTER TABLE public.publication_source_hold
    DROP CONSTRAINT publication_source_hold_receipt_pin_fkey,
    DROP CONSTRAINT publication_source_hold_receipt_pair_check;

DROP INDEX public.publication_source_hold_receipt_track_source_idx;

ALTER TABLE public.publication_source_hold
    DROP COLUMN receipt_compatible,
    DROP COLUMN receipt_track_id,
    DROP COLUMN receipt_track_source_id;

ALTER TABLE public.track_source
    DROP CONSTRAINT track_source_receipt_pin_key;
