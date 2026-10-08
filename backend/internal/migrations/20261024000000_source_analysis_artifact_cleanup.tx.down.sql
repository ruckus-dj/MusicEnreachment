DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM operation WHERE kind = 'cleanup_source_analysis_artifacts') THEN
        RAISE EXCEPTION 'cannot roll back artifact cleanup while cleanup operation history survives';
    END IF;
    IF EXISTS (SELECT 1 FROM source_analysis_artifact_cleanup_item) THEN
        RAISE EXCEPTION 'cannot roll back artifact cleanup while cleanup outcomes survive';
    END IF;
END;
$$;

DROP TABLE source_analysis_artifact_cleanup_item;

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN ('install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source'));
