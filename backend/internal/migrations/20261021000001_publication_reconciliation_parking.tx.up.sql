ALTER TABLE public.publication_reconciliation_request
    ADD COLUMN evaluated_generation bigint,
    ADD COLUMN parked_reason text,
    ADD COLUMN retry_not_before timestamptz,
    ADD CONSTRAINT publication_reconciliation_parking_shape CHECK (
        (evaluated_generation IS NULL AND parked_reason IS NULL AND retry_not_before IS NULL)
        OR (evaluated_generation IS NOT NULL AND evaluated_generation >= 0
            AND (parked_reason IS NULL OR parked_reason <> ''))
    );
