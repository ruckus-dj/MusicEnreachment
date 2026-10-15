-- Fresh-instance baseline: application core.
-- Later baselines attach cross-domain foreign keys and guards where necessary.
SET LOCAL check_function_bodies = false;

-- table: app_setting
CREATE TABLE public.app_setting (
    setting_name text NOT NULL,
    setting_value text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

-- table: operation
CREATE TABLE public.operation (
    id uuid NOT NULL,
    kind text NOT NULL,
    state text NOT NULL,
    stage text NOT NULL,
    input_snapshot jsonb NOT NULL,
    bytes_completed bigint DEFAULT 0 NOT NULL,
    bytes_total bigint,
    safe_error text,
    river_job_id bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    target_installation_id uuid,
    attempt integer DEFAULT 1 NOT NULL,
    target_source_root_id uuid,
    target_source_location_id uuid,
    source_analysis_mode text,
    target_work_id uuid,
    target_step text,
    tools_read_required boolean DEFAULT false NOT NULL,
    rerun_target boolean DEFAULT false NOT NULL,
    provider_source_id uuid,
    provider_configuration_identity text,
    provider_cache_key text,
    provider_explicit_refresh boolean DEFAULT false NOT NULL,
    provider_delivery_claimed_at timestamp with time zone,
    provider_execution_epoch integer DEFAULT 0 NOT NULL,
    CONSTRAINT operation_analysis_target_shape CHECK (((kind <> 'analyze_source'::text) OR ((target_installation_id IS NULL) AND ((state <> ALL (ARRAY['queued'::text, 'running'::text])) OR (target_source_root_id IS NOT NULL))))),
    CONSTRAINT operation_attempt_check CHECK ((attempt > 0)),
    CONSTRAINT operation_bytes_completed_check CHECK ((bytes_completed >= 0)),
    CONSTRAINT operation_check CHECK (((bytes_total IS NULL) OR (bytes_total >= bytes_completed))),
    CONSTRAINT operation_failed_has_error CHECK (((state <> 'failed'::text) OR (safe_error IS NOT NULL))),
    CONSTRAINT operation_finished_at_matches_state CHECK ((((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (finished_at IS NULL)) OR ((state = ANY (ARRAY['failed'::text, 'succeeded'::text])) AND (finished_at IS NOT NULL)))),
    CONSTRAINT operation_kind_check CHECK ((kind = ANY (ARRAY['install'::text, 'activate'::text, 'delete'::text, 'move_tools_root'::text, 'scan_source'::text, 'analyze_source'::text, 'cleanup_source_analysis_artifacts'::text, 'provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text, 'matching_evaluation_v1'::text, 'matching_apply_v1'::text]))),
    CONSTRAINT operation_mutation_has_target_installation CHECK (((kind = ANY (ARRAY['move_tools_root'::text, 'scan_source'::text, 'analyze_source'::text, 'cleanup_source_analysis_artifacts'::text, 'provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text, 'matching_evaluation_v1'::text, 'matching_apply_v1'::text])) OR (state <> ALL (ARRAY['queued'::text, 'running'::text])) OR (target_installation_id IS NOT NULL))),
    CONSTRAINT operation_normalized_analysis_shape CHECK ((((kind <> 'analyze_source'::text) OR (state <> ALL (ARRAY['queued'::text, 'running'::text])) OR (source_analysis_mode IS NOT NULL)) AND (((source_analysis_mode IS NULL) OR ((kind = 'analyze_source'::text) AND (((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_source_root_id IS NOT NULL) AND (((source_analysis_mode = 'batch'::text) AND (target_source_location_id IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL)) OR ((source_analysis_mode = 'single_step'::text) AND (target_source_location_id IS NOT NULL) AND (target_work_id IS NOT NULL) AND (target_step = ANY (ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text]))))) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_source_root_id IS NULL) AND (target_source_location_id IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL))))) IS TRUE))),
    CONSTRAINT operation_provider_execution_epoch_check CHECK ((provider_execution_epoch >= 0)),
    CONSTRAINT operation_provider_intent CHECK ((((kind = ANY (ARRAY['provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text])) AND (provider_source_id IS NOT NULL) AND (COALESCE(provider_configuration_identity, ''::text) <> ''::text) AND (COALESCE(provider_cache_key, ''::text) <> ''::text)) OR ((kind <> ALL (ARRAY['provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text])) AND (provider_source_id IS NULL) AND (provider_configuration_identity IS NULL) AND (provider_cache_key IS NULL) AND (provider_explicit_refresh = false)))),
    CONSTRAINT operation_source_analysis_mode_valid CHECK (((source_analysis_mode IS NULL) OR (source_analysis_mode = ANY (ARRAY['batch'::text, 'single_step'::text])))),
    CONSTRAINT operation_source_analysis_selectors_kind CHECK (((source_analysis_mode IS NULL) OR (kind = 'analyze_source'::text))),
    CONSTRAINT operation_source_analysis_target_shape CHECK (((((source_analysis_mode IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (NOT rerun_target) AND ((NOT tools_read_required) OR (kind = 'scan_source'::text))) OR ((source_analysis_mode = 'batch'::text) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (NOT rerun_target)) OR ((source_analysis_mode = 'single_step'::text) AND (((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_work_id IS NOT NULL) AND (target_step = ANY (ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text]))) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_work_id IS NULL) AND (target_step IS NULL))) AND ((NOT rerun_target) OR ((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_step = 'fingerprint'::text)) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_work_id IS NULL) AND (target_step IS NULL))))) IS TRUE)),
    CONSTRAINT operation_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'failed'::text, 'succeeded'::text]))),
    CONSTRAINT operation_target_identity CHECK (((kind = ANY (ARRAY['move_tools_root'::text, 'scan_source'::text, 'analyze_source'::text, 'cleanup_source_analysis_artifacts'::text, 'provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text, 'matching_evaluation_v1'::text, 'matching_apply_v1'::text])) OR (COALESCE((input_snapshot ->> 'target_identity'::text), ''::text) <> ''::text)))
);

-- table: output_reset_journal
CREATE TABLE public.output_reset_journal (
    token uuid NOT NULL,
    state text NOT NULL,
    old_root text NOT NULL,
    new_root text NOT NULL,
    created_directories text[] DEFAULT '{}'::text[] NOT NULL,
    directory_manifest jsonb DEFAULT '[]'::jsonb NOT NULL,
    cleanup_outcomes jsonb DEFAULT '{"invalidated_artifact_ids": [], "removed_candidate_operation_ids": []}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT output_reset_journal_check CHECK ((old_root <> new_root)),
    CONSTRAINT output_reset_journal_state_check CHECK ((state = ANY (ARRAY['preparing'::text, 'committed'::text, 'finished'::text])))
);

-- table: tool_installation
CREATE TABLE public.tool_installation (
    id uuid NOT NULL,
    package_kind text NOT NULL,
    platform_goos text NOT NULL,
    platform_goarch text NOT NULL,
    source_name text NOT NULL,
    release_identity text NOT NULL,
    relative_path text NOT NULL,
    state text NOT NULL,
    executable_versions jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    verified_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    artifact_identities jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT tool_installation_package_kind_check CHECK ((package_kind = ANY (ARRAY['ffmpeg'::text, 'fpcalc'::text]))),
    CONSTRAINT tool_installation_ready_is_verified CHECK (((state <> 'ready'::text) OR (verified_at IS NOT NULL))),
    CONSTRAINT tool_installation_relative_path_matches_identity CHECK (((package_kind = ANY (ARRAY['ffmpeg'::text, 'fpcalc'::text])) AND (release_identity <> ''::text) AND (release_identity <> ALL (ARRAY['.'::text, '..'::text])) AND (release_identity !~ '[/\\:]'::text) AND (relative_path = ((package_kind ||
CASE
    WHEN (platform_goos = 'windows'::text) THEN chr(92)
    ELSE '/'::text
END) || release_identity)))),
    CONSTRAINT tool_installation_relative_path_not_empty CHECK ((relative_path <> ''::text)),
    CONSTRAINT tool_installation_release_identity_not_empty CHECK ((release_identity <> ''::text)),
    CONSTRAINT tool_installation_state_check CHECK ((state = ANY (ARRAY['preparing'::text, 'ready'::text, 'failed'::text])))
);

-- table: tools_execution_claim
CREATE TABLE public.tools_execution_claim (
    operation_id uuid NOT NULL,
    attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    borrower text NOT NULL,
    claimed_at timestamp with time zone DEFAULT now() NOT NULL,
    borrowed boolean DEFAULT false NOT NULL,
    CONSTRAINT tools_execution_claim_attempt_check CHECK ((attempt > 0)),
    CONSTRAINT tools_execution_claim_borrower_check CHECK ((borrower = ANY (ARRAY['install'::text, 'move_tools_root'::text])))
);

-- constraint: app_setting app_setting_pkey
ALTER TABLE ONLY public.app_setting
    ADD CONSTRAINT app_setting_pkey PRIMARY KEY (setting_name);

-- constraint: operation operation_active_tools_operations_exclusive
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_active_tools_operations_exclusive EXCLUDE USING gist (numrange(
CASE
    WHEN (kind = 'move_tools_root'::text) THEN NULL::numeric
    WHEN tools_read_required THEN (hashtextextended((id)::text, (0)::bigint))::numeric
    WHEN (target_installation_id IS NOT NULL) THEN (hashtextextended((target_installation_id)::text, (0)::bigint))::numeric
    ELSE (hashtextextended((input_snapshot ->> 'target_identity'::text), (0)::bigint))::numeric
END,
CASE
    WHEN (kind = 'move_tools_root'::text) THEN NULL::numeric
    WHEN tools_read_required THEN (hashtextextended((id)::text, (0)::bigint))::numeric
    WHEN (target_installation_id IS NOT NULL) THEN (hashtextextended((target_installation_id)::text, (0)::bigint))::numeric
    ELSE (hashtextextended((input_snapshot ->> 'target_identity'::text), (0)::bigint))::numeric
END,
CASE
    WHEN (kind = 'move_tools_root'::text) THEN '()'::text
    ELSE '[]'::text
END) WITH &&) WHERE (((state = ANY (ARRAY['queued'::text, 'running'::text])) AND ((kind = 'move_tools_root'::text) OR tools_read_required OR (target_installation_id IS NOT NULL) OR (COALESCE((input_snapshot ->> 'target_identity'::text), ''::text) <> ''::text))));

-- constraint: operation operation_id_attempt_job_unique
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_id_attempt_job_unique UNIQUE (id, attempt, river_job_id);

-- constraint: operation operation_pkey
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_pkey PRIMARY KEY (id);

-- constraint: operation operation_river_job_id_key
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_river_job_id_key UNIQUE (river_job_id);

-- constraint: output_reset_journal output_reset_journal_pkey
ALTER TABLE ONLY public.output_reset_journal
    ADD CONSTRAINT output_reset_journal_pkey PRIMARY KEY (token);

-- constraint: tool_installation tool_installation_identity_unique
ALTER TABLE ONLY public.tool_installation
    ADD CONSTRAINT tool_installation_identity_unique UNIQUE (package_kind, source_name, release_identity, platform_goos, platform_goarch);

-- constraint: tool_installation tool_installation_pkey
ALTER TABLE ONLY public.tool_installation
    ADD CONSTRAINT tool_installation_pkey PRIMARY KEY (id);

-- constraint: tools_execution_claim tools_execution_claim_pkey
ALTER TABLE ONLY public.tools_execution_claim
    ADD CONSTRAINT tools_execution_claim_pkey PRIMARY KEY (operation_id);

-- index: operation_active_source_analysis_work_idx
CREATE INDEX operation_active_source_analysis_work_idx ON public.operation USING btree (target_work_id) WHERE ((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_work_id IS NOT NULL));

-- index: operation_active_source_root_idx
CREATE INDEX operation_active_source_root_idx ON public.operation USING btree (target_source_root_id) WHERE ((kind = 'scan_source'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

-- index: operation_active_updated_at_idx
CREATE INDEX operation_active_updated_at_idx ON public.operation USING btree (updated_at) WHERE (state = ANY (ARRAY['queued'::text, 'running'::text, 'failed'::text]));

-- index: operation_one_active_installation
CREATE UNIQUE INDEX operation_one_active_installation ON public.operation USING btree (target_installation_id) WHERE ((kind <> 'move_tools_root'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

-- index: operation_one_active_source_root_scan
CREATE UNIQUE INDEX operation_one_active_source_root_scan ON public.operation USING btree (target_source_root_id) WHERE ((kind = 'scan_source'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

-- index: operation_one_active_tools_root_move
CREATE UNIQUE INDEX operation_one_active_tools_root_move ON public.operation USING btree (kind) WHERE ((kind = 'move_tools_root'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

-- index: operation_scan_source_updated_at_idx
CREATE INDEX operation_scan_source_updated_at_idx ON public.operation USING btree (updated_at) WHERE ((kind = 'scan_source'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

-- index: operation_target_installation_active_idx
CREATE INDEX operation_target_installation_active_idx ON public.operation USING btree (target_installation_id) WHERE (state = ANY (ARRAY['queued'::text, 'running'::text]));

-- index: operation_target_source_root_idx
CREATE INDEX operation_target_source_root_idx ON public.operation USING btree (target_source_root_id) WHERE (target_source_root_id IS NOT NULL);

-- index: output_reset_one_unresolved_idx
CREATE UNIQUE INDEX output_reset_one_unresolved_idx ON public.output_reset_journal USING btree ((true)) WHERE (state <> 'finished'::text);

-- index: tool_installation_package_platform_idx
CREATE INDEX tool_installation_package_platform_idx ON public.tool_installation USING btree (package_kind, platform_goos, platform_goarch);

-- fk constraint: operation operation_target_installation_id_fkey
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_installation_id_fkey FOREIGN KEY (target_installation_id) REFERENCES public.tool_installation(id) ON DELETE SET NULL;

-- fk constraint: tools_execution_claim tools_execution_claim_operation_id_fkey
ALTER TABLE ONLY public.tools_execution_claim
    ADD CONSTRAINT tools_execution_claim_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;
