CREATE TABLE incoming_group_return_automatic_receipt (
    request_id uuid PRIMARY KEY,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL CHECK (intent_hash ~ '^[0-9a-f]{64}$'),
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX incoming_group_return_automatic_receipt_group_idx
    ON incoming_group_return_automatic_receipt (group_id, created_at);
