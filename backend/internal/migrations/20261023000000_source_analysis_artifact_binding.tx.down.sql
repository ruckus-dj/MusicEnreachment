DROP TABLE source_analysis_work_artifact_binding;

ALTER TABLE source_analysis_artifact
    DROP CONSTRAINT source_analysis_artifact_work_id_id_key;
