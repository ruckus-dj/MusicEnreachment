DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM incoming_matching_reconcile_event WHERE reason='current')
       OR EXISTS (SELECT 1 FROM incoming_matching_generation
                   WHERE requested_generation <> 0 OR reconciled_generation <> 0) THEN
        RAISE EXCEPTION 'cannot roll back current incoming matching requests while obligations or current events remain';
    END IF;
END
$$;

DROP INDEX incoming_matching_reconcile_current_pending_idx;

ALTER TABLE incoming_matching_reconcile_event
    DROP CONSTRAINT incoming_matching_reconcile_event_reason_check,
    DROP CONSTRAINT incoming_matching_reconcile_event_origin_check,
    ADD CONSTRAINT incoming_matching_reconcile_event_reason_check
        CHECK (reason IN ('analysis_success', 'scan_success', 'startup')),
    ADD CONSTRAINT incoming_matching_reconcile_event_origin_check CHECK (
        (reason IN ('analysis_success', 'scan_success')
            AND source_operation_id IS NOT NULL AND source_attempt IS NOT NULL AND source_attempt > 0)
        OR (reason = 'startup'
            AND source_operation_id IS NULL AND source_attempt IS NULL AND source_input_snapshot = '{}'::jsonb)
    );

ALTER TABLE incoming_matching_generation
    DROP CONSTRAINT incoming_matching_generation_reconciled_lte_requested,
    DROP COLUMN reconciled_generation,
    DROP COLUMN requested_generation;
