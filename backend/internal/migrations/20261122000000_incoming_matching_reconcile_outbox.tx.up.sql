CREATE TABLE incoming_matching_reconcile_event (
    id uuid PRIMARY KEY,
    source_operation_id uuid NOT NULL,
    source_attempt integer NOT NULL CHECK (source_attempt > 0),
    reason text NOT NULL CHECK (reason IN ('analysis_success', 'scan_success')),
    source_input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(source_input_snapshot) = 'object'),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'dispatched', 'done')),
    registered_job_id bigint,
    delivery_epoch bigint NOT NULL DEFAULT 0 CHECK (delivery_epoch >= 0),
    highest_delivery_attempt integer NOT NULL DEFAULT 0 CHECK (highest_delivery_attempt >= 0),
    delivery_claimed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    dispatched_at timestamptz,
    completed_at timestamptz,
    UNIQUE (source_operation_id, source_attempt, reason),
    CHECK ((state = 'pending' AND registered_job_id IS NULL AND dispatched_at IS NULL) OR
           (state IN ('dispatched', 'done') AND registered_job_id IS NOT NULL AND dispatched_at IS NOT NULL)),
    CHECK ((state = 'done') = (completed_at IS NOT NULL))
);

CREATE INDEX incoming_matching_reconcile_pending_idx
    ON incoming_matching_reconcile_event (created_at, id)
    WHERE state = 'pending';
