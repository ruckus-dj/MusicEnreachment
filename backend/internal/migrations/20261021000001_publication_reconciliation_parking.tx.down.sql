DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_reconciliation_request
        WHERE evaluated_generation IS NOT NULL OR parked_reason IS NOT NULL OR retry_not_before IS NOT NULL) THEN
        RAISE EXCEPTION 'publication reconciliation parking data exists; refusing rollback';
    END IF;
END $$;
ALTER TABLE public.publication_reconciliation_request
    DROP CONSTRAINT publication_reconciliation_parking_shape,
    DROP COLUMN retry_not_before,
    DROP COLUMN parked_reason,
    DROP COLUMN evaluated_generation;
