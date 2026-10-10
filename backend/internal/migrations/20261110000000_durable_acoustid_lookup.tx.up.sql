ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN (
        'install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source',
        'cleanup_source_analysis_artifacts', 'provider_release_lookup', 'provider_recording_lookup',
        'provider_release_search', 'provider_recording_search', 'provider_acoustid_lookup', 'matching_evaluation_v1'
    ));

ALTER TABLE operation
    DROP CONSTRAINT operation_provider_intent,
    ADD CONSTRAINT operation_provider_intent CHECK (
        (kind IN ('provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search', 'provider_acoustid_lookup')
            AND provider_source_id IS NOT NULL
            AND COALESCE(provider_configuration_identity, '') <> ''
            AND COALESCE(provider_cache_key, '') <> '')
        OR
        (kind NOT IN ('provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search', 'provider_acoustid_lookup')
            AND provider_source_id IS NULL
            AND provider_configuration_identity IS NULL
            AND provider_cache_key IS NULL
            AND provider_explicit_refresh = false)
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search', 'provider_acoustid_lookup', 'matching_evaluation_v1')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search', 'provider_acoustid_lookup', 'matching_evaluation_v1')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );
