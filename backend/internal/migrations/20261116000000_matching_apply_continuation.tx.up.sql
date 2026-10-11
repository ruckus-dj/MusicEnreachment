ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN (
        'install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source',
        'cleanup_source_analysis_artifacts', 'provider_release_lookup', 'provider_recording_lookup',
        'provider_release_search', 'provider_recording_search', 'provider_artist_search',
        'provider_artist_release_browse', 'provider_acoustid_lookup', 'matching_evaluation_v1',
        'matching_apply_v1'
    ));

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search',
            'provider_recording_search', 'provider_artist_search', 'provider_artist_release_browse',
            'provider_acoustid_lookup', 'matching_evaluation_v1', 'matching_apply_v1')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search',
            'provider_recording_search', 'provider_artist_search', 'provider_artist_release_browse',
            'provider_acoustid_lookup', 'matching_evaluation_v1', 'matching_apply_v1')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

CREATE TABLE matching_apply_request (
    group_id uuid NOT NULL,
    parent_run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE RESTRICT,
    evaluation_revision text NOT NULL CHECK (evaluation_revision <> ''),
    apply_operation_id uuid NOT NULL UNIQUE REFERENCES operation(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (group_id, evaluation_revision)
);

CREATE TABLE matching_apply_receipt (
    apply_operation_id uuid PRIMARY KEY REFERENCES matching_apply_request(apply_operation_id) ON DELETE RESTRICT,
    group_id uuid NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('applied', 'review_required')),
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION reject_matching_apply_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'matching apply receipts are immutable';
END;
$$;

CREATE TRIGGER matching_apply_receipt_write_once
    BEFORE UPDATE OR DELETE ON matching_apply_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_matching_apply_receipt_mutation();
