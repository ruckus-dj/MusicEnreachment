DO $$
DECLARE
    unique_name text;
BEGIN
    SELECT conname INTO unique_name
    FROM pg_constraint
    WHERE conrelid = 'incoming_matching_reconcile_event'::regclass
      AND contype = 'u'
      AND pg_get_constraintdef(oid) = 'UNIQUE (source_operation_id, source_attempt, reason)';
    IF unique_name IS NULL THEN
        RAISE EXCEPTION 'source matching reconciliation uniqueness constraint is missing';
    END IF;
    EXECUTE format('ALTER TABLE incoming_matching_reconcile_event DROP CONSTRAINT %I', unique_name);
END
$$;

ALTER TABLE incoming_matching_reconcile_event
    ALTER COLUMN source_operation_id DROP NOT NULL,
    ALTER COLUMN source_attempt DROP NOT NULL,
    DROP CONSTRAINT incoming_matching_reconcile_event_reason_check;

ALTER TABLE incoming_matching_reconcile_event
    ADD CONSTRAINT incoming_matching_reconcile_event_origin_check CHECK (
        (reason IN ('analysis_success', 'scan_success') AND source_operation_id IS NOT NULL AND source_attempt IS NOT NULL AND source_attempt > 0)
        OR (reason = 'startup' AND source_operation_id IS NULL AND source_attempt IS NULL AND source_input_snapshot = '{}'::jsonb)
    ),
    ADD CONSTRAINT incoming_matching_reconcile_event_reason_check CHECK (reason IN ('analysis_success', 'scan_success', 'startup'));

CREATE UNIQUE INDEX incoming_matching_reconcile_source_unique
    ON incoming_matching_reconcile_event (source_operation_id, source_attempt, reason)
    WHERE reason IN ('analysis_success', 'scan_success');

CREATE UNIQUE INDEX incoming_matching_reconcile_startup_open_unique
    ON incoming_matching_reconcile_event (reason)
    WHERE reason = 'startup' AND state IN ('pending', 'dispatched');
