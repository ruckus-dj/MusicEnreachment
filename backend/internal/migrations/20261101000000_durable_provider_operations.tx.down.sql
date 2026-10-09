DO $$
BEGIN
    IF to_regclass('river_job') IS NOT NULL THEN
        DELETE FROM river_job
        WHERE kind = 'provider_fetch_v1'
          AND args ->> 'operation_id' IN (
              SELECT id::text FROM operation WHERE kind IN (
                  'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search'
              )
          );
    END IF;
END $$;

DELETE FROM operation
WHERE kind IN ('provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search');

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN (
        'install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source',
        'cleanup_source_analysis_artifacts'
    ));

ALTER TABLE operation DROP CONSTRAINT operation_provider_intent;
ALTER TABLE operation
    DROP COLUMN provider_delivery_claimed_at,
    DROP COLUMN provider_execution_epoch,
    DROP COLUMN provider_explicit_refresh,
    DROP COLUMN provider_cache_key,
    DROP COLUMN provider_configuration_identity,
    DROP COLUMN provider_source_id;
