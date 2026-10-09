ALTER TABLE source_analysis_artifact
    DROP CONSTRAINT source_analysis_artifact_unknown_requested_steps_empty,
    DROP CONSTRAINT source_analysis_artifact_requested_steps_order,
    DROP CONSTRAINT source_analysis_artifact_requested_steps_unique,
    DROP COLUMN requested_steps_known,
    DROP COLUMN requested_steps;
