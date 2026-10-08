ALTER TABLE source_root
    ADD COLUMN processing_mode text NOT NULL DEFAULT 'in_place'
        CHECK (processing_mode IN ('in_place', 'staged'));

-- Artifact rows are the ownership registry for staged copies. Keep their work
-- reference restrictive so stale-work cleanup cannot discard filesystem ownership;
-- explicit output reset/reconciliation must remove artifact references first.
-- Paths are relative to the managed output directory; filesystem
-- creation/validation is implemented by the later staged-preparation stage.
CREATE TABLE source_analysis_artifact (
    id uuid PRIMARY KEY,
    work_id uuid NOT NULL REFERENCES source_analysis_work(id) ON DELETE RESTRICT,
    relative_output_path text NOT NULL UNIQUE CHECK (
        relative_output_path <> ''
        AND relative_output_path !~ '(^/|(^|/)\.\.?(/|$))'
    ),
    source_size_bytes bigint NOT NULL CHECK (source_size_bytes >= 0),
    source_mtime timestamptz NOT NULL,
    owner_operation_id uuid NOT NULL,
    owner_operation_attempt integer NOT NULL CHECK (owner_operation_attempt > 0),
    owner_job_id bigint NOT NULL,
    state text NOT NULL CHECK (state IN ('acquiring', 'ready', 'cleanup_eligible', 'cleanup_failed')),
    cleanup_error text,
    cleanup_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (owner_operation_id, owner_operation_attempt, owner_job_id)
        REFERENCES operation(id, attempt, river_job_id) ON DELETE RESTRICT,
    CHECK (
        (state = 'cleanup_failed' AND cleanup_error IS NOT NULL AND cleanup_error <> '' AND cleanup_at IS NOT NULL)
        OR (state <> 'cleanup_failed' AND cleanup_error IS NULL AND cleanup_at IS NULL)
    )
);

CREATE INDEX source_analysis_artifact_work_idx ON source_analysis_artifact(work_id);
CREATE INDEX source_analysis_artifact_cleanup_idx ON source_analysis_artifact(state)
    WHERE state IN ('cleanup_eligible', 'cleanup_failed');
