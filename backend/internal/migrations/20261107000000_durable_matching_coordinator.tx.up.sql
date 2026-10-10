-- A matching run is an immutable, explicitly admitted plan. Group generations
-- and delivery identities are separate rows so refreshes and duplicate River
-- wakes can be fenced without relying on worker-local state.
ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN (
        'install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source',
        'cleanup_source_analysis_artifacts', 'provider_release_lookup', 'provider_recording_lookup',
        'provider_release_search', 'provider_recording_search', 'matching_evaluation_v1'
    )),
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search',
            'matching_evaluation_v1')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    ),
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
            'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search', 'provider_recording_search',
            'matching_evaluation_v1')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

CREATE TABLE incoming_matching_generation (
    group_id uuid PRIMARY KEY,
    high_water bigint NOT NULL DEFAULT 0 CHECK (high_water >= 0),
    current_run_id uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE matching_run (
    id uuid PRIMARY KEY,
    operation_id uuid NOT NULL UNIQUE REFERENCES operation(id) ON DELETE CASCADE,
    plan_admission_key text NOT NULL UNIQUE CHECK (length(btrim(plan_admission_key)) > 0),
    plan_digest bytea NOT NULL CHECK (octet_length(plan_digest) = 32),
    immutable_plan jsonb NOT NULL CHECK (jsonb_typeof(immutable_plan) = 'object'),
    grouping_revision bigint NOT NULL CHECK (grouping_revision >= 0),
    grouping_epoch bigint NOT NULL CHECK (grouping_epoch >= 0),
    provider_source_id uuid NOT NULL REFERENCES provider_source(id) ON DELETE RESTRICT,
    provider_configuration_identity text NOT NULL CHECK (length(btrim(provider_configuration_identity)) > 0),
    policy_version text NOT NULL CHECK (length(btrim(policy_version)) > 0),
    state text NOT NULL CHECK (state IN ('queued', 'running', 'waiting_provider', 'succeeded', 'failed')),
    execution_epoch bigint NOT NULL DEFAULT 0 CHECK (execution_epoch >= 0),
    current_river_job_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX matching_run_operation_id_idx ON matching_run(operation_id);
CREATE INDEX matching_run_active_idx ON matching_run(state) WHERE state IN ('queued', 'running', 'waiting_provider');

CREATE TABLE matching_run_target (
    run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE CASCADE,
    group_id uuid NOT NULL,
    captured_group_revision text NOT NULL CHECK (length(btrim(captured_group_revision)) > 0),
    target_generation bigint NOT NULL CHECK (target_generation > 0),
    PRIMARY KEY (run_id, group_id),
    UNIQUE (group_id, target_generation)
);

ALTER TABLE incoming_matching_generation
    ADD CONSTRAINT incoming_matching_generation_current_run_fk
    FOREIGN KEY (current_run_id) REFERENCES matching_run(id) ON DELETE SET NULL;

CREATE TABLE matching_provider_dependency (
    parent_run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE CASCADE,
    request_id uuid NOT NULL REFERENCES operation(id) ON DELETE RESTRICT,
    cache_key text NOT NULL CHECK (length(btrim(cache_key)) > 0),
    terminal_outcome text CHECK (terminal_outcome IS NULL OR terminal_outcome IN ('succeeded', 'failed')),
    settled_at timestamptz,
    PRIMARY KEY (parent_run_id, request_id),
    CHECK ((terminal_outcome IS NULL) = (settled_at IS NULL))
);

CREATE INDEX matching_provider_dependency_pending_idx
    ON matching_provider_dependency(request_id) WHERE terminal_outcome IS NULL;

CREATE TABLE matching_delivery (
    id bigserial PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES matching_run(id) ON DELETE CASCADE,
    river_job_id bigint NOT NULL UNIQUE,
    registered_at timestamptz NOT NULL DEFAULT now(),
    consumed_at timestamptz,
    UNIQUE (run_id, river_job_id)
);
