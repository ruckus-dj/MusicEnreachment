-- Fresh-instance baseline: source inventory analysis.
-- Later baselines attach cross-domain foreign keys and guards where necessary.
SET LOCAL check_function_bodies = false;

-- function: check_source_analysis_operation_holds()
CREATE FUNCTION public.check_source_analysis_operation_holds() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    operation_row operation%ROWTYPE;
    snapshot jsonb;
    affected_operation_id uuid;
    work_hold_count bigint;
    tool_hold_count bigint;
    has_execution boolean;
BEGIN
    IF TG_TABLE_NAME = 'operation' THEN
        affected_operation_id := COALESCE(NEW.id, OLD.id);
    ELSE
        affected_operation_id := COALESCE(NEW.operation_id, OLD.operation_id);
    END IF;
    SELECT * INTO operation_row FROM operation WHERE id = affected_operation_id;
    IF NOT FOUND THEN RETURN NULL; END IF;

    SELECT count(*) INTO work_hold_count FROM operation_source_work_hold WHERE operation_id = operation_row.id;
    SELECT count(*) INTO tool_hold_count FROM operation_tool_read_hold WHERE operation_id = operation_row.id;
    SELECT EXISTS(SELECT 1 FROM source_analysis_step WHERE execution_operation_id = operation_row.id) INTO has_execution;
    IF operation_row.tools_read_required IS DISTINCT FROM (tool_hold_count > 0) THEN
        RAISE EXCEPTION 'operation tools_read_required must match its tool read holds';
    END IF;
    IF operation_row.source_analysis_mode IS NULL THEN
        IF work_hold_count <> 0 OR has_execution OR
           (operation_row.state NOT IN ('queued', 'running') AND tool_hold_count <> 0) THEN
            RAISE EXCEPTION 'non-analysis operation cannot retain work holds or execution, and terminal operations cannot retain tool holds';
        END IF;
        RETURN NULL;
    END IF;

    snapshot := operation_row.input_snapshot;
    IF operation_row.state IN ('queued', 'running') THEN
        IF snapshot->>'mode' IS DISTINCT FROM operation_row.source_analysis_mode OR
           (snapshot->>'rerun_target')::boolean IS DISTINCT FROM operation_row.rerun_target OR
           (snapshot->>'schema_version')::integer IS DISTINCT FROM 1 OR
           jsonb_typeof(snapshot->'rerun_target') IS DISTINCT FROM 'boolean' THEN
            RAISE EXCEPTION 'active normalized source-analysis operation selectors must match its explicit snapshot';
        END IF;
        IF (operation_row.source_analysis_mode='single_step' AND
            (operation_row.target_work_id IS DISTINCT FROM (snapshot->>'target_work_id')::uuid OR operation_row.target_step IS DISTINCT FROM (snapshot->>'target_step'))) OR
           (operation_row.source_analysis_mode='batch' AND
            ((snapshot->>'target_work_id') IS NOT NULL OR (snapshot->>'target_step') IS NOT NULL)) THEN
            RAISE EXCEPTION 'active normalized source-analysis target must match its snapshot';
        END IF;
        IF jsonb_typeof(snapshot->'work_ids') IS DISTINCT FROM 'array' OR work_hold_count <> jsonb_array_length(snapshot->'work_ids') THEN
            RAISE EXCEPTION 'active normalized source-analysis operation must hold exactly its pinned work items';
        END IF;
        IF operation_row.source_analysis_mode='batch' AND
           (jsonb_array_length(snapshot->'work_ids') <> 1 OR work_hold_count <> 1) THEN
            RAISE EXCEPTION 'active batch source-analysis operation must hold exactly one pinned work item';
        END IF;
        IF EXISTS (
            SELECT 1 FROM jsonb_array_elements_text(snapshot->'work_ids') AS selected(work_id)
            WHERE NOT EXISTS (SELECT 1 FROM operation_source_work_hold h WHERE h.operation_id=operation_row.id AND h.work_id=selected.work_id::uuid)
        ) THEN
            RAISE EXCEPTION 'active normalized source-analysis holds must match the snapshot';
        END IF;
        IF operation_row.source_analysis_mode='single_step' AND NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements_text(snapshot->'work_ids') AS selected(work_id)
            WHERE selected.work_id::uuid=operation_row.target_work_id
        ) THEN
            RAISE EXCEPTION 'active single-step target must be a pinned work item';
        END IF;
        IF (operation_row.source_analysis_mode='batch' AND operation_row.target_source_location_id IS NOT NULL) OR
           EXISTS (
               SELECT 1 FROM operation_source_work_hold h JOIN source_analysis_work w ON w.id=h.work_id
               WHERE h.operation_id=operation_row.id
                 AND (w.source_root_id IS DISTINCT FROM operation_row.target_source_root_id
                      OR (operation_row.target_source_location_id IS NOT NULL AND w.location_id IS DISTINCT FROM operation_row.target_source_location_id))
           ) THEN
            RAISE EXCEPTION 'active source-analysis holds must match operation source selectors';
        END IF;
    ELSE
        IF work_hold_count <> 0 OR tool_hold_count <> 0 OR has_execution THEN
            RAISE EXCEPTION 'terminal normalized source-analysis operation cannot retain holds or execution triples';
        END IF;
    END IF;
    RETURN NULL;
END $$;

