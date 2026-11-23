-- A receipt-scoped audio upgrade must retain the exact compatible association it
-- was admitted from for the whole delivery. The pin is optional so ordinary
-- source holds keep their existing shape: both receipt columns are NULL and the
-- composite foreign key is skipped under MATCH SIMPLE. A receipt hold supplies
-- both columns, and the generated compatible witness makes the foreign key prove
-- the live association is still compatible without a nullable boolean default.
ALTER TABLE public.track_source
    ADD CONSTRAINT track_source_receipt_pin_key
        UNIQUE (id, track_id, media_variant_id, compatible);

ALTER TABLE public.publication_source_hold
    ADD COLUMN receipt_track_source_id uuid,
    ADD COLUMN receipt_track_id uuid,
    ADD COLUMN receipt_compatible boolean GENERATED ALWAYS AS (
        CASE WHEN receipt_track_source_id IS NULL THEN NULL ELSE true END
    ) STORED,
    ADD CONSTRAINT publication_source_hold_receipt_pair_check CHECK (
        (receipt_track_source_id IS NULL) = (receipt_track_id IS NULL)
    ),
    ADD CONSTRAINT publication_source_hold_receipt_pin_fkey
        FOREIGN KEY (receipt_track_source_id, receipt_track_id, media_variant_id, receipt_compatible)
        REFERENCES public.track_source (id, track_id, media_variant_id, compatible)
        ON UPDATE RESTRICT ON DELETE RESTRICT;

-- Reverse lookup from the pinned association to the live operations retaining it.
CREATE INDEX publication_source_hold_receipt_track_source_idx
    ON public.publication_source_hold (receipt_track_source_id, operation_id)
    WHERE receipt_track_source_id IS NOT NULL;
