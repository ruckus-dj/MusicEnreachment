ALTER TABLE incoming_matching_generation
    ADD COLUMN requested_generation bigint NOT NULL DEFAULT 0 CHECK (requested_generation >= 0),
    ADD COLUMN reconciled_generation bigint NOT NULL DEFAULT 0 CHECK (reconciled_generation >= 0),
    ADD CONSTRAINT incoming_matching_generation_reconciled_lte_requested
        CHECK (reconciled_generation <= requested_generation);

ALTER TABLE incoming_matching_reconcile_event
    ALTER COLUMN source_operation_id DROP NOT NULL,
    ALTER COLUMN source_attempt DROP NOT NULL,
    DROP CONSTRAINT incoming_matching_reconcile_event_reason_check,
    ADD CONSTRAINT incoming_matching_reconcile_event_reason_check
        CHECK (reason IN ('analysis_success', 'scan_success', 'startup', 'current')),
    DROP CONSTRAINT incoming_matching_reconcile_event_origin_check,
    ADD CONSTRAINT incoming_matching_reconcile_event_origin_check CHECK (
        (reason IN ('analysis_success', 'scan_success')
            AND source_operation_id IS NOT NULL AND source_attempt IS NOT NULL AND source_attempt > 0)
        OR (reason IN ('startup', 'current')
            AND source_operation_id IS NULL AND source_attempt IS NULL AND source_input_snapshot = '{}'::jsonb)
    );

CREATE UNIQUE INDEX incoming_matching_reconcile_current_pending_idx
    ON incoming_matching_reconcile_event (reason)
    WHERE reason = 'current' AND state = 'pending';