-- function: check_source_analysis_step_membership()
CREATE FUNCTION public.check_source_analysis_step_membership() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    operation_id_value uuid;
    operation_row operation%ROWTYPE;
    work_id_value uuid;
    step_value text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        operation_id_value := OLD.execution_operation_id;
        work_id_value := OLD.work_id;
        step_value := OLD.step;
    ELSE
        operation_id_value := NEW.execution_operation_id;
        work_id_value := NEW.work_id;
        step_value := NEW.step;
    END IF;
    IF operation_id_value IS NULL THEN RETURN NULL; END IF;
    SELECT * INTO operation_row FROM operation WHERE id=operation_id_value;
    IF NOT FOUND THEN RAISE EXCEPTION 'source-analysis execution must reference an existing operation'; END IF;
    IF operation_row.source_analysis_mode IS NULL THEN RAISE EXCEPTION 'source-analysis execution must reference a normalized analysis operation'; END IF;
    IF operation_row.state NOT IN ('queued','running') THEN
        IF TG_OP = 'DELETE' THEN RETURN NULL; END IF;
        RAISE EXCEPTION 'terminal source-analysis operation cannot acquire execution';
    END IF;
    IF operation_row.source_analysis_mode='batch' AND operation_row.target_source_location_id IS NOT NULL THEN
        RAISE EXCEPTION 'batch source-analysis execution cannot have a single-location target';
    END IF;
    IF operation_row.source_analysis_mode='single_step' AND operation_row.target_source_location_id IS NULL THEN
        RAISE EXCEPTION 'single-step source-analysis execution requires a location target';
    END IF;
    IF NOT EXISTS (
           SELECT 1 FROM source_analysis_work w WHERE w.id=work_id_value
             AND w.source_root_id=operation_row.target_source_root_id
             AND (operation_row.target_source_location_id IS NULL OR w.location_id=operation_row.target_source_location_id)
       ) OR NOT EXISTS (SELECT 1 FROM operation_source_work_hold WHERE operation_id=operation_id_value AND work_id=work_id_value)
       OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(operation_row.input_snapshot->'work_ids') AS selected(work_id) WHERE selected.work_id::uuid=work_id_value)
       OR (operation_row.source_analysis_mode='single_step' AND (operation_row.target_work_id<>work_id_value OR operation_row.target_step<>step_value)) THEN
        RAISE EXCEPTION 'active source-analysis execution must match its source selectors, pinned work, and step selection';
    END IF;
    IF step_value NOT IN ('sha256','probe','fingerprint','metadata') THEN
        RAISE EXCEPTION 'active source-analysis execution must select a supported step';
    END IF;
    IF operation_row.source_analysis_mode='batch' AND operation_row.target_work_id IS NOT NULL THEN
        RAISE EXCEPTION 'batch source-analysis execution cannot have a single-work target';
    END IF;
    IF operation_row.source_analysis_mode='batch' THEN
        IF jsonb_typeof(operation_row.input_snapshot->'work_ids') IS DISTINCT FROM 'array' OR
           jsonb_array_length(operation_row.input_snapshot->'work_ids') <> 1 OR
           jsonb_typeof(operation_row.input_snapshot->'selected_steps') IS DISTINCT FROM 'array' OR
           jsonb_array_length(operation_row.input_snapshot->'selected_steps') = 0 THEN
            RAISE EXCEPTION 'active batch source-analysis operation requires one work item and explicit selected steps';
        END IF;
        IF EXISTS (
            SELECT 1 FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps') AS selected(tuple)
            WHERE jsonb_typeof(selected.tuple) IS DISTINCT FROM 'object'
               OR (selected.tuple - 'work_id' - 'step') IS DISTINCT FROM '{}'::jsonb
               OR jsonb_typeof(selected.tuple->'work_id') IS DISTINCT FROM 'string'
               OR jsonb_typeof(selected.tuple->'step') IS DISTINCT FROM 'string'
               OR selected.tuple->>'step' NOT IN ('sha256', 'probe', 'fingerprint', 'metadata')
               OR (selected.tuple->>'work_id')::uuid IS DISTINCT FROM work_id_value
        ) OR
        (SELECT count(*) FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps')) <>
        (SELECT count(DISTINCT ((selected.tuple->>'work_id')::uuid, selected.tuple->>'step'))
         FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps') AS selected(tuple)) OR
        NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps') AS selected(tuple)
            WHERE (selected.tuple->>'work_id')::uuid = work_id_value AND selected.tuple->>'step' = step_value
        ) THEN
            RAISE EXCEPTION 'active source-analysis execution must belong to an exact selected work/step tuple';
        END IF;
    END IF;
    RETURN NULL;
END $$;

-- function: prevent_source_analysis_work_execution_mutation()
CREATE FUNCTION public.prevent_source_analysis_work_execution_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'source analysis work execution identity is immutable';
END;
$$;

-- function: prevent_source_work_variant_identity_rekey()
CREATE FUNCTION public.prevent_source_work_variant_identity_rekey() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
	IF NEW.work_id IS DISTINCT FROM OLD.work_id OR NEW.media_variant_id IS DISTINCT FROM OLD.media_variant_id THEN
		RAISE EXCEPTION 'source work variant identity is immutable';
	END IF;
	RETURN NEW;
END;
$$;

-- function: reject_manual_picker_search_result_mutation()
CREATE FUNCTION public.reject_manual_picker_search_result_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'manual picker search results are immutable';
END;
$$;

-- function: source_analysis_immutable_result()
CREATE FUNCTION public.source_analysis_immutable_result() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'source analysis result rows are immutable';
END $$;

-- function: source_analysis_work_attachment_guard()
CREATE FUNCTION public.source_analysis_work_attachment_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.current_location_id IS NULL THEN
            NEW.current_location_id := NEW.location_id;
        END IF;
    ELSIF OLD.current_location_id IS NULL AND NEW.current_location_id IS NOT NULL THEN
        RAISE EXCEPTION 'retired source analysis work cannot be reattached';
    ELSIF OLD.current_location_id IS NOT NULL AND NEW.current_location_id IS NULL AND EXISTS (
        SELECT 1 FROM operation_source_work_hold hold
        JOIN operation operation ON operation.id=hold.operation_id
        WHERE hold.work_id=OLD.id AND operation.state IN ('queued','running')
    ) THEN
        RAISE EXCEPTION 'active source analysis work cannot be retired';
    END IF;
    RETURN NEW;
END $$;

-- function: source_analysis_work_hold_requires_current_attachment()
CREATE FUNCTION public.source_analysis_work_hold_requires_current_attachment() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM 1 FROM source_analysis_work
        WHERE id=NEW.work_id AND current_location_id IS NOT NULL
        FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'retired source analysis work cannot be held';
    END IF;
    RETURN NEW;
END $$;

-- function: source_fingerprint_result_identity_immutable()
CREATE FUNCTION public.source_fingerprint_result_identity_immutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR (OLD.source_sha256 IS NOT NULL AND NEW.source_sha256 IS DISTINCT FROM OLD.source_sha256) THEN
        RAISE EXCEPTION 'source fingerprint result identity is immutable';
    END IF;
    RETURN NEW;
END $$;

-- function: validate_media_probe_cache_association()
CREATE FUNCTION public.validate_media_probe_cache_association() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    canonical_size bigint;
    result_size bigint;
    result_version text;
    result_policy integer;
BEGIN
    NEW.ffprobe_version_sha256 := sha256(convert_to(NEW.ffprobe_version, 'UTF8'));
    SELECT size_bytes INTO canonical_size FROM media_variant WHERE source_sha256 = NEW.source_sha256;
    SELECT size_bytes, ffprobe_version, analysis_policy_version
      INTO result_size, result_version, result_policy
      FROM media_variant WHERE id = NEW.result_id;
    IF canonical_size IS NULL OR result_size IS NULL OR canonical_size <> result_size
       OR result_version IS DISTINCT FROM NEW.ffprobe_version
       OR result_policy IS DISTINCT FROM NEW.analysis_policy_version THEN
        RAISE EXCEPTION 'probe cache association does not match digest size, version and policy';
    END IF;
    RETURN NEW;
END;
$$;

-- table: media_fingerprint_result
CREATE TABLE public.media_fingerprint_result (
    id uuid NOT NULL,
    fpcalc_version text NOT NULL,
    version_banner text NOT NULL,
    algorithm_namespace text NOT NULL,
    algorithm_id smallint NOT NULL,
    fingerprint text NOT NULL,
    reported_duration double precision NOT NULL,
    calculated_at timestamp with time zone NOT NULL,
    applied_operation_id uuid NOT NULL,
    parser_contract_version integer NOT NULL,
    source_sha256 bytea,
    winning_result_id uuid NOT NULL,
    CONSTRAINT media_fingerprint_result_algorithm_id_check CHECK (((algorithm_id >= 0) AND (algorithm_id <= 255))),
    CONSTRAINT media_fingerprint_result_algorithm_namespace_check CHECK ((algorithm_namespace <> ''::text)),
    CONSTRAINT media_fingerprint_result_fingerprint_check CHECK ((fingerprint <> ''::text)),
    CONSTRAINT media_fingerprint_result_fpcalc_version_check CHECK ((fpcalc_version <> ''::text)),
    CONSTRAINT media_fingerprint_result_parser_contract_version_check CHECK ((parser_contract_version > 0)),
    CONSTRAINT media_fingerprint_result_reported_duration_check CHECK (((reported_duration >= (0)::double precision) AND (reported_duration < 'Infinity'::double precision))),
    CONSTRAINT media_fingerprint_result_source_sha256_length CHECK (((source_sha256 IS NULL) OR (octet_length(source_sha256) = 32))),
    CONSTRAINT media_fingerprint_result_version_banner_check CHECK ((version_banner <> ''::text))
);

-- table: media_metadata_result
CREATE TABLE public.media_metadata_result (
    id uuid NOT NULL,
    source_sha256 bytea,
    observed_tags jsonb NOT NULL,
    provenance jsonb NOT NULL,
    native_matroska jsonb,
    observed_at timestamp with time zone NOT NULL,
    winning_result_id uuid NOT NULL,
    applied_operation_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT media_metadata_result_native_matroska_check CHECK (((native_matroska IS NULL) OR (jsonb_typeof(native_matroska) = 'object'::text))),
    CONSTRAINT media_metadata_result_observed_tags_check CHECK ((jsonb_typeof(observed_tags) = 'object'::text)),
    CONSTRAINT media_metadata_result_provenance_check CHECK ((jsonb_typeof(provenance) = 'object'::text)),
    CONSTRAINT media_metadata_result_sha256_length CHECK (((source_sha256 IS NULL) OR (octet_length(source_sha256) = 32)))
);

-- table: media_probe_cache
CREATE TABLE public.media_probe_cache (
    source_sha256 bytea NOT NULL,
    ffprobe_version text NOT NULL,
    ffprobe_version_sha256 bytea NOT NULL,
    analysis_policy_version integer NOT NULL,
    result_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT media_probe_cache_analysis_policy_version_check CHECK ((analysis_policy_version >= 1)),
    CONSTRAINT media_probe_cache_ffprobe_version_check CHECK ((ffprobe_version <> ''::text))
);

-- table: media_variant
CREATE TABLE public.media_variant (
    id uuid NOT NULL,
    size_bytes bigint NOT NULL,
    analysis_policy_version integer,
    ffprobe_version text,
    ffprobe_json jsonb,
    observed_tags jsonb,
    inspected_at timestamp with time zone,
    applied_operation_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    source_sha256 bytea,
    sha256_calculated_at timestamp with time zone,
    sha256_algorithm text,
    sha256_applied_operation_id uuid,
    audio_stream_count integer,
    CONSTRAINT media_variant_analysis_policy_version_positive CHECK ((analysis_policy_version >= 1)),
    CONSTRAINT media_variant_audio_stream_count_nonnegative CHECK (((audio_stream_count IS NULL) OR (audio_stream_count >= 0))),
    CONSTRAINT media_variant_ffprobe_version_not_empty CHECK ((ffprobe_version <> ''::text)),
    CONSTRAINT media_variant_probe_group_all_or_none CHECK ((((ffprobe_version IS NULL) AND (ffprobe_json IS NULL) AND (analysis_policy_version IS NULL) AND (observed_tags IS NULL) AND (inspected_at IS NULL) AND (applied_operation_id IS NULL)) OR ((ffprobe_version IS NOT NULL) AND (ffprobe_json IS NOT NULL) AND (analysis_policy_version IS NOT NULL) AND (observed_tags IS NOT NULL) AND (inspected_at IS NOT NULL) AND (applied_operation_id IS NOT NULL)))),
    CONSTRAINT media_variant_probe_values_valid CHECK (((ffprobe_version IS NULL) OR ((ffprobe_version <> ''::text) AND (analysis_policy_version >= 1) AND (jsonb_typeof(observed_tags) = 'object'::text)))),
    CONSTRAINT media_variant_sha256_length CHECK (((source_sha256 IS NULL) OR (octet_length(source_sha256) = 32))),
    CONSTRAINT media_variant_sha256_provenance CHECK ((((source_sha256 IS NULL) AND (sha256_calculated_at IS NULL) AND (sha256_algorithm IS NULL) AND (sha256_applied_operation_id IS NULL)) OR ((source_sha256 IS NOT NULL) AND (sha256_calculated_at IS NOT NULL) AND (sha256_algorithm IS NOT NULL) AND (sha256_algorithm <> ''::text) AND (sha256_applied_operation_id IS NOT NULL)))),
    CONSTRAINT media_variant_size_not_negative CHECK ((size_bytes >= 0))
);

-- table: operation_source_work_hold
CREATE TABLE public.operation_source_work_hold (
    operation_id uuid NOT NULL,
    work_id uuid NOT NULL
);

-- table: operation_tool_read_hold
CREATE TABLE public.operation_tool_read_hold (
    operation_id uuid NOT NULL,
    installation_id uuid NOT NULL
);

-- table: source_analysis_artifact
CREATE TABLE public.source_analysis_artifact (
    id uuid NOT NULL,
    work_id uuid NOT NULL,
    relative_output_path text NOT NULL,
    source_size_bytes bigint NOT NULL,
    source_mtime timestamp with time zone NOT NULL,
    owner_operation_id uuid NOT NULL,
    owner_operation_attempt integer NOT NULL,
    owner_job_id bigint NOT NULL,
    state text NOT NULL,
    cleanup_error text,
    cleanup_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    requested_steps text[] DEFAULT '{}'::text[] NOT NULL,
    requested_steps_known boolean DEFAULT false NOT NULL,
    CONSTRAINT source_analysis_artifact_check CHECK ((((state = 'cleanup_failed'::text) AND (cleanup_error IS NOT NULL) AND (cleanup_error <> ''::text) AND (cleanup_at IS NOT NULL)) OR ((state <> 'cleanup_failed'::text) AND (cleanup_error IS NULL) AND (cleanup_at IS NULL)))),
    CONSTRAINT source_analysis_artifact_owner_operation_attempt_check CHECK ((owner_operation_attempt > 0)),
    CONSTRAINT source_analysis_artifact_relative_output_path_check CHECK (((relative_output_path <> ''::text) AND (relative_output_path !~ '(^/|(^|/)\.\.?(/|$))'::text))),
    CONSTRAINT source_analysis_artifact_requested_steps_check CHECK ((requested_steps <@ ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text])),
    CONSTRAINT source_analysis_artifact_requested_steps_order CHECK ((((array_position(requested_steps, 'sha256'::text) IS NULL) OR (array_position(requested_steps, 'probe'::text) IS NULL) OR (array_position(requested_steps, 'sha256'::text) < array_position(requested_steps, 'probe'::text))) AND ((array_position(requested_steps, 'sha256'::text) IS NULL) OR (array_position(requested_steps, 'fingerprint'::text) IS NULL) OR (array_position(requested_steps, 'sha256'::text) < array_position(requested_steps, 'fingerprint'::text))) AND ((array_position(requested_steps, 'sha256'::text) IS NULL) OR (array_position(requested_steps, 'metadata'::text) IS NULL) OR (array_position(requested_steps, 'sha256'::text) < array_position(requested_steps, 'metadata'::text))) AND ((array_position(requested_steps, 'probe'::text) IS NULL) OR (array_position(requested_steps, 'fingerprint'::text) IS NULL) OR (array_position(requested_steps, 'probe'::text) < array_position(requested_steps, 'fingerprint'::text))) AND ((array_position(requested_steps, 'probe'::text) IS NULL) OR (array_position(requested_steps, 'metadata'::text) IS NULL) OR (array_position(requested_steps, 'probe'::text) < array_position(requested_steps, 'metadata'::text))) AND ((array_position(requested_steps, 'fingerprint'::text) IS NULL) OR (array_position(requested_steps, 'metadata'::text) IS NULL) OR (array_position(requested_steps, 'fingerprint'::text) < array_position(requested_steps, 'metadata'::text))))),
    CONSTRAINT source_analysis_artifact_requested_steps_unique CHECK (((cardinality(array_positions(requested_steps, 'sha256'::text)) <= 1) AND (cardinality(array_positions(requested_steps, 'probe'::text)) <= 1) AND (cardinality(array_positions(requested_steps, 'fingerprint'::text)) <= 1) AND (cardinality(array_positions(requested_steps, 'metadata'::text)) <= 1))),
    CONSTRAINT source_analysis_artifact_source_size_bytes_check CHECK ((source_size_bytes >= 0)),
    CONSTRAINT source_analysis_artifact_state_check CHECK ((state = ANY (ARRAY['acquiring'::text, 'ready'::text, 'cleanup_eligible'::text, 'cleanup_failed'::text]))),
    CONSTRAINT source_analysis_artifact_unknown_requested_steps_empty CHECK ((requested_steps_known OR (cardinality(requested_steps) = 0)))
);

-- table: source_analysis_artifact_cleanup_item
CREATE TABLE public.source_analysis_artifact_cleanup_item (
    artifact_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    operation_attempt integer NOT NULL,
    job_id bigint NOT NULL,
    relative_output_path text NOT NULL,
    state text NOT NULL,
    safe_error text,
    claimed_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT source_analysis_artifact_cleanup_ite_relative_output_path_check CHECK ((relative_output_path <> ''::text)),
    CONSTRAINT source_analysis_artifact_cleanup_item_check CHECK ((((state = 'claimed'::text) AND (finished_at IS NULL)) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (finished_at IS NOT NULL)))),
    CONSTRAINT source_analysis_artifact_cleanup_item_check1 CHECK ((((state = 'failed'::text) AND (safe_error IS NOT NULL) AND (safe_error <> ''::text)) OR ((state <> 'failed'::text) AND (safe_error IS NULL)))),
    CONSTRAINT source_analysis_artifact_cleanup_item_job_id_check CHECK ((job_id > 0)),
    CONSTRAINT source_analysis_artifact_cleanup_item_operation_attempt_check CHECK ((operation_attempt > 0)),
    CONSTRAINT source_analysis_artifact_cleanup_item_state_check CHECK ((state = ANY (ARRAY['claimed'::text, 'succeeded'::text, 'failed'::text])))
);

