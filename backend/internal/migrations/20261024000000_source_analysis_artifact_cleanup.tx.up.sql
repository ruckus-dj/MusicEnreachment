ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN (
        'install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source',
        'cleanup_source_analysis_artifacts'
    ));

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

CREATE TABLE source_analysis_artifact_cleanup_item (
    artifact_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    operation_attempt integer NOT NULL CHECK (operation_attempt > 0),
    job_id bigint NOT NULL CHECK (job_id > 0),
    relative_output_path text NOT NULL CHECK (relative_output_path <> ''),
    state text NOT NULL CHECK (state IN ('claimed', 'succeeded', 'failed')),
    safe_error text,
    claimed_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    PRIMARY KEY (artifact_id, operation_id, operation_attempt, job_id),
    CHECK ((state = 'claimed' AND finished_at IS NULL) OR
           (state IN ('succeeded', 'failed') AND finished_at IS NOT NULL)),
    CHECK ((state = 'failed' AND safe_error IS NOT NULL AND safe_error <> '') OR
           (state <> 'failed' AND safe_error IS NULL))
);

CREATE UNIQUE INDEX source_analysis_artifact_cleanup_one_claim_idx
    ON source_analysis_artifact_cleanup_item (artifact_id)
    WHERE state = 'claimed';

CREATE INDEX source_analysis_artifact_cleanup_operation_idx
    ON source_analysis_artifact_cleanup_item (operation_id, operation_attempt, job_id);
