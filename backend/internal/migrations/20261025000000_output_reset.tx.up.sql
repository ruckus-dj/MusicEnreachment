CREATE TABLE output_reset_journal (
    token uuid PRIMARY KEY,
    state text NOT NULL CHECK (state IN ('preparing', 'committed', 'finished')),
    old_root text NOT NULL,
    new_root text NOT NULL,
    created_directories text[] NOT NULL DEFAULT '{}',
    directory_manifest jsonb NOT NULL DEFAULT '[]'::jsonb,
    cleanup_outcomes jsonb NOT NULL DEFAULT '{"invalidated_artifact_ids":[],"removed_candidate_operation_ids":[]}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (old_root <> new_root)
);

CREATE UNIQUE INDEX output_reset_one_unresolved_idx
    ON output_reset_journal ((true)) WHERE state <> 'finished';