-- table: source_analysis_step
CREATE TABLE public.source_analysis_step (
    work_id uuid NOT NULL,
    step text NOT NULL,
    state text NOT NULL,
    step_attempt integer DEFAULT 0 NOT NULL,
    safe_error text,
    skip_reason text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    execution_operation_id uuid,
    execution_operation_attempt integer,
    execution_job_id bigint,
    last_operation_id uuid,
    success_sha_variant_id uuid,
    success_probe_variant_id uuid,
    success_fingerprint_result_id uuid,
    success_reuse_origin text,
    input_snapshot jsonb,
    success_metadata_result_id uuid,
    CONSTRAINT source_analysis_step_active_has_execution CHECK (((state <> ALL (ARRAY['queued'::text, 'running'::text])) OR (execution_operation_id IS NOT NULL))),
    CONSTRAINT source_analysis_step_active_input_snapshot CHECK (((state <> ALL (ARRAY['queued'::text, 'running'::text])) OR ((input_snapshot IS NOT NULL) AND (jsonb_typeof(input_snapshot) = 'object'::text) AND
CASE
    WHEN (jsonb_typeof(input_snapshot) = 'object'::text) THEN ((((((((input_snapshot - 'schema_version'::text) - 'mode'::text) - 'work_ids'::text) - 'target_work_id'::text) - 'target_step'::text) - 'rerun_target'::text) - 'analysis_policy_version'::text) = '{}'::jsonb)
    ELSE false
END AND (NOT (jsonb_typeof((input_snapshot -> 'schema_version'::text)) IS DISTINCT FROM 'number'::text)) AND (NOT ((input_snapshot ->> 'schema_version'::text) IS DISTINCT FROM '1'::text)) AND (NOT ((input_snapshot ->> 'mode'::text) IS DISTINCT FROM 'single_step'::text)) AND (NOT (jsonb_typeof((input_snapshot -> 'work_ids'::text)) IS DISTINCT FROM 'array'::text)) AND
CASE
    WHEN (jsonb_typeof((input_snapshot -> 'work_ids'::text)) = 'array'::text) THEN (jsonb_array_length((input_snapshot -> 'work_ids'::text)) = 1)
    ELSE false
END AND (NOT (((input_snapshot -> 'work_ids'::text) ->> 0) IS DISTINCT FROM (work_id)::text)) AND (NOT ((input_snapshot ->> 'target_work_id'::text) IS DISTINCT FROM (work_id)::text)) AND (NOT ((input_snapshot ->> 'target_step'::text) IS DISTINCT FROM step)) AND (step = ANY (ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text])) AND (NOT (jsonb_typeof((input_snapshot -> 'rerun_target'::text)) IS DISTINCT FROM 'boolean'::text)) AND (NOT (jsonb_typeof((input_snapshot -> 'analysis_policy_version'::text)) IS DISTINCT FROM 'number'::text)) AND (((input_snapshot ->> 'analysis_policy_version'::text) ~ '^[1-9][0-9]*$'::text) IS TRUE)))),
    CONSTRAINT source_analysis_step_error_shape CHECK ((((state = 'failed'::text) AND (safe_error IS NOT NULL) AND (safe_error <> ''::text)) OR ((state <> 'failed'::text) AND (safe_error IS NULL)))),
    CONSTRAINT source_analysis_step_execution_all_or_none CHECK ((((execution_operation_id IS NULL) AND (execution_operation_attempt IS NULL) AND (execution_job_id IS NULL)) OR ((execution_operation_id IS NOT NULL) AND (execution_operation_attempt IS NOT NULL) AND (execution_operation_attempt > 0) AND (execution_job_id IS NOT NULL)))),
    CONSTRAINT source_analysis_step_result_matches_step CHECK ((((success_sha_variant_id IS NULL) OR (step = 'sha256'::text)) AND ((success_probe_variant_id IS NULL) OR (step = 'probe'::text)) AND ((success_fingerprint_result_id IS NULL) OR (step = 'fingerprint'::text)) AND ((success_metadata_result_id IS NULL) OR (step = 'metadata'::text)))),
    CONSTRAINT source_analysis_step_reuse_origin_shape CHECK (((state <> 'succeeded'::text) OR (success_reuse_origin IS NOT NULL))),
    CONSTRAINT source_analysis_step_skip_shape CHECK ((((state = 'skipped'::text) AND (skip_reason IS NOT NULL) AND (skip_reason <> ''::text)) OR ((state <> 'skipped'::text) AND (skip_reason IS NULL)))),
    CONSTRAINT source_analysis_step_state_check CHECK ((state = ANY (ARRAY['not_requested'::text, 'pending'::text, 'queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'skipped'::text]))),
    CONSTRAINT source_analysis_step_step_attempt_check CHECK ((step_attempt >= 0)),
    CONSTRAINT source_analysis_step_step_check CHECK ((step = ANY (ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text]))),
    CONSTRAINT source_analysis_step_success_has_result CHECK (((state <> 'succeeded'::text) OR ((step = 'sha256'::text) AND (success_sha_variant_id IS NOT NULL)) OR ((step = 'probe'::text) AND (success_probe_variant_id IS NOT NULL)) OR ((step = 'fingerprint'::text) AND (success_fingerprint_result_id IS NOT NULL)) OR ((step = 'metadata'::text) AND (success_metadata_result_id IS NOT NULL)))),
    CONSTRAINT source_analysis_step_success_reuse_origin_check CHECK (((success_reuse_origin IS NULL) OR (success_reuse_origin = ANY (ARRAY['executed'::text, 'sha256'::text, 'work'::text]))))
);

-- table: source_analysis_work
CREATE TABLE public.source_analysis_work (
    id uuid NOT NULL,
    location_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    sha256_enabled boolean NOT NULL,
    origin_scan_operation_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    current_location_id uuid,
    CONSTRAINT source_analysis_work_current_location_origin_check CHECK (((current_location_id IS NULL) OR (current_location_id = location_id))),
    CONSTRAINT source_analysis_work_relative_path_check CHECK ((relative_path <> ''::text)),
    CONSTRAINT source_analysis_work_size_bytes_check CHECK ((size_bytes >= 0))
);

-- table: source_analysis_work_artifact_binding
CREATE TABLE public.source_analysis_work_artifact_binding (
    work_id uuid NOT NULL,
    artifact_id uuid NOT NULL,
    borrower_operation_id uuid,
    borrower_operation_attempt integer,
    borrower_job_id bigint,
    requested_steps text[] DEFAULT '{}'::text[] NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT source_analysis_work_artifact_binding_borrower_shape CHECK ((((borrower_operation_id IS NULL) AND (borrower_operation_attempt IS NULL) AND (borrower_job_id IS NULL)) OR ((borrower_operation_id IS NOT NULL) AND (borrower_operation_attempt IS NOT NULL) AND (borrower_operation_attempt > 0) AND (borrower_job_id IS NOT NULL) AND (borrower_job_id > 0)))),
    CONSTRAINT source_analysis_work_artifact_binding_requested_steps CHECK ((requested_steps <@ ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text]))
);

-- table: source_analysis_work_execution
CREATE TABLE public.source_analysis_work_execution (
    work_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    operation_attempt integer NOT NULL,
    job_id bigint NOT NULL,
    processing_mode text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT source_analysis_work_execution_job_id_check CHECK ((job_id > 0)),
    CONSTRAINT source_analysis_work_execution_operation_attempt_check CHECK ((operation_attempt > 0)),
    CONSTRAINT source_analysis_work_execution_processing_mode_check CHECK ((processing_mode = ANY (ARRAY['in_place'::text, 'staged'::text])))
);

-- table: source_location
CREATE TABLE public.source_location (
    id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    last_seen_scan_generation bigint NOT NULL,
    probe_status text NOT NULL,
    safe_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    media_variant_id uuid,
    CONSTRAINT source_location_generation_positive CHECK ((last_seen_scan_generation > 0)),
    CONSTRAINT source_location_probe_error_has_error CHECK (((probe_status <> 'probe_error'::text) OR (safe_error IS NOT NULL))),
    CONSTRAINT source_location_probe_status_allowed CHECK ((probe_status = ANY (ARRAY['not_analyzed'::text, 'audio'::text, 'no_audio'::text, 'probe_error'::text]))),
    CONSTRAINT source_location_probe_status_has_no_error CHECK (((probe_status = 'probe_error'::text) OR (safe_error IS NULL))),
    CONSTRAINT source_location_relative_path_not_empty CHECK ((relative_path <> ''::text)),
    CONSTRAINT source_location_size_not_negative CHECK ((size_bytes >= 0))
);

-- table: source_root
CREATE TABLE public.source_root (
    id uuid NOT NULL,
    configured_path text NOT NULL,
    display_name text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    scan_generation bigint DEFAULT 0 NOT NULL,
    inventory_path text,
    last_successful_scan_at timestamp with time zone,
    last_applied_operation_id uuid,
    status text DEFAULT 'unknown'::text NOT NULL,
    safe_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    processing_mode text DEFAULT 'in_place'::text NOT NULL,
    CONSTRAINT source_root_display_name_not_empty CHECK ((display_name <> ''::text)),
    CONSTRAINT source_root_inventory_path_matches_generation CHECK (((inventory_path IS NULL) OR (scan_generation > 0))),
    CONSTRAINT source_root_processing_mode_check CHECK ((processing_mode = ANY (ARRAY['in_place'::text, 'staged'::text]))),
    CONSTRAINT source_root_scan_generation_not_negative CHECK ((scan_generation >= 0)),
    CONSTRAINT source_root_status_allowed CHECK ((status = ANY (ARRAY['unknown'::text, 'available'::text, 'unavailable'::text]))),
    CONSTRAINT source_root_unavailable_has_error CHECK (((status <> 'unavailable'::text) OR (safe_error IS NOT NULL)))
);

-- table: source_scan_candidate
CREATE TABLE public.source_scan_candidate (
    id uuid NOT NULL,
    operation_id uuid NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    probe_status text NOT NULL,
    safe_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    source_sha256 bytea,
    sha256_calculated_at timestamp with time zone,
    sha256_applied_operation_id uuid,
    audio_stream_count integer,
    ffprobe_version text,
    ffprobe_json jsonb,
    analysis_policy_version integer,
    observed_tags jsonb,
    inspected_at timestamp with time zone,
    probe_applied_operation_id uuid,
    prepared_analysis jsonb,
    CONSTRAINT source_scan_candidate_probe_error_has_error CHECK (((probe_status <> 'probe_error'::text) OR (safe_error IS NOT NULL))),
    CONSTRAINT source_scan_candidate_probe_group_all_or_none CHECK ((((ffprobe_version IS NULL) AND (ffprobe_json IS NULL) AND (analysis_policy_version IS NULL) AND (observed_tags IS NULL) AND (inspected_at IS NULL) AND (probe_applied_operation_id IS NULL)) OR ((ffprobe_version IS NOT NULL) AND (ffprobe_json IS NOT NULL) AND (analysis_policy_version IS NOT NULL) AND (analysis_policy_version >= 1) AND (observed_tags IS NOT NULL) AND (inspected_at IS NOT NULL) AND (probe_applied_operation_id IS NOT NULL)))),
    CONSTRAINT source_scan_candidate_probe_status_allowed CHECK ((probe_status = ANY (ARRAY['not_analyzed'::text, 'audio'::text, 'no_audio'::text, 'probe_error'::text]))),
    CONSTRAINT source_scan_candidate_probe_status_has_no_error CHECK (((probe_status = 'probe_error'::text) OR (safe_error IS NULL))),
    CONSTRAINT source_scan_candidate_relative_path_not_empty CHECK ((relative_path <> ''::text)),
    CONSTRAINT source_scan_candidate_sha256_length CHECK (((source_sha256 IS NULL) OR (octet_length(source_sha256) = 32))),
    CONSTRAINT source_scan_candidate_sha256_provenance CHECK ((((source_sha256 IS NULL) AND (sha256_calculated_at IS NULL) AND (sha256_applied_operation_id IS NULL)) OR ((source_sha256 IS NOT NULL) AND (sha256_calculated_at IS NOT NULL) AND (sha256_applied_operation_id IS NOT NULL)))),
    CONSTRAINT source_scan_candidate_size_not_negative CHECK ((size_bytes >= 0))
);

-- table: source_work_variant_identity
CREATE TABLE public.source_work_variant_identity (
    work_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

-- constraint: media_fingerprint_result media_fingerprint_result_pkey
ALTER TABLE ONLY public.media_fingerprint_result
    ADD CONSTRAINT media_fingerprint_result_pkey PRIMARY KEY (id);

-- constraint: media_fingerprint_result media_fingerprint_result_source_sha256_unique
ALTER TABLE ONLY public.media_fingerprint_result
    ADD CONSTRAINT media_fingerprint_result_source_sha256_unique UNIQUE (source_sha256);

-- constraint: media_metadata_result media_metadata_result_pkey
ALTER TABLE ONLY public.media_metadata_result
    ADD CONSTRAINT media_metadata_result_pkey PRIMARY KEY (id);

-- constraint: media_probe_cache media_probe_cache_pkey
ALTER TABLE ONLY public.media_probe_cache
    ADD CONSTRAINT media_probe_cache_pkey PRIMARY KEY (source_sha256, ffprobe_version_sha256, analysis_policy_version);

-- constraint: media_variant media_variant_pkey
ALTER TABLE ONLY public.media_variant
    ADD CONSTRAINT media_variant_pkey PRIMARY KEY (id);

-- constraint: media_variant media_variant_source_sha256_unique
ALTER TABLE ONLY public.media_variant
    ADD CONSTRAINT media_variant_source_sha256_unique UNIQUE (source_sha256);

-- constraint: operation_source_work_hold operation_source_work_hold_pkey
ALTER TABLE ONLY public.operation_source_work_hold
    ADD CONSTRAINT operation_source_work_hold_pkey PRIMARY KEY (operation_id, work_id);

-- constraint: operation_tool_read_hold operation_tool_read_hold_pkey
ALTER TABLE ONLY public.operation_tool_read_hold
    ADD CONSTRAINT operation_tool_read_hold_pkey PRIMARY KEY (operation_id, installation_id);

-- constraint: source_analysis_artifact source_analysis_artifact_pkey
ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_pkey PRIMARY KEY (id);

-- constraint: source_analysis_artifact source_analysis_artifact_relative_output_path_key
ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_relative_output_path_key UNIQUE (relative_output_path);

-- constraint: source_analysis_artifact source_analysis_artifact_work_id_id_key
ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_work_id_id_key UNIQUE (work_id, id);

-- constraint: source_analysis_artifact_cleanup_item source_analysis_artifact_cleanup_item_pkey
ALTER TABLE ONLY public.source_analysis_artifact_cleanup_item
    ADD CONSTRAINT source_analysis_artifact_cleanup_item_pkey PRIMARY KEY (artifact_id, operation_id, operation_attempt, job_id);

-- constraint: source_analysis_step source_analysis_step_pkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_pkey PRIMARY KEY (work_id, step);

-- constraint: source_analysis_work source_analysis_work_pkey
ALTER TABLE ONLY public.source_analysis_work
    ADD CONSTRAINT source_analysis_work_pkey PRIMARY KEY (id);

-- constraint: source_analysis_work_artifact_binding source_analysis_work_artifact_binding_artifact_id_key
ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_artifact_id_key UNIQUE (artifact_id);

-- constraint: source_analysis_work_artifact_binding source_analysis_work_artifact_binding_pkey
ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_pkey PRIMARY KEY (work_id);

-- constraint: source_analysis_work_execution source_analysis_work_execution_pkey
ALTER TABLE ONLY public.source_analysis_work_execution
    ADD CONSTRAINT source_analysis_work_execution_pkey PRIMARY KEY (work_id, operation_id, operation_attempt, job_id);

-- constraint: source_location source_location_id_root_unique
ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_id_root_unique UNIQUE (id, source_root_id);

-- constraint: source_location source_location_pkey
ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_pkey PRIMARY KEY (id);

-- constraint: source_location source_location_root_path_unique
ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_root_path_unique UNIQUE (source_root_id, relative_path);

-- constraint: source_root source_root_configured_path_unique
ALTER TABLE ONLY public.source_root
    ADD CONSTRAINT source_root_configured_path_unique UNIQUE (configured_path);

-- constraint: source_root source_root_pkey
ALTER TABLE ONLY public.source_root
    ADD CONSTRAINT source_root_pkey PRIMARY KEY (id);

-- constraint: source_scan_candidate source_scan_candidate_operation_path_unique
ALTER TABLE ONLY public.source_scan_candidate
    ADD CONSTRAINT source_scan_candidate_operation_path_unique UNIQUE (operation_id, relative_path);

-- constraint: source_scan_candidate source_scan_candidate_pkey
ALTER TABLE ONLY public.source_scan_candidate
    ADD CONSTRAINT source_scan_candidate_pkey PRIMARY KEY (id);

-- constraint: source_work_variant_identity source_work_variant_identity_pkey
ALTER TABLE ONLY public.source_work_variant_identity
    ADD CONSTRAINT source_work_variant_identity_pkey PRIMARY KEY (work_id);

-- index: media_metadata_result_current_sha256_idx
CREATE UNIQUE INDEX media_metadata_result_current_sha256_idx ON public.media_metadata_result USING btree (source_sha256) WHERE (source_sha256 IS NOT NULL);

-- index: operation_source_work_hold_one_active_work
CREATE UNIQUE INDEX operation_source_work_hold_one_active_work ON public.operation_source_work_hold USING btree (work_id);

-- index: source_analysis_artifact_cleanup_idx
CREATE INDEX source_analysis_artifact_cleanup_idx ON public.source_analysis_artifact USING btree (state) WHERE (state = ANY (ARRAY['cleanup_eligible'::text, 'cleanup_failed'::text]));

-- index: source_analysis_artifact_cleanup_one_claim_idx
CREATE UNIQUE INDEX source_analysis_artifact_cleanup_one_claim_idx ON public.source_analysis_artifact_cleanup_item USING btree (artifact_id) WHERE (state = 'claimed'::text);

-- index: source_analysis_artifact_cleanup_operation_idx
CREATE INDEX source_analysis_artifact_cleanup_operation_idx ON public.source_analysis_artifact_cleanup_item USING btree (operation_id, operation_attempt, job_id);

-- index: source_analysis_artifact_work_idx
CREATE INDEX source_analysis_artifact_work_idx ON public.source_analysis_artifact USING btree (work_id);

-- index: source_analysis_step_pending_idx
CREATE INDEX source_analysis_step_pending_idx ON public.source_analysis_step USING btree (work_id) WHERE (state = 'pending'::text);

-- index: source_analysis_work_current_location_id_key
CREATE UNIQUE INDEX source_analysis_work_current_location_id_key ON public.source_analysis_work USING btree (current_location_id) WHERE (current_location_id IS NOT NULL);

-- index: source_location_media_variant_idx
CREATE INDEX source_location_media_variant_idx ON public.source_location USING btree (media_variant_id);

-- index: source_location_root_id_idx
CREATE INDEX source_location_root_id_idx ON public.source_location USING btree (source_root_id);

-- index: source_scan_candidate_operation_idx
CREATE INDEX source_scan_candidate_operation_idx ON public.source_scan_candidate USING btree (operation_id);

-- index: source_work_variant_identity_media_variant_id_idx
CREATE INDEX source_work_variant_identity_media_variant_id_idx ON public.source_work_variant_identity USING btree (media_variant_id);

-- fk constraint: media_fingerprint_result media_fingerprint_result_source_sha256_fkey
ALTER TABLE ONLY public.media_fingerprint_result
    ADD CONSTRAINT media_fingerprint_result_source_sha256_fkey FOREIGN KEY (source_sha256) REFERENCES public.media_variant(source_sha256) ON DELETE RESTRICT;

-- fk constraint: media_probe_cache media_probe_cache_result_id_fkey
ALTER TABLE ONLY public.media_probe_cache
    ADD CONSTRAINT media_probe_cache_result_id_fkey FOREIGN KEY (result_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

-- fk constraint: media_probe_cache media_probe_cache_source_sha256_fkey
ALTER TABLE ONLY public.media_probe_cache
    ADD CONSTRAINT media_probe_cache_source_sha256_fkey FOREIGN KEY (source_sha256) REFERENCES public.media_variant(source_sha256) ON DELETE RESTRICT;

-- fk constraint: operation operation_target_source_location_id_fkey
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_source_location_id_fkey FOREIGN KEY (target_source_location_id) REFERENCES public.source_location(id) ON DELETE SET NULL;

-- fk constraint: operation operation_target_source_root_id_fkey
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_source_root_id_fkey FOREIGN KEY (target_source_root_id) REFERENCES public.source_root(id) ON DELETE SET NULL;

-- fk constraint: operation operation_target_work_id_fkey
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_work_id_fkey FOREIGN KEY (target_work_id) REFERENCES public.source_analysis_work(id) ON DELETE SET NULL;

-- fk constraint: operation_source_work_hold operation_source_work_hold_operation_id_fkey
ALTER TABLE ONLY public.operation_source_work_hold
    ADD CONSTRAINT operation_source_work_hold_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: operation_source_work_hold operation_source_work_hold_work_id_fkey
ALTER TABLE ONLY public.operation_source_work_hold
    ADD CONSTRAINT operation_source_work_hold_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE RESTRICT;

-- fk constraint: operation_tool_read_hold operation_tool_read_hold_installation_id_fkey
ALTER TABLE ONLY public.operation_tool_read_hold
    ADD CONSTRAINT operation_tool_read_hold_installation_id_fkey FOREIGN KEY (installation_id) REFERENCES public.tool_installation(id) ON DELETE RESTRICT;

-- fk constraint: operation_tool_read_hold operation_tool_read_hold_operation_id_fkey
ALTER TABLE ONLY public.operation_tool_read_hold
    ADD CONSTRAINT operation_tool_read_hold_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_artifact source_analysis_artifact_execution_fkey
ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_execution_fkey FOREIGN KEY (work_id, owner_operation_id, owner_operation_attempt, owner_job_id) REFERENCES public.source_analysis_work_execution(work_id, operation_id, operation_attempt, job_id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_artifact source_analysis_artifact_work_id_fkey
ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_step source_analysis_step_execution_operation_id_execution_oper_fkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_execution_operation_id_execution_oper_fkey FOREIGN KEY (execution_operation_id, execution_operation_attempt, execution_job_id) REFERENCES public.operation(id, attempt, river_job_id) DEFERRABLE INITIALLY DEFERRED;

-- fk constraint: source_analysis_step source_analysis_step_success_fingerprint_result_id_fkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_fingerprint_result_id_fkey FOREIGN KEY (success_fingerprint_result_id) REFERENCES public.media_fingerprint_result(id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_step source_analysis_step_success_metadata_result_id_fkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_metadata_result_id_fkey FOREIGN KEY (success_metadata_result_id) REFERENCES public.media_metadata_result(id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_step source_analysis_step_success_probe_variant_id_fkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_probe_variant_id_fkey FOREIGN KEY (success_probe_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_step source_analysis_step_success_sha_variant_id_fkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_sha_variant_id_fkey FOREIGN KEY (success_sha_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_step source_analysis_step_work_hold_fkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_work_hold_fkey FOREIGN KEY (execution_operation_id, work_id) REFERENCES public.operation_source_work_hold(operation_id, work_id) DEFERRABLE INITIALLY DEFERRED;

-- fk constraint: source_analysis_step source_analysis_step_work_id_fkey
ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

-- fk constraint: source_analysis_work source_analysis_work_current_location_fk
ALTER TABLE ONLY public.source_analysis_work
    ADD CONSTRAINT source_analysis_work_current_location_fk FOREIGN KEY (current_location_id, source_root_id) REFERENCES public.source_location(id, source_root_id) ON DELETE SET NULL (current_location_id);

-- fk constraint: source_analysis_work_artifact_binding source_analysis_work_artifact_binding_artifact_fk
ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_artifact_fk FOREIGN KEY (work_id, artifact_id) REFERENCES public.source_analysis_artifact(work_id, id) ON DELETE CASCADE;

-- fk constraint: source_analysis_work_artifact_binding source_analysis_work_artifact_binding_execution_fk
ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_execution_fk FOREIGN KEY (work_id, borrower_operation_id, borrower_operation_attempt, borrower_job_id) REFERENCES public.source_analysis_work_execution(work_id, operation_id, operation_attempt, job_id) ON DELETE SET NULL (borrower_operation_id, borrower_operation_attempt, borrower_job_id);

-- fk constraint: source_analysis_work_artifact_binding source_analysis_work_artifact_binding_work_id_fkey
ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

-- fk constraint: source_analysis_work_execution source_analysis_work_execution_operation_id_fkey
ALTER TABLE ONLY public.source_analysis_work_execution
    ADD CONSTRAINT source_analysis_work_execution_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: source_analysis_work_execution source_analysis_work_execution_work_id_fkey
ALTER TABLE ONLY public.source_analysis_work_execution
    ADD CONSTRAINT source_analysis_work_execution_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

-- fk constraint: source_location source_location_media_variant_id_fkey
ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_media_variant_id_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

-- fk constraint: source_location source_location_source_root_id_fkey
ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_source_root_id_fkey FOREIGN KEY (source_root_id) REFERENCES public.source_root(id) ON DELETE CASCADE;

-- fk constraint: source_scan_candidate source_scan_candidate_operation_id_fkey
ALTER TABLE ONLY public.source_scan_candidate
    ADD CONSTRAINT source_scan_candidate_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE CASCADE;

-- fk constraint: source_work_variant_identity source_work_variant_identity_media_variant_id_fkey
ALTER TABLE ONLY public.source_work_variant_identity
    ADD CONSTRAINT source_work_variant_identity_media_variant_id_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

-- fk constraint: source_work_variant_identity source_work_variant_identity_work_id_fkey
ALTER TABLE ONLY public.source_work_variant_identity
    ADD CONSTRAINT source_work_variant_identity_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

-- trigger: media_fingerprint_result media_fingerprint_result_identity_immutable
CREATE TRIGGER media_fingerprint_result_identity_immutable BEFORE UPDATE ON public.media_fingerprint_result FOR EACH ROW EXECUTE FUNCTION public.source_fingerprint_result_identity_immutable();

-- trigger: media_probe_cache media_probe_cache_association_valid
CREATE TRIGGER media_probe_cache_association_valid BEFORE INSERT OR UPDATE ON public.media_probe_cache FOR EACH ROW EXECUTE FUNCTION public.validate_media_probe_cache_association();

-- trigger: operation operation_source_analysis_holds_guard
CREATE CONSTRAINT TRIGGER operation_source_analysis_holds_guard AFTER INSERT OR UPDATE ON public.operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_operation_holds();

-- trigger: operation_source_work_hold operation_source_work_hold_guard
CREATE CONSTRAINT TRIGGER operation_source_work_hold_guard AFTER INSERT OR DELETE OR UPDATE ON public.operation_source_work_hold DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_operation_holds();

-- trigger: operation_source_work_hold source_analysis_work_hold_requires_current_attachment
CREATE TRIGGER source_analysis_work_hold_requires_current_attachment BEFORE INSERT OR UPDATE ON public.operation_source_work_hold FOR EACH ROW EXECUTE FUNCTION public.source_analysis_work_hold_requires_current_attachment();

-- trigger: operation_tool_read_hold operation_tool_read_hold_guard
CREATE CONSTRAINT TRIGGER operation_tool_read_hold_guard AFTER INSERT OR DELETE OR UPDATE ON public.operation_tool_read_hold DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_operation_holds();

-- trigger: source_analysis_step source_analysis_step_membership_guard
CREATE CONSTRAINT TRIGGER source_analysis_step_membership_guard AFTER INSERT OR DELETE OR UPDATE ON public.source_analysis_step DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_step_membership();

-- trigger: source_analysis_work source_analysis_work_attachment_guard
CREATE TRIGGER source_analysis_work_attachment_guard BEFORE INSERT OR UPDATE OF current_location_id ON public.source_analysis_work FOR EACH ROW EXECUTE FUNCTION public.source_analysis_work_attachment_guard();

-- trigger: source_analysis_work_execution source_analysis_work_execution_immutable
CREATE TRIGGER source_analysis_work_execution_immutable BEFORE UPDATE ON public.source_analysis_work_execution FOR EACH ROW EXECUTE FUNCTION public.prevent_source_analysis_work_execution_mutation();

-- trigger: source_work_variant_identity source_work_variant_identity_no_rekey
CREATE TRIGGER source_work_variant_identity_no_rekey BEFORE UPDATE ON public.source_work_variant_identity FOR EACH ROW EXECUTE FUNCTION public.prevent_source_work_variant_identity_rekey();
