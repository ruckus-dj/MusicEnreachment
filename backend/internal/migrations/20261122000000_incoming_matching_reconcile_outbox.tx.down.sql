DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM incoming_matching_reconcile_event) THEN
        RAISE EXCEPTION 'cannot roll back matching reconciliation outbox while durable events exist; pending work and deduplication receipts must be retained';
    END IF;
END
$$;

DROP TABLE incoming_matching_reconcile_event;
