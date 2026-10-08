-- One immutable record captures the mode selected when an exact work delivery
-- starts. Operation attempt/job fields are intentionally not foreign-keyed to
-- mutable columns on operation.
CREATE TABLE source_analysis_work_execution (
    work_id uuid NOT NULL REFERENCES source_analysis_work(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL REFERENCES operation(id) ON DELETE RESTRICT,
    operation_attempt integer NOT NULL CHECK (operation_attempt > 0),
    job_id bigint NOT NULL CHECK (job_id > 0),
    processing_mode text NOT NULL CHECK (processing_mode IN ('in_place', 'staged')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (work_id, operation_id, operation_attempt, job_id)
);

-- Before I06, an artifact could only be acquired by the staged-only artifact
-- repository. Preserve that proven origin without reading the mutable root mode.
INSERT INTO source_analysis_work_execution
    (work_id, operation_id, operation_attempt, job_id, processing_mode)
SELECT DISTINCT artifact.work_id, artifact.owner_operation_id,
       artifact.owner_operation_attempt, artifact.owner_job_id, 'staged'
FROM source_analysis_artifact AS artifact
JOIN source_analysis_work AS work ON work.id = artifact.work_id
JOIN operation AS operation ON operation.id = artifact.owner_operation_id;

DO $$
DECLARE
    legacy_constraint text;
BEGIN
    SELECT constraint_row.conname INTO legacy_constraint
    FROM pg_constraint AS constraint_row
    WHERE constraint_row.conrelid = 'source_analysis_artifact'::regclass
      AND constraint_row.contype = 'f'
      AND constraint_row.confrelid = 'operation'::regclass
      AND pg_get_constraintdef(constraint_row.oid) LIKE 'FOREIGN KEY (owner_operation_id, owner_operation_attempt, owner_job_id)%';
    IF legacy_constraint IS NULL THEN
        RAISE EXCEPTION 'source analysis artifact legacy delivery constraint was not found';
    END IF;
    EXECUTE format('ALTER TABLE source_analysis_artifact DROP CONSTRAINT %I', legacy_constraint);
END;
$$;

ALTER TABLE source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_execution_fkey
    FOREIGN KEY (work_id, owner_operation_id, owner_operation_attempt, owner_job_id)
    REFERENCES source_analysis_work_execution(work_id, operation_id, operation_attempt, job_id)
    ON DELETE RESTRICT;

CREATE FUNCTION prevent_source_analysis_work_execution_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'source analysis work execution identity is immutable';
END;
$$;

CREATE TRIGGER source_analysis_work_execution_immutable
BEFORE UPDATE ON source_analysis_work_execution
FOR EACH ROW EXECUTE FUNCTION prevent_source_analysis_work_execution_mutation();
