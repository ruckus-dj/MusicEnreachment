DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM source_analysis_artifact AS artifact
        JOIN operation AS operation ON operation.id = artifact.owner_operation_id
        WHERE operation.attempt IS DISTINCT FROM artifact.owner_operation_attempt
           OR operation.river_job_id IS DISTINCT FROM artifact.owner_job_id
    ) THEN
        RAISE EXCEPTION 'cannot downgrade source analysis execution identity: artifact delivery no longer matches mutable operation identity';
    END IF;
END;
$$;

DROP TRIGGER source_analysis_work_execution_immutable ON source_analysis_work_execution;
DROP FUNCTION prevent_source_analysis_work_execution_mutation();

ALTER TABLE source_analysis_artifact
    DROP CONSTRAINT source_analysis_artifact_execution_fkey;

ALTER TABLE source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_owner_operation_id_owner_operation_attempt_owner_job_id_fkey
    FOREIGN KEY (owner_operation_id, owner_operation_attempt, owner_job_id)
    REFERENCES operation(id, attempt, river_job_id) ON DELETE RESTRICT;

DROP TABLE source_analysis_work_execution;
