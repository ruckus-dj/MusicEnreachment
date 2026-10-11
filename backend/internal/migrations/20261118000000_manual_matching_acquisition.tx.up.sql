-- A request ID binds one explicit release choice to a durable manual-review
-- run. The original member snapshot is captured before provider hydration.
CREATE TABLE manual_matching_admission (
    request_id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES matching_run(id) ON DELETE CASCADE,
    group_id uuid NOT NULL,
    group_revision text NOT NULL CHECK (length(btrim(group_revision)) > 0),
    evaluation_revision text NOT NULL CHECK (length(btrim(evaluation_revision)) > 0),
    selected_release_mbid uuid NOT NULL,
    lookup_operation_id uuid NOT NULL UNIQUE REFERENCES operation(id) ON DELETE RESTRICT,
    cache_key text NOT NULL CHECK (length(btrim(cache_key)) > 0),
    canonical_input_digest bytea NOT NULL CHECK (octet_length(canonical_input_digest) = 32),
    plan_digest bytea NOT NULL CHECK (octet_length(plan_digest) = 32),
    original_members jsonb NOT NULL CHECK (jsonb_typeof(original_members) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, request_id)
);

CREATE INDEX manual_matching_admission_group_created_idx
    ON manual_matching_admission(group_id, created_at DESC);
