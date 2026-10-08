ALTER TABLE source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_work_id_id_key UNIQUE (work_id, id);

CREATE TABLE source_analysis_work_artifact_binding (
    work_id uuid PRIMARY KEY REFERENCES source_analysis_work(id) ON DELETE CASCADE,
    artifact_id uuid NOT NULL UNIQUE,
    borrower_operation_id uuid,
    borrower_operation_attempt integer,
    borrower_job_id bigint,
    requested_steps text[] NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT source_analysis_work_artifact_binding_artifact_fk
        FOREIGN KEY (work_id, artifact_id)
        REFERENCES source_analysis_artifact(work_id, id) ON DELETE CASCADE,
    CONSTRAINT source_analysis_work_artifact_binding_execution_fk
        FOREIGN KEY (work_id, borrower_operation_id, borrower_operation_attempt, borrower_job_id)
        REFERENCES source_analysis_work_execution(work_id, operation_id, operation_attempt, job_id)
        ON DELETE SET NULL (borrower_operation_id, borrower_operation_attempt, borrower_job_id),
    CONSTRAINT source_analysis_work_artifact_binding_borrower_shape
        CHECK ((borrower_operation_id IS NULL AND borrower_operation_attempt IS NULL AND borrower_job_id IS NULL)
            OR (borrower_operation_id IS NOT NULL AND borrower_operation_attempt IS NOT NULL AND borrower_operation_attempt > 0
                AND borrower_job_id IS NOT NULL AND borrower_job_id > 0)),
    CONSTRAINT source_analysis_work_artifact_binding_requested_steps
        CHECK (requested_steps <@ ARRAY['sha256','probe','fingerprint']::text[])
);
