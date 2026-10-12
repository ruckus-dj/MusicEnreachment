DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM incoming_matching_reconcile_event WHERE reason = 'startup') THEN
        RAISE EXCEPTION 'cannot roll back startup matching reconciliation while startup reconciliation events exist';
    END IF;
END
$$;

DROP INDEX incoming_matching_reconcile_startup_open_unique;
DROP INDEX incoming_matching_reconcile_source_unique;

ALTER TABLE incoming_matching_reconcile_event
    DROP CONSTRAINT incoming_matching_reconcile_event_origin_check,
    DROP CONSTRAINT incoming_matching_reconcile_event_reason_check,
    ALTER COLUMN source_operation_id SET NOT NULL,
    ALTER COLUMN source_attempt SET NOT NULL,
    ADD CONSTRAINT incoming_matching_reconcile_event_reason_check CHECK (reason IN ('analysis_success', 'scan_success')),
    ADD CONSTRAINT incoming_matching_reconcile_source_identity_unique UNIQUE (source_operation_id, source_attempt, reason);
