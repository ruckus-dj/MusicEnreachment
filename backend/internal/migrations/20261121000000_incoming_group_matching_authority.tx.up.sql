-- Matching authority is intentionally independent of incoming_group rows:
-- grouping recomputation replaces that materialized read model.
CREATE TABLE incoming_group_matching_authority (
    group_id uuid PRIMARY KEY,
    mode text NOT NULL CHECK (mode IN ('automatic', 'manual')),
    revision bigint NOT NULL CHECK (revision >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE matching_run_target
    ADD COLUMN authority_revision bigint NOT NULL DEFAULT 0 CHECK (authority_revision >= 0);

ALTER TABLE incoming_match_evaluation
    ADD COLUMN matching_authority_mode text NOT NULL DEFAULT 'automatic'
        CHECK (matching_authority_mode IN ('automatic', 'manual_review')),
    ADD COLUMN matching_authority_revision bigint NOT NULL DEFAULT 0
        CHECK (matching_authority_revision >= 0);

CREATE TABLE incoming_group_unlink_receipt (
    request_id uuid PRIMARY KEY,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL CHECK (length(intent_hash) = 64),
    response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now()
);
