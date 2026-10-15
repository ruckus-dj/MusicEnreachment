CREATE TABLE local_matching_receipt (
    request_id uuid PRIMARY KEY,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL CHECK (intent_hash ~ '^[0-9a-f]{64}$'),
    preview_digest text NOT NULL CHECK (preview_digest ~ '^[0-9a-f]{64}$'),
    response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX local_matching_receipt_group_idx ON local_matching_receipt(group_id);
