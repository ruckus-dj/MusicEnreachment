DROP TABLE matching_delivery;
DROP TABLE matching_provider_dependency;
ALTER TABLE incoming_matching_generation DROP CONSTRAINT incoming_matching_generation_current_run_fk;
DROP TABLE matching_run_target;
DROP TABLE matching_run;
DROP TABLE incoming_matching_generation;

DELETE FROM operation WHERE kind='matching_evaluation_v1';

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN (
        'install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source',
        'cleanup_source_analysis_artifacts', 'provider_release_lookup', 'provider_recording_lookup',
        'provider_release_search', 'provider_recording_search'
    )),
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    ),
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );
