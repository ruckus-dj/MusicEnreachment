-- Fresh-instance baseline: incoming matching.
-- Later baselines attach cross-domain foreign keys and guards where necessary.
SET LOCAL check_function_bodies = false;

-- function: matching_run_retirement_is_authorized(uuid)
CREATE FUNCTION public.matching_run_retirement_is_authorized(target_run uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    SELECT EXISTS (SELECT 1 FROM matching_run_retirement_authorization
                    WHERE run_id = target_run AND transaction_id = pg_current_xact_id())
$$;

-- function: protect_matching_run_quiescence_timestamp()
CREATE FUNCTION public.protect_matching_run_quiescence_timestamp() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.quiesced_at IS NOT NULL THEN
            RAISE EXCEPTION 'matching run cannot be inserted already quiesced';
        END IF;
        RETURN NEW;
    END IF;
    IF OLD.quiesced_at IS NOT NULL AND NEW.quiesced_at IS DISTINCT FROM OLD.quiesced_at THEN
        RAISE EXCEPTION 'matching run quiescence timestamp is write-once';
    END IF;
    IF OLD.quiesced_at IS NULL AND NEW.quiesced_at IS NOT NULL AND
       (NEW.active_river_job_id IS NOT NULL OR NEW.active_river_attempt IS NOT NULL OR NEW.active_execution_epoch IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot quiesce a matching run with an active owner';
    END IF;
    IF OLD.quiesced_at IS NULL AND NEW.quiesced_at IS NOT NULL AND NEW.quiesced_at <> transaction_timestamp() THEN
        RAISE EXCEPTION 'matching run quiescence timestamp must be database-authored';
    END IF;
    RETURN NEW;
END;
$$;

-- function: reject_frozen_matching_apply_request_mutation()
CREATE FUNCTION public.reject_frozen_matching_apply_request_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.schema_version = 2 AND matching_run_retirement_is_authorized(OLD.parent_run_id) THEN RETURN OLD; END IF;
    IF OLD.schema_version = 2 THEN RAISE EXCEPTION 'frozen matching apply requests are immutable'; END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

-- function: reject_manual_matching_receipt_mutation()
CREATE FUNCTION public.reject_manual_matching_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'manual matching receipts are immutable';
END;
$$;

-- function: reject_matching_acquisition_phase_mutation()
CREATE FUNCTION public.reject_matching_acquisition_phase_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching acquisition phase records are append-only';
END;
$$;

-- function: reject_matching_acquisition_seal_mutation()
CREATE FUNCTION public.reject_matching_acquisition_seal_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching acquisition seals are write-once';
END;
$$;

-- function: reject_matching_apply_receipt_mutation()
CREATE FUNCTION public.reject_matching_apply_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(
        (SELECT parent_run_id FROM matching_apply_request WHERE apply_operation_id = OLD.apply_operation_id)) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching apply receipts are immutable';
END;
$$;

-- function: reject_matching_fallback_authority_seal_mutation()
CREATE FUNCTION public.reject_matching_fallback_authority_seal_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback authority seals are write-once';
END;
$$;

-- function: reject_matching_fallback_calculation_receipt_mutation()
CREATE FUNCTION public.reject_matching_fallback_calculation_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback calculation receipts are immutable';
END;
$$;

-- function: reject_matching_fallback_evaluation_handoff_mutation()
CREATE FUNCTION public.reject_matching_fallback_evaluation_handoff_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback evaluation handoffs are write-once';
END;
$$;

-- function: reject_matching_fallback_lookup_receipt_mutation()
CREATE FUNCTION public.reject_matching_fallback_lookup_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback lookup receipts are immutable';
END;
$$;

-- function: reject_matching_fallback_request_registration_mutation()
CREATE FUNCTION public.reject_matching_fallback_request_registration_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback request registrations are immutable';
END;
$$;

-- function: reject_matching_fallback_search_receipt_mutation()
CREATE FUNCTION public.reject_matching_fallback_search_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback search receipts are immutable';
END;
$$;

-- function: reject_matching_run_retirement_authorization_mutation()
CREATE FUNCTION public.reject_matching_run_retirement_authorization_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.transaction_id = pg_current_xact_id() THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching run retirement authorizations are write-once';
END;
$$;

-- function: validate_matching_apply_receipt_input_binding()
CREATE FUNCTION public.validate_matching_apply_receipt_input_binding() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE request_schema integer;
BEGIN
    SELECT schema_version INTO request_schema
    FROM matching_apply_request WHERE apply_operation_id = NEW.apply_operation_id;
    IF request_schema = 2 AND (NEW.input_sha256 IS NULL OR octet_length(NEW.input_sha256) <> 32) THEN
        RAISE EXCEPTION 'schema-2 matching apply receipt requires its input digest';
    END IF;
    IF request_schema = 1 AND NEW.input_sha256 IS NOT NULL THEN
        RAISE EXCEPTION 'schema-1 matching apply receipt cannot carry an input digest';
    END IF;
    RETURN NEW;
END;
$$;

-- function: validate_matching_run_retirement_authorization()
CREATE FUNCTION public.validate_matching_run_retirement_authorization() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    operation_row operation%ROWTYPE;
    acquisition_version integer;
BEGIN
    PERFORM pg_advisory_xact_lock_shared(1297371734, 3);
    PERFORM pg_advisory_xact_lock(1297371734, 4);
    PERFORM pg_advisory_xact_lock_shared(hashtext('musicbrainz-config'));
    IF NEW.transaction_id <> pg_current_xact_id() THEN
        RAISE EXCEPTION 'matching run retirement authorization must belong to this transaction';
    END IF;
    IF NEW.authorized_at <> transaction_timestamp() THEN
        RAISE EXCEPTION 'matching run retirement authorization timestamp must equal transaction_timestamp()';
    END IF;
    IF NEW.cleanup_cutoff > transaction_timestamp() - interval '24 hours' THEN
        RAISE EXCEPTION 'matching run retirement cleanup cutoff exceeds the 24-hour eligibility boundary';
    END IF;
    SELECT operation.* INTO operation_row
      FROM matching_run run JOIN operation ON operation.id = run.operation_id
     WHERE run.id = NEW.run_id FOR UPDATE OF run, operation;
    IF NOT FOUND OR operation_row.kind <> 'matching_evaluation_v1' THEN
        RAISE EXCEPTION 'matching run is not eligible for proof retirement';
    END IF;
    SELECT intent_version INTO acquisition_version FROM matching_acquisition WHERE run_id=NEW.run_id;

    IF NEW.authorization_kind = 'obsolete_quiesced' THEN
        IF acquisition_version IS NULL OR acquisition_version NOT IN (2,3)
           OR operation_row.state NOT IN ('queued','running','failed')
           OR NOT EXISTS (SELECT 1 FROM matching_run run WHERE run.id=NEW.run_id
                            AND run.quiesced_at IS NOT NULL AND run.quiesced_at < NEW.cleanup_cutoff
                            AND run.active_river_job_id IS NULL AND run.active_river_attempt IS NULL AND run.active_execution_epoch IS NULL)
           OR NOT EXISTS (SELECT 1 FROM matching_run_target target
                           JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
                           WHERE target.run_id=NEW.run_id AND generation.high_water > target.target_generation
                             AND generation.current_run_id IS DISTINCT FROM NEW.run_id)
           OR EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id=NEW.run_id)
           OR EXISTS (SELECT 1 FROM incoming_match_evaluation evaluation
                       WHERE evaluation.target_run_id=NEW.run_id OR evaluation.frozen_run_id=NEW.run_id)
           OR EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=NEW.run_id AND delivery.consumed_at IS NULL)
           OR EXISTS (SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
                       WHERE delivery.run_id=NEW.run_id
                         AND job.state IN ('available','pending','running','retryable','scheduled'))
           OR EXISTS (SELECT 1 FROM matching_acquisition_seal seal JOIN river_job job ON job.id=seal.river_job_id
                       WHERE seal.run_id=NEW.run_id
                         AND job.state IN ('available','pending','running','retryable','scheduled'))
           OR EXISTS (SELECT 1 FROM matching_apply_request request
                       LEFT JOIN operation child ON child.id=request.apply_operation_id
                       LEFT JOIN river_job job ON job.id=child.river_job_id
                       WHERE request.parent_run_id=NEW.run_id AND
                         (child.id IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                          OR child.finished_at >= NEW.cleanup_cutoff OR child.state IN ('queued','running')
                          OR job.state IN ('available','pending','running','retryable','scheduled')))
           OR EXISTS (SELECT 1 FROM matching_provider_dependency dependency
                       LEFT JOIN operation child ON child.id=dependency.request_id
                       LEFT JOIN river_job job ON job.id=child.river_job_id
                       WHERE dependency.parent_run_id=NEW.run_id AND
                         (dependency.terminal_outcome IS NULL OR child.id IS NULL
                          OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                          OR child.finished_at >= NEW.cleanup_cutoff OR child.state IN ('queued','running')
                          OR job.state IN ('available','pending','running','retryable','scheduled')))
           OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=operation_row.river_job_id
                       AND job.state IN ('available','pending','running','retryable','scheduled'))
           OR EXISTS (SELECT 1 FROM matching_run run JOIN river_job job ON job.id=run.current_river_job_id
                       WHERE run.id=NEW.run_id
                         AND job.state IN ('available','pending','running','retryable','scheduled'))
        THEN
            RAISE EXCEPTION 'quiesced matching run is not eligible for obsolete-proof retirement';
        END IF;
        RETURN NEW;
    END IF;

    IF operation_row.state <> 'succeeded' OR operation_row.finished_at IS NULL
       OR operation_row.finished_at >= NEW.cleanup_cutoff
       OR acquisition_version IS DISTINCT FROM 3
       OR EXISTS (SELECT 1 FROM matching_run run WHERE run.id=NEW.run_id AND run.state <> 'succeeded')
       OR EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id=NEW.run_id)
       OR EXISTS (SELECT 1 FROM incoming_match_evaluation evaluation
                   WHERE evaluation.target_run_id=NEW.run_id OR evaluation.frozen_run_id=NEW.run_id)
       OR EXISTS (SELECT 1 FROM matching_apply_request request JOIN operation child ON child.id=request.apply_operation_id
                   WHERE request.parent_run_id=NEW.run_id AND
                     (child.state <> 'succeeded' OR child.finished_at IS NULL OR child.finished_at >= NEW.cleanup_cutoff
                      OR child.state IN ('queued','running') OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                          AND job.state IN ('available','pending','running','retryable','scheduled'))))
       OR EXISTS (SELECT 1 FROM matching_provider_dependency dependency JOIN operation child ON child.id=dependency.request_id
                   WHERE dependency.parent_run_id=NEW.run_id AND
                     (dependency.terminal_outcome IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                      OR child.finished_at >= NEW.cleanup_cutoff OR child.state IN ('queued','running')
                      OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                                 AND job.state IN ('available','pending','running','retryable','scheduled'))))
       OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=operation_row.river_job_id
                   AND job.state IN ('available','pending','running','retryable','scheduled'))
       OR EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=NEW.run_id AND delivery.consumed_at IS NULL)
    THEN
        RAISE EXCEPTION 'matching run is not eligible for proof retirement';
    END IF;
    RETURN NEW;
END;
$$;

-- sequence: matching_delivery_id_seq
CREATE SEQUENCE public.matching_delivery_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- table: incoming_group
CREATE TABLE public.incoming_group (
    id uuid NOT NULL,
    manual boolean DEFAULT false NOT NULL,
    revision text NOT NULL,
    diagnostics jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_diagnostics_check CHECK ((jsonb_typeof(diagnostics) = 'array'::text))
);

-- table: incoming_group_matching_authority
CREATE TABLE public.incoming_group_matching_authority (
    group_id uuid NOT NULL,
    mode text NOT NULL,
    revision bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_matching_authority_mode_check CHECK ((mode = ANY (ARRAY['automatic'::text, 'manual'::text]))),
    CONSTRAINT incoming_group_matching_authority_revision_check CHECK ((revision >= 0))
);

-- table: incoming_group_member
CREATE TABLE public.incoming_group_member (
    group_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    ready boolean DEFAULT true NOT NULL
);

-- table: incoming_group_member_location
CREATE TABLE public.incoming_group_member_location (
    group_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    work_id uuid NOT NULL,
    location_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    CONSTRAINT incoming_group_member_location_size_bytes_check CHECK ((size_bytes >= 0))
);

-- table: incoming_group_return_automatic_receipt
CREATE TABLE public.incoming_group_return_automatic_receipt (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_return_automatic_receipt_intent_hash_check CHECK ((intent_hash ~ '^[0-9a-f]{64}$'::text))
);

-- table: incoming_group_unlink_receipt
CREATE TABLE public.incoming_group_unlink_receipt (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_unlink_receipt_intent_hash_check CHECK ((length(intent_hash) = 64)),
    CONSTRAINT incoming_group_unlink_receipt_response_check CHECK ((jsonb_typeof(response) = 'object'::text))
);

-- table: incoming_grouping_state
CREATE TABLE public.incoming_grouping_state (
    singleton boolean DEFAULT true NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    needs_refresh boolean DEFAULT true NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_grouping_state_revision_check CHECK ((revision >= 0)),
    CONSTRAINT incoming_grouping_state_singleton_check CHECK (singleton)
);

-- table: incoming_match_candidate
CREATE TABLE public.incoming_match_candidate (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    release_id uuid NOT NULL,
    score double precision NOT NULL,
    evidence jsonb NOT NULL,
    CONSTRAINT incoming_match_candidate_evidence_check CHECK ((jsonb_typeof(evidence) = 'object'::text)),
    CONSTRAINT incoming_match_candidate_score_check CHECK (((score >= (0)::double precision) AND (score <= (1)::double precision)))
);

-- table: incoming_match_candidate_track
CREATE TABLE public.incoming_match_candidate_track (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    track_id uuid NOT NULL,
    medium integer NOT NULL,
    "position" integer NOT NULL,
    CONSTRAINT incoming_match_candidate_track_medium_check CHECK ((medium >= 0)),
    CONSTRAINT incoming_match_candidate_track_position_check CHECK (("position" >= 0))
);

-- table: incoming_match_evaluation
CREATE TABLE public.incoming_match_evaluation (
    group_id uuid NOT NULL,
    group_revision text NOT NULL,
    groups_revision text NOT NULL,
    grouping_epoch bigint NOT NULL,
    policy_version text NOT NULL,
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_configuration_identity text NOT NULL,
    provider_namespace text,
    provider_endpoint text,
    cache_key text,
    cache_revision text,
    provider_snapshot_revision text,
    provider_fences jsonb NOT NULL,
    evaluated_at timestamp with time zone DEFAULT now() NOT NULL,
    evaluation_revision text,
    evaluation_evidence jsonb DEFAULT '{}'::jsonb NOT NULL,
    matching_authority_mode text DEFAULT 'automatic'::text NOT NULL,
    matching_authority_revision bigint DEFAULT 0 NOT NULL,
    fence_version integer DEFAULT 1 NOT NULL,
    target_run_id uuid,
    target_capture jsonb,
    target_capture_digest bytea,
    frozen_authority_version integer,
    frozen_run_id uuid,
    frozen_wait_revision bigint,
    frozen_seal_digest bytea,
    CONSTRAINT incoming_match_evaluation_evaluation_evidence_check CHECK ((jsonb_typeof(evaluation_evidence) = 'object'::text)),
    CONSTRAINT incoming_match_evaluation_fence_version_check CHECK ((fence_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT incoming_match_evaluation_frozen_authority_all_or_none CHECK ((((frozen_authority_version IS NULL) AND (frozen_run_id IS NULL) AND (frozen_wait_revision IS NULL) AND (frozen_seal_digest IS NULL)) OR ((frozen_authority_version IS NOT NULL) AND (frozen_run_id IS NOT NULL) AND (frozen_wait_revision IS NOT NULL) AND (frozen_seal_digest IS NOT NULL) AND (frozen_authority_version = 3) AND (frozen_wait_revision > 0) AND (octet_length(frozen_seal_digest) = 32) AND (target_run_id IS NOT NULL) AND (fence_version = 2) AND (matching_authority_mode = 'automatic'::text)))),
    CONSTRAINT incoming_match_evaluation_frozen_target_run CHECK (((frozen_run_id IS NULL) OR (target_run_id = frozen_run_id))),
    CONSTRAINT incoming_match_evaluation_grouping_epoch_check CHECK ((grouping_epoch >= 0)),
    CONSTRAINT incoming_match_evaluation_matching_authority_mode_check CHECK ((matching_authority_mode = ANY (ARRAY['automatic'::text, 'manual_review'::text]))),
    CONSTRAINT incoming_match_evaluation_matching_authority_revision_check CHECK ((matching_authority_revision >= 0)),
    CONSTRAINT incoming_match_evaluation_policy_version_check CHECK ((length(btrim(policy_version)) > 0)),
    CONSTRAINT incoming_match_evaluation_provider_fences_check CHECK ((jsonb_typeof(provider_fences) = 'object'::text)),
    CONSTRAINT incoming_match_evaluation_provider_snapshot_revision_check CHECK ((length(btrim(provider_snapshot_revision)) > 0)),
    CONSTRAINT incoming_match_evaluation_target_capture_digest_length CHECK (((target_capture_digest IS NULL) OR (octet_length(target_capture_digest) = 32))),
    CONSTRAINT incoming_match_evaluation_target_capture_pair CHECK (((target_capture IS NULL) = (target_capture_digest IS NULL))),
    CONSTRAINT incoming_match_evaluation_target_fence CHECK ((((fence_version = 1) AND (target_run_id IS NULL) AND (target_capture IS NULL) AND (target_capture_digest IS NULL)) OR ((fence_version = 2) AND (target_run_id IS NOT NULL) AND (target_capture IS NOT NULL) AND (target_capture_digest IS NOT NULL))))
);

-- table: incoming_match_evaluation_location
CREATE TABLE public.incoming_match_evaluation_location (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    location_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    CONSTRAINT incoming_match_evaluation_location_size_bytes_check CHECK ((size_bytes >= 0))
);

-- table: incoming_match_evaluation_member
CREATE TABLE public.incoming_match_evaluation_member (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    work_id uuid NOT NULL,
    source_sha256 bytea,
    CONSTRAINT incoming_match_evaluation_member_source_sha256_check CHECK (((source_sha256 IS NULL) OR (octet_length(source_sha256) = 32)))
);

-- table: incoming_matching_automatic_admission
CREATE TABLE public.incoming_matching_automatic_admission (
    fingerprint bytea NOT NULL,
    original_run_id uuid NOT NULL,
    original_operation_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_matching_automatic_admission_fingerprint_check CHECK ((octet_length(fingerprint) = 32))
);

-- table: incoming_matching_generation
CREATE TABLE public.incoming_matching_generation (
    group_id uuid NOT NULL,
    high_water bigint DEFAULT 0 NOT NULL,
    current_run_id uuid,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    requested_generation bigint DEFAULT 0 NOT NULL,
    reconciled_generation bigint DEFAULT 0 NOT NULL,
    CONSTRAINT incoming_matching_generation_high_water_check CHECK ((high_water >= 0)),
    CONSTRAINT incoming_matching_generation_reconciled_generation_check CHECK ((reconciled_generation >= 0)),
    CONSTRAINT incoming_matching_generation_reconciled_lte_requested CHECK ((reconciled_generation <= requested_generation)),
    CONSTRAINT incoming_matching_generation_requested_generation_check CHECK ((requested_generation >= 0))
);

-- table: incoming_matching_reconcile_event
CREATE TABLE public.incoming_matching_reconcile_event (
    id uuid NOT NULL,
    source_operation_id uuid,
    source_attempt integer,
    reason text NOT NULL,
    source_input_snapshot jsonb NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    registered_job_id bigint,
    delivery_epoch bigint DEFAULT 0 NOT NULL,
    highest_delivery_attempt integer DEFAULT 0 NOT NULL,
    delivery_claimed boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    dispatched_at timestamp with time zone,
    completed_at timestamp with time zone,
    CONSTRAINT incoming_matching_reconcile_even_highest_delivery_attempt_check CHECK ((highest_delivery_attempt >= 0)),
    CONSTRAINT incoming_matching_reconcile_event_check CHECK ((((state = 'pending'::text) AND (registered_job_id IS NULL) AND (dispatched_at IS NULL)) OR ((state = ANY (ARRAY['dispatched'::text, 'done'::text])) AND (registered_job_id IS NOT NULL) AND (dispatched_at IS NOT NULL)))),
    CONSTRAINT incoming_matching_reconcile_event_check1 CHECK (((state = 'done'::text) = (completed_at IS NOT NULL))),
    CONSTRAINT incoming_matching_reconcile_event_delivery_epoch_check CHECK ((delivery_epoch >= 0)),
    CONSTRAINT incoming_matching_reconcile_event_origin_check CHECK ((((reason = ANY (ARRAY['analysis_success'::text, 'scan_success'::text])) AND (source_operation_id IS NOT NULL) AND (source_attempt IS NOT NULL) AND (source_attempt > 0)) OR ((reason = ANY (ARRAY['startup'::text, 'current'::text])) AND (source_operation_id IS NULL) AND (source_attempt IS NULL) AND (source_input_snapshot = '{}'::jsonb)))),
    CONSTRAINT incoming_matching_reconcile_event_reason_check CHECK ((reason = ANY (ARRAY['analysis_success'::text, 'scan_success'::text, 'startup'::text, 'current'::text]))),
    CONSTRAINT incoming_matching_reconcile_event_source_attempt_check CHECK ((source_attempt > 0)),
    CONSTRAINT incoming_matching_reconcile_event_source_input_snapshot_check CHECK ((jsonb_typeof(source_input_snapshot) = 'object'::text)),
    CONSTRAINT incoming_matching_reconcile_event_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'dispatched'::text, 'done'::text])))
);

-- table: local_matching_receipt
CREATE TABLE public.local_matching_receipt (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL,
    preview_digest text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT local_matching_receipt_intent_hash_check CHECK ((intent_hash ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT local_matching_receipt_preview_digest_check CHECK ((preview_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT local_matching_receipt_response_check CHECK ((jsonb_typeof(response) = 'object'::text))
);

-- table: manual_matching_admission
CREATE TABLE public.manual_matching_admission (
    request_id uuid NOT NULL,
    run_id uuid NOT NULL,
    group_id uuid NOT NULL,
    group_revision text NOT NULL,
    evaluation_revision text,
    selected_release_mbid uuid NOT NULL,
    lookup_operation_id uuid NOT NULL,
    cache_key text NOT NULL,
    canonical_input_digest bytea NOT NULL,
    plan_digest bytea NOT NULL,
    original_members jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT manual_matching_admission_cache_key_check CHECK ((length(btrim(cache_key)) > 0)),
    CONSTRAINT manual_matching_admission_canonical_input_digest_check CHECK ((octet_length(canonical_input_digest) = 32)),
    CONSTRAINT manual_matching_admission_evaluation_revision_check CHECK (((evaluation_revision IS NULL) OR (length(btrim(evaluation_revision)) > 0))),
    CONSTRAINT manual_matching_admission_group_revision_check CHECK ((length(btrim(group_revision)) > 0)),
    CONSTRAINT manual_matching_admission_original_members_check CHECK ((jsonb_typeof(original_members) = 'array'::text)),
    CONSTRAINT manual_matching_admission_plan_digest_check CHECK ((octet_length(plan_digest) = 32))
);

-- table: manual_matching_lookup_seal
CREATE TABLE public.manual_matching_lookup_seal (
    request_id uuid NOT NULL,
    lookup_operation_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    cache_key text NOT NULL,
    payload_sha256 bytea NOT NULL,
    cache_revision text,
    projection_sha256 bytea NOT NULL,
    sealed_at timestamp with time zone,
    CONSTRAINT manual_matching_lookup_seal_cache_key_check CHECK ((length(btrim(cache_key)) > 0)),
    CONSTRAINT manual_matching_lookup_seal_configuration_identity_check CHECK ((length(btrim(configuration_identity)) > 0)),
    CONSTRAINT manual_matching_lookup_seal_payload_sha256_check CHECK ((octet_length(payload_sha256) = 32)),
    CONSTRAINT manual_matching_lookup_seal_projection_sha256_check CHECK ((octet_length(projection_sha256) = 32))
);

-- table: manual_matching_receipt
CREATE TABLE public.manual_matching_receipt (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    local_release_id uuid NOT NULL,
    intent_hash text NOT NULL,
    preview_digest text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT manual_matching_receipt_intent_hash_check CHECK ((intent_hash <> ''::text)),
    CONSTRAINT manual_matching_receipt_preview_digest_check CHECK ((preview_digest <> ''::text))
);

-- table: manual_picker_search
CREATE TABLE public.manual_picker_search (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    search_kind text NOT NULL,
    canonical_digest bytea NOT NULL,
    query_text text NOT NULL,
    page_offset integer NOT NULL,
    page_limit integer NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    cache_key text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT manual_picker_search_canonical_digest_check CHECK ((octet_length(canonical_digest) = 32)),
    CONSTRAINT manual_picker_search_page_limit_check CHECK ((page_limit > 0)),
    CONSTRAINT manual_picker_search_page_offset_check CHECK ((page_offset >= 0)),
    CONSTRAINT manual_picker_search_search_kind_check CHECK ((search_kind = ANY (ARRAY['artist_search'::text, 'release_search'::text, 'artist_release_browse'::text])))
);

-- table: manual_picker_search_result
CREATE TABLE public.manual_picker_search_result (
    operation_id uuid NOT NULL,
    group_id uuid NOT NULL,
    search_kind text NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    cache_key text NOT NULL,
    payload jsonb NOT NULL,
    payload_sha256 bytea NOT NULL,
    cache_revision text,
    accepted_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT manual_picker_search_result_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text)),
    CONSTRAINT manual_picker_search_result_payload_sha256_check CHECK ((octet_length(payload_sha256) = 32)),
    CONSTRAINT manual_picker_search_result_search_kind_check CHECK ((search_kind = ANY (ARRAY['artist_search'::text, 'release_search'::text, 'artist_release_browse'::text])))
);

-- table: matching_acquisition
CREATE TABLE public.matching_acquisition (
    run_id uuid NOT NULL,
    intent_version integer NOT NULL,
    intent jsonb NOT NULL,
    intent_digest bytea NOT NULL,
    current_phase text NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    CONSTRAINT matching_acquisition_current_phase_check CHECK ((length(btrim(current_phase)) > 0)),
    CONSTRAINT matching_acquisition_intent_check CHECK ((jsonb_typeof(intent) = 'object'::text)),
    CONSTRAINT matching_acquisition_intent_digest_check CHECK ((octet_length(intent_digest) = 32)),
    CONSTRAINT matching_acquisition_intent_version_check CHECK ((intent_version > 0)),
    CONSTRAINT matching_acquisition_revision_check CHECK ((revision >= 0))
);

-- table: matching_acquisition_phase
CREATE TABLE public.matching_acquisition_phase (
    run_id uuid NOT NULL,
    revision bigint NOT NULL,
    phase text NOT NULL,
    input jsonb NOT NULL,
    output jsonb NOT NULL,
    response_provenance jsonb NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_acquisition_phase_input_check CHECK ((jsonb_typeof(input) = 'object'::text)),
    CONSTRAINT matching_acquisition_phase_output_check CHECK ((jsonb_typeof(output) = 'object'::text)),
    CONSTRAINT matching_acquisition_phase_phase_check CHECK ((length(btrim(phase)) > 0)),
    CONSTRAINT matching_acquisition_phase_response_provenance_check CHECK ((jsonb_typeof(response_provenance) = 'array'::text)),
    CONSTRAINT matching_acquisition_phase_revision_check CHECK ((revision >= 0))
);

-- table: matching_acquisition_seal
CREATE TABLE public.matching_acquisition_seal (
    run_id uuid NOT NULL,
    acquisition_revision bigint NOT NULL,
    input jsonb NOT NULL,
    input_digest bytea NOT NULL,
    catalog_digest bytea NOT NULL,
    river_job_id bigint NOT NULL,
    operation_attempt integer NOT NULL,
    sealed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_acquisition_seal_acquisition_revision_check CHECK ((acquisition_revision >= 0)),
    CONSTRAINT matching_acquisition_seal_catalog_digest_check CHECK ((octet_length(catalog_digest) = 32)),
    CONSTRAINT matching_acquisition_seal_input_check CHECK ((jsonb_typeof(input) = 'object'::text)),
    CONSTRAINT matching_acquisition_seal_input_digest_check CHECK ((octet_length(input_digest) = 32)),
    CONSTRAINT matching_acquisition_seal_operation_attempt_check CHECK ((operation_attempt > 0))
);

-- table: matching_apply_receipt
CREATE TABLE public.matching_apply_receipt (
    apply_operation_id uuid NOT NULL,
    group_id uuid NOT NULL,
    outcome text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL,
    input_sha256 bytea,
    CONSTRAINT matching_apply_receipt_input_sha256_length CHECK (((input_sha256 IS NULL) OR (octet_length(input_sha256) = 32))),
    CONSTRAINT matching_apply_receipt_outcome_check CHECK ((outcome = ANY (ARRAY['applied'::text, 'review_required'::text])))
);

-- table: matching_apply_request
CREATE TABLE public.matching_apply_request (
    group_id uuid NOT NULL,
    parent_run_id uuid NOT NULL,
    evaluation_revision text NOT NULL,
    apply_operation_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    schema_version integer DEFAULT 1 NOT NULL,
    input_sha256 bytea,
    frozen_authority_version integer,
    parent_execution_epoch bigint,
    seal_wait_revision bigint,
    seal_digest bytea,
    target_capture_sha256 bytea,
    CONSTRAINT matching_apply_request_evaluation_revision_check CHECK ((evaluation_revision <> ''::text)),
    CONSTRAINT matching_apply_request_schema_version CHECK ((schema_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT matching_apply_request_v2_binding CHECK ((((schema_version = 1) AND (input_sha256 IS NULL) AND (frozen_authority_version IS NULL) AND (parent_execution_epoch IS NULL) AND (seal_wait_revision IS NULL) AND (seal_digest IS NULL) AND (target_capture_sha256 IS NULL)) OR ((schema_version = 2) AND (input_sha256 IS NOT NULL) AND (octet_length(input_sha256) = 32) AND (frozen_authority_version IS NOT NULL) AND (frozen_authority_version = 3) AND (parent_execution_epoch IS NOT NULL) AND (parent_execution_epoch > 0) AND (seal_wait_revision IS NOT NULL) AND (seal_wait_revision > 0) AND (seal_digest IS NOT NULL) AND (octet_length(seal_digest) = 32) AND (target_capture_sha256 IS NOT NULL) AND (octet_length(target_capture_sha256) = 32))))
);

-- table: matching_delivery
CREATE TABLE public.matching_delivery (
    id bigint NOT NULL,
    run_id uuid NOT NULL,
    river_job_id bigint NOT NULL,
    registered_at timestamp with time zone DEFAULT now() NOT NULL,
    consumed_at timestamp with time zone,
    operation_attempt integer NOT NULL,
    CONSTRAINT matching_delivery_attempt_positive CHECK ((operation_attempt > 0))
);

-- table: matching_fallback_authority_seal
CREATE TABLE public.matching_fallback_authority_seal (
    run_id uuid NOT NULL,
    wait_revision bigint NOT NULL,
    schema_version integer NOT NULL,
    authority jsonb NOT NULL,
    authority_digest bytea NOT NULL,
    sealed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_fallback_authority_seal_authority_check CHECK ((jsonb_typeof(authority) = 'object'::text)),
    CONSTRAINT matching_fallback_authority_seal_authority_digest_check CHECK ((octet_length(authority_digest) = 32)),
    CONSTRAINT matching_fallback_authority_seal_schema_version_check CHECK ((schema_version = 3)),
    CONSTRAINT matching_fallback_authority_seal_wait_revision_check CHECK ((wait_revision > 0))
);

-- table: matching_fallback_calculation_receipt
CREATE TABLE public.matching_fallback_calculation_receipt (
    run_id uuid NOT NULL,
    acquisition_revision bigint NOT NULL,
    stage_index integer NOT NULL,
    schema_version integer NOT NULL,
    calculator_version integer NOT NULL,
    digest_version integer NOT NULL,
    input_digest bytea NOT NULL,
    context jsonb NOT NULL,
    edge_calculations jsonb NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_fallback_calculation_receip_acquisition_revision_check CHECK ((acquisition_revision > 0)),
    CONSTRAINT matching_fallback_calculation_receipt_calculator_version_check CHECK (((schema_version = 3) AND (calculator_version = 3) AND (digest_version = 3))),
    CONSTRAINT matching_fallback_calculation_receipt_context_check CHECK ((jsonb_typeof(context) = 'object'::text)),
    CONSTRAINT matching_fallback_calculation_receipt_digest_version_check CHECK (((schema_version = 3) AND (calculator_version = 3) AND (digest_version = 3))),
    CONSTRAINT matching_fallback_calculation_receipt_edge_calculations_check CHECK ((jsonb_typeof(edge_calculations) = 'array'::text)),
    CONSTRAINT matching_fallback_calculation_receipt_input_digest_check CHECK ((octet_length(input_digest) = 32)),
    CONSTRAINT matching_fallback_calculation_receipt_schema_version_check CHECK (((schema_version = 3) AND (calculator_version = 3) AND (digest_version = 3))),
    CONSTRAINT matching_fallback_calculation_receipt_stage_index_check CHECK ((stage_index >= 0))
);

-- table: matching_fallback_evaluation_handoff
CREATE TABLE public.matching_fallback_evaluation_handoff (
    run_id uuid NOT NULL,
    wait_revision bigint NOT NULL,
    provider_authority_version integer NOT NULL,
    seal_digest bytea NOT NULL,
    river_job_id bigint NOT NULL,
    operation_attempt integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_fallback_evaluation_h_provider_authority_version_check CHECK ((provider_authority_version = 3)),
    CONSTRAINT matching_fallback_evaluation_handoff_operation_attempt_check CHECK ((operation_attempt > 0)),
    CONSTRAINT matching_fallback_evaluation_handoff_seal_digest_check CHECK ((octet_length(seal_digest) = 32)),
    CONSTRAINT matching_fallback_evaluation_handoff_wait_revision_check CHECK ((wait_revision > 0))
);

-- table: matching_fallback_lookup_receipt
CREATE TABLE public.matching_fallback_lookup_receipt (
    run_id uuid NOT NULL,
    request_id uuid NOT NULL,
    stage_index integer NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    cache_key text NOT NULL,
    request_input jsonb NOT NULL,
    payload jsonb NOT NULL,
    payload_sha256 bytea NOT NULL,
    accepted_projection jsonb NOT NULL,
    projection_sha256 bytea NOT NULL,
    cache_revision text,
    cache_generation bigint NOT NULL,
    fetch_order bigint NOT NULL,
    execution_epoch bigint NOT NULL,
    provider_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    accepted_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_fallback_lookup_receipt_accepted_projection_check CHECK ((jsonb_typeof(accepted_projection) = 'object'::text)),
    CONSTRAINT matching_fallback_lookup_receipt_cache_generation_check CHECK ((cache_generation > 0)),
    CONSTRAINT matching_fallback_lookup_receipt_cache_key_check CHECK ((length(btrim(cache_key)) > 0)),
    CONSTRAINT matching_fallback_lookup_receipt_configuration_identity_check CHECK ((length(btrim(configuration_identity)) > 0)),
    CONSTRAINT matching_fallback_lookup_receipt_execution_epoch_check CHECK ((execution_epoch > 0)),
    CONSTRAINT matching_fallback_lookup_receipt_fetch_order_check CHECK ((fetch_order > 0)),
    CONSTRAINT matching_fallback_lookup_receipt_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text)),
    CONSTRAINT matching_fallback_lookup_receipt_payload_sha256_check CHECK ((octet_length(payload_sha256) = 32)),
    CONSTRAINT matching_fallback_lookup_receipt_projection_sha256_check CHECK ((octet_length(projection_sha256) = 32)),
    CONSTRAINT matching_fallback_lookup_receipt_provider_attempt_check CHECK ((provider_attempt > 0)),
    CONSTRAINT matching_fallback_lookup_receipt_request_input_check CHECK ((jsonb_typeof(request_input) = 'object'::text)),
    CONSTRAINT matching_fallback_lookup_receipt_river_job_id_check CHECK ((river_job_id > 0)),
    CONSTRAINT matching_fallback_lookup_receipt_stage_index_check CHECK ((stage_index >= 0))
);

-- table: matching_fallback_request_registration
CREATE TABLE public.matching_fallback_request_registration (
    run_id uuid NOT NULL,
    stage_index integer NOT NULL,
    role text NOT NULL,
    request_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    kind text NOT NULL,
    cache_key text NOT NULL,
    canonical_input jsonb NOT NULL,
    input_sha256 bytea NOT NULL,
    registered_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_fallback_request_registra_configuration_identity_check CHECK ((length(btrim(configuration_identity)) > 0)),
    CONSTRAINT matching_fallback_request_registration_cache_key_check CHECK ((length(btrim(cache_key)) > 0)),
    CONSTRAINT matching_fallback_request_registration_canonical_input_check CHECK ((jsonb_typeof(canonical_input) = 'object'::text)),
    CONSTRAINT matching_fallback_request_registration_check CHECK ((((role = 'search'::text) AND (kind = ANY (ARRAY['provider_release_search'::text, 'provider_recording_search'::text]))) OR ((role = 'lookup'::text) AND (kind = 'provider_release_lookup'::text)))),
    CONSTRAINT matching_fallback_request_registration_input_sha256_check CHECK ((octet_length(input_sha256) = 32)),
    CONSTRAINT matching_fallback_request_registration_kind_check CHECK ((kind = ANY (ARRAY['provider_release_search'::text, 'provider_recording_search'::text, 'provider_release_lookup'::text]))),
    CONSTRAINT matching_fallback_request_registration_role_check CHECK ((role = ANY (ARRAY['search'::text, 'lookup'::text]))),
    CONSTRAINT matching_fallback_request_registration_stage_index_check CHECK ((stage_index >= 0))
);

-- table: matching_fallback_search_receipt
CREATE TABLE public.matching_fallback_search_receipt (
    run_id uuid NOT NULL,
    request_id uuid NOT NULL,
    stage_index integer NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    cache_key text NOT NULL,
    request_input jsonb NOT NULL,
    payload jsonb NOT NULL,
    payload_sha256 bytea NOT NULL,
    cache_revision text,
    cache_generation bigint NOT NULL,
    fetch_order bigint NOT NULL,
    execution_epoch bigint NOT NULL,
    provider_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    accepted_at timestamp with time zone DEFAULT now() NOT NULL,
    payload_digest_version smallint DEFAULT 2 NOT NULL,
    CONSTRAINT matching_fallback_search_receipt_cache_generation_check CHECK ((cache_generation > 0)),
    CONSTRAINT matching_fallback_search_receipt_cache_key_check CHECK ((length(btrim(cache_key)) > 0)),
    CONSTRAINT matching_fallback_search_receipt_configuration_identity_check CHECK ((length(btrim(configuration_identity)) > 0)),
    CONSTRAINT matching_fallback_search_receipt_execution_epoch_check CHECK ((execution_epoch > 0)),
    CONSTRAINT matching_fallback_search_receipt_fetch_order_check CHECK ((fetch_order > 0)),
    CONSTRAINT matching_fallback_search_receipt_payload_check CHECK ((jsonb_typeof(payload) = 'object'::text)),
    CONSTRAINT matching_fallback_search_receipt_payload_digest_version_check CHECK ((payload_digest_version = 2)),
    CONSTRAINT matching_fallback_search_receipt_payload_sha256_check CHECK ((octet_length(payload_sha256) = 32)),
    CONSTRAINT matching_fallback_search_receipt_provider_attempt_check CHECK ((provider_attempt > 0)),
    CONSTRAINT matching_fallback_search_receipt_request_input_check CHECK ((jsonb_typeof(request_input) = 'object'::text)),
    CONSTRAINT matching_fallback_search_receipt_river_job_id_check CHECK ((river_job_id > 0)),
    CONSTRAINT matching_fallback_search_receipt_stage_index_check CHECK ((stage_index >= 0))
);

-- table: matching_provider_dependency
CREATE TABLE public.matching_provider_dependency (
    parent_run_id uuid NOT NULL,
    request_id uuid NOT NULL,
    cache_key text NOT NULL,
    terminal_outcome text,
    settled_at timestamp with time zone,
    CONSTRAINT matching_provider_dependency_cache_key_check CHECK ((length(btrim(cache_key)) > 0)),
    CONSTRAINT matching_provider_dependency_check CHECK (((terminal_outcome IS NULL) = (settled_at IS NULL))),
    CONSTRAINT matching_provider_dependency_terminal_outcome_check CHECK (((terminal_outcome IS NULL) OR (terminal_outcome = ANY (ARRAY['succeeded'::text, 'failed'::text]))))
);

-- table: matching_run
CREATE TABLE public.matching_run (
    id uuid NOT NULL,
    operation_id uuid NOT NULL,
    plan_admission_key text NOT NULL,
    plan_digest bytea NOT NULL,
    immutable_plan jsonb NOT NULL,
    grouping_revision bigint NOT NULL,
    grouping_epoch bigint NOT NULL,
    provider_source_id uuid NOT NULL,
    provider_configuration_identity text NOT NULL,
    policy_version text NOT NULL,
    state text NOT NULL,
    execution_epoch bigint DEFAULT 0 NOT NULL,
    current_river_job_id bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    active_river_job_id bigint,
    active_river_attempt integer,
    active_execution_epoch bigint,
    fence_version integer DEFAULT 1 NOT NULL,
    quiesced_at timestamp with time zone,
    CONSTRAINT matching_run_active_claim_complete CHECK ((((active_river_job_id IS NULL) AND (active_river_attempt IS NULL) AND (active_execution_epoch IS NULL)) OR ((active_river_job_id IS NOT NULL) AND (active_river_attempt IS NOT NULL) AND (active_execution_epoch IS NOT NULL)))),
    CONSTRAINT matching_run_execution_epoch_check CHECK ((execution_epoch >= 0)),
    CONSTRAINT matching_run_fence_version_check CHECK ((fence_version = ANY (ARRAY[1, 2]))),
    CONSTRAINT matching_run_grouping_epoch_check CHECK ((grouping_epoch >= 0)),
    CONSTRAINT matching_run_grouping_revision_check CHECK ((grouping_revision >= 0)),
    CONSTRAINT matching_run_immutable_plan_check CHECK ((jsonb_typeof(immutable_plan) = 'object'::text)),
    CONSTRAINT matching_run_plan_admission_key_check CHECK ((length(btrim(plan_admission_key)) > 0)),
    CONSTRAINT matching_run_plan_digest_check CHECK ((octet_length(plan_digest) = 32)),
    CONSTRAINT matching_run_policy_version_check CHECK ((length(btrim(policy_version)) > 0)),
    CONSTRAINT matching_run_provider_configuration_identity_check CHECK ((length(btrim(provider_configuration_identity)) > 0)),
    CONSTRAINT matching_run_quiescence_has_no_active_owner CHECK (((quiesced_at IS NULL) OR ((active_river_job_id IS NULL) AND (active_river_attempt IS NULL) AND (active_execution_epoch IS NULL)))),
    CONSTRAINT matching_run_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'waiting_provider'::text, 'waiting_policy'::text, 'succeeded'::text, 'failed'::text])))
);

-- table: matching_run_retirement_authorization
CREATE TABLE public.matching_run_retirement_authorization (
    run_id uuid NOT NULL,
    transaction_id xid8 DEFAULT pg_current_xact_id() NOT NULL,
    authorized_at timestamp with time zone DEFAULT transaction_timestamp() NOT NULL,
    cleanup_cutoff timestamp with time zone DEFAULT (transaction_timestamp() - '24:00:00'::interval) NOT NULL,
    authorization_kind text DEFAULT 'automatic_fallback'::text NOT NULL,
    CONSTRAINT matching_run_retirement_authorization_authorization_kind_check CHECK ((authorization_kind = ANY (ARRAY['automatic_fallback'::text, 'obsolete_quiesced'::text]))),
    CONSTRAINT matching_run_retirement_authorization_obsolete_v2 CHECK (((authorization_kind <> 'obsolete_quiesced'::text) OR (cleanup_cutoff <= (authorized_at - '24:00:00'::interval))))
);

-- table: matching_run_target
CREATE TABLE public.matching_run_target (
    run_id uuid NOT NULL,
    group_id uuid NOT NULL,
    captured_group_revision text NOT NULL,
    target_generation bigint NOT NULL,
    authority_revision bigint DEFAULT 0 NOT NULL,
    target_capture jsonb,
    target_capture_digest bytea,
    reconciliation_generation bigint,
    CONSTRAINT matching_run_target_authority_revision_check CHECK ((authority_revision >= 0)),
    CONSTRAINT matching_run_target_capture_digest_length CHECK (((target_capture_digest IS NULL) OR (octet_length(target_capture_digest) = 32))),
    CONSTRAINT matching_run_target_capture_pair CHECK (((target_capture IS NULL) = (target_capture_digest IS NULL))),
    CONSTRAINT matching_run_target_captured_group_revision_check CHECK ((length(btrim(captured_group_revision)) > 0)),
    CONSTRAINT matching_run_target_reconciliation_generation_check CHECK (((reconciliation_generation IS NULL) OR (reconciliation_generation > 0))),
    CONSTRAINT matching_run_target_target_generation_check CHECK ((target_generation > 0))
);

-- sequence owned by: matching_delivery_id_seq
ALTER SEQUENCE public.matching_delivery_id_seq OWNED BY public.matching_delivery.id;

-- default: matching_delivery id
ALTER TABLE ONLY public.matching_delivery ALTER COLUMN id SET DEFAULT nextval('public.matching_delivery_id_seq'::regclass);

-- constraint: incoming_group incoming_group_pkey
ALTER TABLE ONLY public.incoming_group
    ADD CONSTRAINT incoming_group_pkey PRIMARY KEY (id);

-- constraint: incoming_group_matching_authority incoming_group_matching_authority_pkey
ALTER TABLE ONLY public.incoming_group_matching_authority
    ADD CONSTRAINT incoming_group_matching_authority_pkey PRIMARY KEY (group_id);

-- constraint: incoming_group_member incoming_group_member_pkey
ALTER TABLE ONLY public.incoming_group_member
    ADD CONSTRAINT incoming_group_member_pkey PRIMARY KEY (group_id, variant_id);

-- constraint: incoming_group_member incoming_group_member_variant_id_key
ALTER TABLE ONLY public.incoming_group_member
    ADD CONSTRAINT incoming_group_member_variant_id_key UNIQUE (variant_id);

-- constraint: incoming_group_member_location incoming_group_member_location_pkey
ALTER TABLE ONLY public.incoming_group_member_location
    ADD CONSTRAINT incoming_group_member_location_pkey PRIMARY KEY (group_id, variant_id, source_root_id, relative_path);

-- constraint: incoming_group_return_automatic_receipt incoming_group_return_automatic_receipt_pkey
ALTER TABLE ONLY public.incoming_group_return_automatic_receipt
    ADD CONSTRAINT incoming_group_return_automatic_receipt_pkey PRIMARY KEY (request_id);

-- constraint: incoming_group_unlink_receipt incoming_group_unlink_receipt_pkey
ALTER TABLE ONLY public.incoming_group_unlink_receipt
    ADD CONSTRAINT incoming_group_unlink_receipt_pkey PRIMARY KEY (request_id);

-- constraint: incoming_grouping_state incoming_grouping_state_pkey
ALTER TABLE ONLY public.incoming_grouping_state
    ADD CONSTRAINT incoming_grouping_state_pkey PRIMARY KEY (singleton);

-- constraint: incoming_match_candidate incoming_match_candidate_pkey
ALTER TABLE ONLY public.incoming_match_candidate
    ADD CONSTRAINT incoming_match_candidate_pkey PRIMARY KEY (group_id, file_member_id, provider_source_id, release_id);

-- constraint: incoming_match_candidate_track incoming_match_candidate_track_pkey
ALTER TABLE ONLY public.incoming_match_candidate_track
    ADD CONSTRAINT incoming_match_candidate_track_pkey PRIMARY KEY (group_id, file_member_id, provider_source_id, release_id, medium, "position");

-- constraint: incoming_match_evaluation incoming_match_evaluation_pkey
ALTER TABLE ONLY public.incoming_match_evaluation
    ADD CONSTRAINT incoming_match_evaluation_pkey PRIMARY KEY (group_id);

-- constraint: incoming_match_evaluation_location incoming_match_evaluation_location_pkey
ALTER TABLE ONLY public.incoming_match_evaluation_location
    ADD CONSTRAINT incoming_match_evaluation_location_pkey PRIMARY KEY (group_id, file_member_id, source_root_id, relative_path);

-- constraint: incoming_match_evaluation_member incoming_match_evaluation_member_pkey
ALTER TABLE ONLY public.incoming_match_evaluation_member
    ADD CONSTRAINT incoming_match_evaluation_member_pkey PRIMARY KEY (group_id, file_member_id);

-- constraint: incoming_matching_automatic_admission incoming_matching_automatic_admission_pkey
ALTER TABLE ONLY public.incoming_matching_automatic_admission
    ADD CONSTRAINT incoming_matching_automatic_admission_pkey PRIMARY KEY (fingerprint);

-- constraint: incoming_matching_generation incoming_matching_generation_pkey
ALTER TABLE ONLY public.incoming_matching_generation
    ADD CONSTRAINT incoming_matching_generation_pkey PRIMARY KEY (group_id);

-- constraint: incoming_matching_reconcile_event incoming_matching_reconcile_event_pkey
ALTER TABLE ONLY public.incoming_matching_reconcile_event
    ADD CONSTRAINT incoming_matching_reconcile_event_pkey PRIMARY KEY (id);

-- constraint: local_matching_receipt local_matching_receipt_pkey
ALTER TABLE ONLY public.local_matching_receipt
    ADD CONSTRAINT local_matching_receipt_pkey PRIMARY KEY (request_id);

-- constraint: manual_matching_admission manual_matching_admission_group_id_request_id_key
ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_group_id_request_id_key UNIQUE (group_id, request_id);

-- constraint: manual_matching_admission manual_matching_admission_lookup_operation_id_key
ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_lookup_operation_id_key UNIQUE (lookup_operation_id);

-- constraint: manual_matching_admission manual_matching_admission_pkey
ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_pkey PRIMARY KEY (request_id);

-- constraint: manual_matching_admission manual_matching_admission_run_id_key
ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_run_id_key UNIQUE (run_id);

-- constraint: manual_matching_lookup_seal manual_matching_lookup_seal_lookup_operation_id_key
ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_lookup_operation_id_key UNIQUE (lookup_operation_id);

-- constraint: manual_matching_lookup_seal manual_matching_lookup_seal_pkey
ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_pkey PRIMARY KEY (request_id);

-- constraint: manual_matching_receipt manual_matching_receipt_pkey
ALTER TABLE ONLY public.manual_matching_receipt
    ADD CONSTRAINT manual_matching_receipt_pkey PRIMARY KEY (request_id);

-- constraint: manual_picker_search manual_picker_search_operation_id_key
ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_operation_id_key UNIQUE (operation_id);

-- constraint: manual_picker_search manual_picker_search_pkey
ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_pkey PRIMARY KEY (request_id);

-- constraint: manual_picker_search_result manual_picker_search_result_pkey
ALTER TABLE ONLY public.manual_picker_search_result
    ADD CONSTRAINT manual_picker_search_result_pkey PRIMARY KEY (operation_id);

-- constraint: matching_acquisition matching_acquisition_pkey
ALTER TABLE ONLY public.matching_acquisition
    ADD CONSTRAINT matching_acquisition_pkey PRIMARY KEY (run_id);

-- constraint: matching_acquisition_phase matching_acquisition_phase_pkey
ALTER TABLE ONLY public.matching_acquisition_phase
    ADD CONSTRAINT matching_acquisition_phase_pkey PRIMARY KEY (run_id, revision);

-- constraint: matching_acquisition_seal matching_acquisition_seal_pkey
ALTER TABLE ONLY public.matching_acquisition_seal
    ADD CONSTRAINT matching_acquisition_seal_pkey PRIMARY KEY (run_id);

-- constraint: matching_acquisition_seal matching_acquisition_seal_river_job_id_key
ALTER TABLE ONLY public.matching_acquisition_seal
    ADD CONSTRAINT matching_acquisition_seal_river_job_id_key UNIQUE (river_job_id);

-- constraint: matching_apply_receipt matching_apply_receipt_pkey
ALTER TABLE ONLY public.matching_apply_receipt
    ADD CONSTRAINT matching_apply_receipt_pkey PRIMARY KEY (apply_operation_id);

-- constraint: matching_apply_request matching_apply_request_apply_operation_id_key
ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_apply_operation_id_key UNIQUE (apply_operation_id);

-- constraint: matching_apply_request matching_apply_request_group_id_evaluation_revision_key
ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_group_id_evaluation_revision_key UNIQUE (group_id, evaluation_revision);

-- constraint: matching_apply_request matching_apply_request_operation_input_unique
ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_operation_input_unique UNIQUE (apply_operation_id, input_sha256);

-- constraint: matching_delivery matching_delivery_pkey
ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_pkey PRIMARY KEY (id);

-- constraint: matching_delivery matching_delivery_river_job_id_key
ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_river_job_id_key UNIQUE (river_job_id);

-- constraint: matching_delivery matching_delivery_run_id_river_job_id_key
ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_run_id_river_job_id_key UNIQUE (run_id, river_job_id);

-- constraint: matching_fallback_authority_seal matching_fallback_authority_seal_pkey
ALTER TABLE ONLY public.matching_fallback_authority_seal
    ADD CONSTRAINT matching_fallback_authority_seal_pkey PRIMARY KEY (run_id, wait_revision);

-- constraint: matching_fallback_authority_seal matching_fallback_authority_seal_run_wait_digest_key
ALTER TABLE ONLY public.matching_fallback_authority_seal
    ADD CONSTRAINT matching_fallback_authority_seal_run_wait_digest_key UNIQUE (run_id, wait_revision, authority_digest);

-- constraint: matching_fallback_calculation_receipt matching_fallback_calculation_receipt_pkey
ALTER TABLE ONLY public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_pkey PRIMARY KEY (run_id, acquisition_revision);

-- constraint: matching_fallback_evaluation_handoff matching_fallback_evaluation_handoff_pkey
ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_pkey PRIMARY KEY (run_id, wait_revision);

-- constraint: matching_fallback_evaluation_handoff matching_fallback_evaluation_handoff_river_job_id_key
ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_river_job_id_key UNIQUE (river_job_id);

-- constraint: matching_fallback_lookup_receipt matching_fallback_lookup_receipt_pkey
ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_pkey PRIMARY KEY (run_id, request_id);

-- constraint: matching_fallback_lookup_receipt matching_fallback_lookup_receipt_request_id_key
ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_request_id_key UNIQUE (request_id);

-- constraint: matching_fallback_request_registration matching_fallback_request_registration_pkey
ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_pkey PRIMARY KEY (run_id, stage_index, role, request_id);

-- constraint: matching_fallback_request_registration matching_fallback_request_registration_request_id_key
ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_request_id_key UNIQUE (request_id);

-- constraint: matching_fallback_search_receipt matching_fallback_search_receipt_pkey
ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_pkey PRIMARY KEY (run_id, request_id);

-- constraint: matching_fallback_search_receipt matching_fallback_search_receipt_request_id_key
ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_request_id_key UNIQUE (request_id);

-- constraint: matching_provider_dependency matching_provider_dependency_pkey
ALTER TABLE ONLY public.matching_provider_dependency
    ADD CONSTRAINT matching_provider_dependency_pkey PRIMARY KEY (parent_run_id, request_id);

-- constraint: matching_run matching_run_operation_id_key
ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_operation_id_key UNIQUE (operation_id);

-- constraint: matching_run matching_run_pkey
ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_pkey PRIMARY KEY (id);

-- constraint: matching_run matching_run_plan_admission_key_key
ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_plan_admission_key_key UNIQUE (plan_admission_key);

-- constraint: matching_run_retirement_authorization matching_run_retirement_authorization_pkey
ALTER TABLE ONLY public.matching_run_retirement_authorization
    ADD CONSTRAINT matching_run_retirement_authorization_pkey PRIMARY KEY (run_id);

-- constraint: matching_run_target matching_run_target_group_id_target_generation_key
ALTER TABLE ONLY public.matching_run_target
    ADD CONSTRAINT matching_run_target_group_id_target_generation_key UNIQUE (group_id, target_generation);

-- constraint: matching_run_target matching_run_target_pkey
ALTER TABLE ONLY public.matching_run_target
    ADD CONSTRAINT matching_run_target_pkey PRIMARY KEY (run_id, group_id);

-- index: incoming_group_member_location_variant_idx
CREATE INDEX incoming_group_member_location_variant_idx ON public.incoming_group_member_location USING btree (variant_id);

-- index: incoming_group_return_automatic_receipt_group_idx
CREATE INDEX incoming_group_return_automatic_receipt_group_idx ON public.incoming_group_return_automatic_receipt USING btree (group_id, created_at);

-- index: incoming_matching_reconcile_current_pending_idx
CREATE UNIQUE INDEX incoming_matching_reconcile_current_pending_idx ON public.incoming_matching_reconcile_event USING btree (reason) WHERE ((reason = 'current'::text) AND (state = 'pending'::text));

-- index: incoming_matching_reconcile_pending_idx
CREATE INDEX incoming_matching_reconcile_pending_idx ON public.incoming_matching_reconcile_event USING btree (created_at, id) WHERE (state = 'pending'::text);

-- index: incoming_matching_reconcile_source_unique
CREATE UNIQUE INDEX incoming_matching_reconcile_source_unique ON public.incoming_matching_reconcile_event USING btree (source_operation_id, source_attempt, reason) WHERE (reason = ANY (ARRAY['analysis_success'::text, 'scan_success'::text]));

-- index: incoming_matching_reconcile_startup_open_unique
CREATE UNIQUE INDEX incoming_matching_reconcile_startup_open_unique ON public.incoming_matching_reconcile_event USING btree (reason) WHERE ((reason = 'startup'::text) AND (state = ANY (ARRAY['pending'::text, 'dispatched'::text])));

-- index: local_matching_receipt_group_idx
CREATE INDEX local_matching_receipt_group_idx ON public.local_matching_receipt USING btree (group_id);

-- index: manual_matching_admission_group_created_idx
CREATE INDEX manual_matching_admission_group_created_idx ON public.manual_matching_admission USING btree (group_id, created_at DESC);

-- index: manual_matching_lookup_seal_operation_idx
CREATE INDEX manual_matching_lookup_seal_operation_idx ON public.manual_matching_lookup_seal USING btree (lookup_operation_id);

-- index: manual_picker_search_group_idx
CREATE INDEX manual_picker_search_group_idx ON public.manual_picker_search USING btree (group_id, operation_id);

-- index: matching_fallback_request_registration_lookup_key
CREATE UNIQUE INDEX matching_fallback_request_registration_lookup_key ON public.matching_fallback_request_registration USING btree (run_id, stage_index, cache_key) WHERE (role = 'lookup'::text);

-- index: matching_fallback_request_registration_one_search
CREATE UNIQUE INDEX matching_fallback_request_registration_one_search ON public.matching_fallback_request_registration USING btree (run_id, stage_index) WHERE (role = 'search'::text);

-- index: matching_provider_dependency_pending_idx
CREATE INDEX matching_provider_dependency_pending_idx ON public.matching_provider_dependency USING btree (request_id) WHERE (terminal_outcome IS NULL);

-- index: matching_run_active_idx
CREATE INDEX matching_run_active_idx ON public.matching_run USING btree (state) WHERE (state = ANY (ARRAY['queued'::text, 'running'::text, 'waiting_provider'::text, 'waiting_policy'::text]));

-- index: matching_run_operation_id_idx
CREATE INDEX matching_run_operation_id_idx ON public.matching_run USING btree (operation_id);

-- fk constraint: incoming_group_member incoming_group_member_group_id_fkey
ALTER TABLE ONLY public.incoming_group_member
    ADD CONSTRAINT incoming_group_member_group_id_fkey FOREIGN KEY (group_id) REFERENCES public.incoming_group(id) ON DELETE CASCADE;

-- fk constraint: incoming_group_member_location incoming_group_member_location_group_id_variant_id_fkey
ALTER TABLE ONLY public.incoming_group_member_location
    ADD CONSTRAINT incoming_group_member_location_group_id_variant_id_fkey FOREIGN KEY (group_id, variant_id) REFERENCES public.incoming_group_member(group_id, variant_id) ON DELETE CASCADE;

-- fk constraint: incoming_match_candidate incoming_match_candidate_group_id_file_member_id_fkey
ALTER TABLE ONLY public.incoming_match_candidate
    ADD CONSTRAINT incoming_match_candidate_group_id_file_member_id_fkey FOREIGN KEY (group_id, file_member_id) REFERENCES public.incoming_match_evaluation_member(group_id, file_member_id) ON DELETE CASCADE;

-- fk constraint: incoming_match_candidate incoming_match_candidate_provider_id_provider_source_id_fkey
ALTER TABLE ONLY public.incoming_match_candidate
    ADD CONSTRAINT incoming_match_candidate_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: incoming_match_candidate incoming_match_candidate_provider_id_release_id_fkey
ALTER TABLE ONLY public.incoming_match_candidate
    ADD CONSTRAINT incoming_match_candidate_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: incoming_match_candidate_track incoming_match_candidate_trac_group_id_file_member_id_prov_fkey
ALTER TABLE ONLY public.incoming_match_candidate_track
    ADD CONSTRAINT incoming_match_candidate_trac_group_id_file_member_id_prov_fkey FOREIGN KEY (group_id, file_member_id, provider_source_id, release_id) REFERENCES public.incoming_match_candidate(group_id, file_member_id, provider_source_id, release_id) ON DELETE CASCADE;

-- fk constraint: incoming_match_candidate_track incoming_match_candidate_trac_provider_id_release_id_track_fkey
ALTER TABLE ONLY public.incoming_match_candidate_track
    ADD CONSTRAINT incoming_match_candidate_trac_provider_id_release_id_track_fkey FOREIGN KEY (provider_id, release_id, track_id) REFERENCES public.provider_release_track(provider_id, release_id, id) ON DELETE RESTRICT;

-- fk constraint: incoming_match_evaluation incoming_match_evaluation_frozen_seal_fk
ALTER TABLE ONLY public.incoming_match_evaluation
    ADD CONSTRAINT incoming_match_evaluation_frozen_seal_fk FOREIGN KEY (frozen_run_id, frozen_wait_revision, frozen_seal_digest) REFERENCES public.matching_fallback_authority_seal(run_id, wait_revision, authority_digest) ON DELETE RESTRICT;

-- fk constraint: incoming_match_evaluation incoming_match_evaluation_provider_id_provider_source_id_fkey
ALTER TABLE ONLY public.incoming_match_evaluation
    ADD CONSTRAINT incoming_match_evaluation_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: incoming_match_evaluation_location incoming_match_evaluation_location_group_id_file_member_id_fkey
ALTER TABLE ONLY public.incoming_match_evaluation_location
    ADD CONSTRAINT incoming_match_evaluation_location_group_id_file_member_id_fkey FOREIGN KEY (group_id, file_member_id) REFERENCES public.incoming_match_evaluation_member(group_id, file_member_id) ON DELETE CASCADE;

-- fk constraint: incoming_match_evaluation_member incoming_match_evaluation_member_group_id_fkey
ALTER TABLE ONLY public.incoming_match_evaluation_member
    ADD CONSTRAINT incoming_match_evaluation_member_group_id_fkey FOREIGN KEY (group_id) REFERENCES public.incoming_match_evaluation(group_id) ON DELETE CASCADE;

-- fk constraint: incoming_matching_generation incoming_matching_generation_current_run_fk
ALTER TABLE ONLY public.incoming_matching_generation
    ADD CONSTRAINT incoming_matching_generation_current_run_fk FOREIGN KEY (current_run_id) REFERENCES public.matching_run(id) ON DELETE SET NULL;

-- fk constraint: manual_matching_admission manual_matching_admission_lookup_operation_id_fkey
ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_lookup_operation_id_fkey FOREIGN KEY (lookup_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: manual_matching_admission manual_matching_admission_run_id_fkey
ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: manual_matching_lookup_seal manual_matching_lookup_seal_lookup_operation_id_fkey
ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_lookup_operation_id_fkey FOREIGN KEY (lookup_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: manual_matching_lookup_seal manual_matching_lookup_seal_provider_source_id_fkey
ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

-- fk constraint: manual_matching_lookup_seal manual_matching_lookup_seal_request_id_fkey
ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.manual_matching_admission(request_id) ON DELETE CASCADE;

-- fk constraint: manual_matching_receipt manual_matching_receipt_local_release_id_fkey
ALTER TABLE ONLY public.manual_matching_receipt
    ADD CONSTRAINT manual_matching_receipt_local_release_id_fkey FOREIGN KEY (local_release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

-- fk constraint: manual_picker_search manual_picker_search_operation_id_fkey
ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE CASCADE;

-- fk constraint: manual_picker_search manual_picker_search_provider_source_id_fkey
ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id);

-- fk constraint: manual_picker_search_result manual_picker_search_result_operation_id_fkey
ALTER TABLE ONLY public.manual_picker_search_result
    ADD CONSTRAINT manual_picker_search_result_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.manual_picker_search(operation_id) ON DELETE CASCADE;

-- fk constraint: manual_picker_search_result manual_picker_search_result_provider_source_id_fkey
ALTER TABLE ONLY public.manual_picker_search_result
    ADD CONSTRAINT manual_picker_search_result_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id);

-- fk constraint: matching_acquisition matching_acquisition_run_id_fkey
ALTER TABLE ONLY public.matching_acquisition
    ADD CONSTRAINT matching_acquisition_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_acquisition_phase matching_acquisition_phase_run_id_fkey
ALTER TABLE ONLY public.matching_acquisition_phase
    ADD CONSTRAINT matching_acquisition_phase_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_acquisition(run_id) ON DELETE CASCADE;

-- fk constraint: matching_acquisition_seal matching_acquisition_seal_run_id_fkey
ALTER TABLE ONLY public.matching_acquisition_seal
    ADD CONSTRAINT matching_acquisition_seal_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_acquisition(run_id) ON DELETE CASCADE;

-- fk constraint: matching_apply_receipt matching_apply_receipt_apply_operation_id_fkey
ALTER TABLE ONLY public.matching_apply_receipt
    ADD CONSTRAINT matching_apply_receipt_apply_operation_id_fkey FOREIGN KEY (apply_operation_id) REFERENCES public.matching_apply_request(apply_operation_id) ON DELETE RESTRICT;

-- fk constraint: matching_apply_receipt matching_apply_receipt_request_input_fk
ALTER TABLE ONLY public.matching_apply_receipt
    ADD CONSTRAINT matching_apply_receipt_request_input_fk FOREIGN KEY (apply_operation_id, input_sha256) REFERENCES public.matching_apply_request(apply_operation_id, input_sha256) DEFERRABLE;

-- fk constraint: matching_apply_request matching_apply_request_apply_operation_id_fkey
ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_apply_operation_id_fkey FOREIGN KEY (apply_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: matching_apply_request matching_apply_request_frozen_seal_fk
ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_frozen_seal_fk FOREIGN KEY (parent_run_id, seal_wait_revision, seal_digest) REFERENCES public.matching_fallback_authority_seal(run_id, wait_revision, authority_digest) DEFERRABLE;

-- fk constraint: matching_apply_request matching_apply_request_parent_run_id_fkey
ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_parent_run_id_fkey FOREIGN KEY (parent_run_id) REFERENCES public.matching_run(id) ON DELETE RESTRICT;

-- fk constraint: matching_delivery matching_delivery_run_id_fkey
ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_fallback_authority_seal matching_fallback_authority_seal_run_id_fkey
ALTER TABLE ONLY public.matching_fallback_authority_seal
    ADD CONSTRAINT matching_fallback_authority_seal_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_fallback_calculation_receipt matching_fallback_calculation__run_id_acquisition_revision_fkey
ALTER TABLE ONLY public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation__run_id_acquisition_revision_fkey FOREIGN KEY (run_id, acquisition_revision) REFERENCES public.matching_acquisition_phase(run_id, revision) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_calculation_receipt matching_fallback_calculation_receipt_run_id_fkey
ALTER TABLE ONLY public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_evaluation_handoff matching_fallback_evaluation_handoff_run_id_fkey
ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_evaluation_handoff matching_fallback_evaluation_handoff_run_id_wait_revision_fkey
ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_run_id_wait_revision_fkey FOREIGN KEY (run_id, wait_revision) REFERENCES public.matching_fallback_authority_seal(run_id, wait_revision) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_lookup_receipt matching_fallback_lookup_receipt_provider_source_id_fkey
ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_lookup_receipt matching_fallback_lookup_receipt_request_id_fkey
ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_lookup_receipt matching_fallback_lookup_receipt_run_id_fkey
ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_fallback_lookup_receipt matching_fallback_lookup_receipt_run_id_request_id_fkey
ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_run_id_request_id_fkey FOREIGN KEY (run_id, request_id) REFERENCES public.matching_provider_dependency(parent_run_id, request_id) ON DELETE CASCADE;

-- fk constraint: matching_fallback_request_registration matching_fallback_request_registration_provider_source_id_fkey
ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_request_registration matching_fallback_request_registration_request_id_fkey
ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_request_registration matching_fallback_request_registration_run_id_fkey
ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_fallback_request_registration matching_fallback_request_registration_run_id_request_id_fkey
ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_run_id_request_id_fkey FOREIGN KEY (run_id, request_id) REFERENCES public.matching_provider_dependency(parent_run_id, request_id) DEFERRABLE INITIALLY DEFERRED;

-- fk constraint: matching_fallback_search_receipt matching_fallback_search_receipt_provider_source_id_fkey
ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_search_receipt matching_fallback_search_receipt_request_id_fkey
ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: matching_fallback_search_receipt matching_fallback_search_receipt_run_id_fkey
ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_fallback_search_receipt matching_fallback_search_receipt_run_id_request_id_fkey
ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_run_id_request_id_fkey FOREIGN KEY (run_id, request_id) REFERENCES public.matching_provider_dependency(parent_run_id, request_id) ON DELETE CASCADE;

-- fk constraint: matching_provider_dependency matching_provider_dependency_parent_run_id_fkey
ALTER TABLE ONLY public.matching_provider_dependency
    ADD CONSTRAINT matching_provider_dependency_parent_run_id_fkey FOREIGN KEY (parent_run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_provider_dependency matching_provider_dependency_request_id_fkey
ALTER TABLE ONLY public.matching_provider_dependency
    ADD CONSTRAINT matching_provider_dependency_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: matching_run matching_run_operation_id_fkey
ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE CASCADE;

-- fk constraint: matching_run matching_run_provider_source_id_fkey
ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

-- fk constraint: matching_run_retirement_authorization matching_run_retirement_authorization_run_id_fkey
ALTER TABLE ONLY public.matching_run_retirement_authorization
    ADD CONSTRAINT matching_run_retirement_authorization_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- fk constraint: matching_run_target matching_run_target_run_id_fkey
ALTER TABLE ONLY public.matching_run_target
    ADD CONSTRAINT matching_run_target_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

-- trigger: manual_matching_receipt manual_matching_receipt_write_once
CREATE TRIGGER manual_matching_receipt_write_once BEFORE DELETE OR UPDATE ON public.manual_matching_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_manual_matching_receipt_mutation();

-- trigger: manual_picker_search_result manual_picker_search_result_immutable
CREATE TRIGGER manual_picker_search_result_immutable BEFORE UPDATE ON public.manual_picker_search_result FOR EACH ROW EXECUTE FUNCTION public.reject_manual_picker_search_result_mutation();

-- trigger: matching_acquisition_phase matching_acquisition_phase_append_only
CREATE TRIGGER matching_acquisition_phase_append_only BEFORE DELETE OR UPDATE ON public.matching_acquisition_phase FOR EACH ROW EXECUTE FUNCTION public.reject_matching_acquisition_phase_mutation();

-- trigger: matching_acquisition_seal matching_acquisition_seal_write_once
CREATE TRIGGER matching_acquisition_seal_write_once BEFORE DELETE OR UPDATE ON public.matching_acquisition_seal FOR EACH ROW EXECUTE FUNCTION public.reject_matching_acquisition_seal_mutation();

-- trigger: matching_apply_receipt matching_apply_receipt_input_binding
CREATE TRIGGER matching_apply_receipt_input_binding BEFORE INSERT ON public.matching_apply_receipt FOR EACH ROW EXECUTE FUNCTION public.validate_matching_apply_receipt_input_binding();

-- trigger: matching_apply_receipt matching_apply_receipt_write_once
CREATE TRIGGER matching_apply_receipt_write_once BEFORE DELETE OR UPDATE ON public.matching_apply_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_apply_receipt_mutation();

-- trigger: matching_apply_request frozen_matching_apply_request_write_once
CREATE TRIGGER frozen_matching_apply_request_write_once BEFORE DELETE OR UPDATE ON public.matching_apply_request FOR EACH ROW EXECUTE FUNCTION public.reject_frozen_matching_apply_request_mutation();

-- trigger: matching_fallback_authority_seal matching_fallback_authority_seal_write_once
CREATE TRIGGER matching_fallback_authority_seal_write_once BEFORE DELETE OR UPDATE ON public.matching_fallback_authority_seal FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_authority_seal_mutation();

-- trigger: matching_fallback_calculation_receipt matching_fallback_calculation_receipt_immutable
CREATE TRIGGER matching_fallback_calculation_receipt_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_calculation_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_calculation_receipt_mutation();

-- trigger: matching_fallback_evaluation_handoff matching_fallback_evaluation_handoff_write_once
CREATE TRIGGER matching_fallback_evaluation_handoff_write_once BEFORE DELETE OR UPDATE ON public.matching_fallback_evaluation_handoff FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_evaluation_handoff_mutation();

-- trigger: matching_fallback_lookup_receipt matching_fallback_lookup_receipt_immutable
CREATE TRIGGER matching_fallback_lookup_receipt_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_lookup_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_lookup_receipt_mutation();

-- trigger: matching_fallback_request_registration matching_fallback_request_registration_immutable
CREATE TRIGGER matching_fallback_request_registration_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_request_registration FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_request_registration_mutation();

-- trigger: matching_fallback_search_receipt matching_fallback_search_receipt_immutable
CREATE TRIGGER matching_fallback_search_receipt_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_search_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_search_receipt_mutation();

-- trigger: matching_run matching_run_quiescence_timestamp_immutable
CREATE TRIGGER matching_run_quiescence_timestamp_immutable BEFORE INSERT OR UPDATE OF quiesced_at ON public.matching_run FOR EACH ROW EXECUTE FUNCTION public.protect_matching_run_quiescence_timestamp();

-- trigger: matching_run_retirement_authorization matching_run_retirement_authorization_immutable
CREATE TRIGGER matching_run_retirement_authorization_immutable BEFORE DELETE OR UPDATE ON public.matching_run_retirement_authorization FOR EACH ROW EXECUTE FUNCTION public.reject_matching_run_retirement_authorization_mutation();

-- trigger: matching_run_retirement_authorization matching_run_retirement_authorization_validate
CREATE TRIGGER matching_run_retirement_authorization_validate BEFORE INSERT ON public.matching_run_retirement_authorization FOR EACH ROW EXECUTE FUNCTION public.validate_matching_run_retirement_authorization();

-- Required singleton, not historical application data.
INSERT INTO public.incoming_grouping_state (singleton, revision, needs_refresh) VALUES (true, 0, true);


-- Squashed additive schema evolution, applied as part of the fresh-instance baseline.

-- Former migration: 20261017000001_drop_native_matroska_metadata.tx.up.sql
ALTER TABLE public.media_metadata_result
    DROP CONSTRAINT media_metadata_result_native_matroska_check,
    DROP COLUMN native_matroska;

-- Former migration: 20261017000002_release_track_identity.tx.up.sql
ALTER TABLE public.release_track
    ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();

ALTER TABLE public.release_track
    ADD CONSTRAINT release_track_id_key UNIQUE (id);

-- Former migration: 20261017000003_library_primary_artist.tx.up.sql
ALTER TABLE local_release
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1),
    ADD COLUMN primary_artist_id uuid REFERENCES local_artist(id) ON DELETE RESTRICT;

ALTER TABLE local_track
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1);

ALTER TABLE local_recording
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1);

ALTER TABLE release_track
    ADD COLUMN metadata_revision bigint NOT NULL DEFAULT 1 CHECK (metadata_revision >= 1),
    ADD COLUMN primary_artist_id uuid REFERENCES local_artist(id) ON DELETE RESTRICT;

-- Former migration: 20261017000004_position_metadata.tx.up.sql
ALTER TABLE release_track
    ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT release_track_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object');

-- Former migration: 20261017000005_file_publication_state.tx.up.sql
CREATE TABLE public.publication_namespace_state (
    singleton boolean NOT NULL,
    output_epoch uuid NOT NULL,
    CONSTRAINT publication_namespace_state_pkey PRIMARY KEY (singleton),
    CONSTRAINT publication_namespace_state_singleton_check CHECK (singleton),
    CONSTRAINT publication_namespace_state_output_epoch_key UNIQUE (output_epoch)
);

INSERT INTO public.publication_namespace_state(singleton, output_epoch)
VALUES (true, gen_random_uuid());

CREATE TABLE public.track_publication (
    position_id uuid NOT NULL,
    epoch uuid NOT NULL,
    generation uuid NOT NULL,
    relative_path text NOT NULL,
    ownership jsonb NOT NULL,
    actual_audio jsonb NOT NULL,
    actual_metadata jsonb NOT NULL,
    actual_assets jsonb NOT NULL,
    quality_policy_version integer NOT NULL,
    validated_at timestamp with time zone NOT NULL,
    CONSTRAINT track_publication_pkey PRIMARY KEY (position_id),
    CONSTRAINT track_publication_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT track_publication_relative_path_check CHECK (relative_path <> ''),
    CONSTRAINT track_publication_ownership_object_check CHECK (jsonb_typeof(ownership) = 'object'),
    CONSTRAINT track_publication_actual_audio_object_check CHECK (jsonb_typeof(actual_audio) = 'object'),
    CONSTRAINT track_publication_actual_metadata_object_check CHECK (jsonb_typeof(actual_metadata) = 'object'),
    CONSTRAINT track_publication_actual_assets_object_check CHECK (jsonb_typeof(actual_assets) = 'object'),
    CONSTRAINT track_publication_quality_policy_version_check CHECK (quality_policy_version > 0)
);

CREATE INDEX track_publication_epoch_idx ON public.track_publication(epoch);

CREATE TABLE public.track_publication_attempt (
    operation_id uuid NOT NULL,
    position_id uuid NOT NULL,
    epoch uuid NOT NULL,
    expected_generation uuid,
    candidate_generation uuid NOT NULL,
    intent_kind text NOT NULL,
    phase text NOT NULL,
    frozen_inputs jsonb,
    namespace_manifest jsonb NOT NULL,
    mutation_journal jsonb NOT NULL,
    CONSTRAINT track_publication_attempt_pkey PRIMARY KEY (operation_id),
    CONSTRAINT track_publication_attempt_operation_fkey FOREIGN KEY (operation_id)
        REFERENCES public.operation(id) ON DELETE RESTRICT,
    CONSTRAINT track_publication_attempt_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT track_publication_attempt_position_key UNIQUE (position_id),
    CONSTRAINT track_publication_attempt_intent_kind_check CHECK (intent_kind = ANY (ARRAY['audio', 'rewrite', 'delete', 'reorganize'])),
    CONSTRAINT track_publication_attempt_phase_check CHECK (phase = ANY (ARRAY['queued', 'preparing', 'staged', 'validated', 'exposing', 'committed', 'cleaning', 'cleanup_pending', 'recovery_required'])),
    CONSTRAINT track_publication_attempt_frozen_inputs_phase_check CHECK (
        (phase IN ('queued', 'preparing', 'staged', 'validated', 'exposing', 'recovery_required')
            AND frozen_inputs IS NOT NULL AND jsonb_typeof(frozen_inputs) = 'object')
        OR (phase IN ('committed', 'cleaning', 'cleanup_pending') AND frozen_inputs IS NULL)
    ),
    CONSTRAINT track_publication_attempt_namespace_manifest_object_check CHECK (jsonb_typeof(namespace_manifest) = 'object'),
    CONSTRAINT track_publication_attempt_mutation_journal_array_check CHECK (jsonb_typeof(mutation_journal) = 'array')
);

CREATE INDEX track_publication_attempt_phase_idx ON public.track_publication_attempt(phase);

CREATE TABLE public.publication_execution_claim (
    operation_id uuid NOT NULL,
    application_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    river_attempt integer NOT NULL,
    claim_uuid uuid NOT NULL,
    boot_uuid uuid NOT NULL,
    delivery_token text NOT NULL,
    epoch uuid NOT NULL,
    state text NOT NULL,
    CONSTRAINT publication_execution_claim_pkey PRIMARY KEY (operation_id, application_attempt),
    CONSTRAINT publication_execution_claim_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id)
        REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT,
    CONSTRAINT publication_execution_claim_claim_uuid_key UNIQUE (claim_uuid),
    CONSTRAINT publication_execution_claim_river_attempt_check CHECK (river_attempt > 0),
    CONSTRAINT publication_execution_claim_delivery_token_check CHECK (delivery_token <> ''),
    CONSTRAINT publication_execution_claim_state_check CHECK (state = ANY (ARRAY['executing', 'recovery_required', 'quiescent']))
);

CREATE INDEX publication_execution_claim_state_idx ON public.publication_execution_claim(state);

ALTER TABLE public.publication_execution_claim
    ADD CONSTRAINT publication_execution_claim_operation_claim_key UNIQUE (operation_id, claim_uuid);

CREATE TABLE public.publication_namespace_claim (
    id uuid NOT NULL,
    epoch uuid NOT NULL,
    key_digest bytea NOT NULL,
    canonical_key text NOT NULL,
    relative_path text NOT NULL,
    node_kind text NOT NULL,
    owner_position_id uuid,
    directory_scope_key text,
    CONSTRAINT publication_namespace_claim_pkey PRIMARY KEY (id),
    CONSTRAINT publication_namespace_claim_owner_fkey FOREIGN KEY (owner_position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT publication_namespace_claim_key_unique UNIQUE (epoch, key_digest),
    CONSTRAINT publication_namespace_claim_digest_size_check CHECK (octet_length(key_digest) = 32),
    CONSTRAINT publication_namespace_claim_canonical_key_check CHECK (canonical_key <> ''),
    CONSTRAINT publication_namespace_claim_relative_path_check CHECK (relative_path <> ''),
    CONSTRAINT publication_namespace_claim_node_kind_check CHECK (node_kind = ANY (ARRAY['file', 'dir'])),
    CONSTRAINT publication_namespace_claim_owner_scope_check CHECK (
        (owner_position_id IS NOT NULL AND directory_scope_key IS NULL)
        OR (owner_position_id IS NULL AND directory_scope_key IS NOT NULL AND directory_scope_key <> '')
    )
);

CREATE INDEX publication_namespace_claim_owner_idx ON public.publication_namespace_claim(owner_position_id);

CREATE TABLE public.publication_namespace_reservation (
    namespace_claim_id uuid NOT NULL,
    position_id uuid NOT NULL,
    generation uuid NOT NULL,
    CONSTRAINT publication_namespace_reservation_pkey PRIMARY KEY (namespace_claim_id, position_id, generation),
    CONSTRAINT publication_namespace_reservation_claim_fkey FOREIGN KEY (namespace_claim_id)
        REFERENCES public.publication_namespace_claim(id) ON DELETE RESTRICT,
    CONSTRAINT publication_namespace_reservation_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT
);

CREATE INDEX publication_namespace_reservation_position_idx ON public.publication_namespace_reservation(position_id);

CREATE TABLE public.source_cleanup_item (
    id uuid NOT NULL,
    operation_id uuid NOT NULL,
    position_id uuid NOT NULL,
    epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    state text NOT NULL,
    source_snapshot jsonb,
    borrower_revision uuid,
    mutation_claim_uuid uuid,
    mutation_started_at timestamptz,
    mutation_completed_at timestamptz,
    safe_error text,
    CONSTRAINT source_cleanup_item_pkey PRIMARY KEY (id),
    CONSTRAINT source_cleanup_item_operation_fkey FOREIGN KEY (operation_id)
        REFERENCES public.operation(id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_item_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_item_mutation_claim_fkey FOREIGN KEY (operation_id, mutation_claim_uuid)
        REFERENCES public.publication_execution_claim(operation_id, claim_uuid) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_item_mutation_frontier_check CHECK (
        (mutation_claim_uuid IS NULL AND mutation_started_at IS NULL)
        OR (mutation_claim_uuid IS NOT NULL AND mutation_started_at IS NOT NULL)
    ),
    CONSTRAINT source_cleanup_item_mutation_completion_check CHECK (
        mutation_completed_at IS NULL
        OR (mutation_started_at IS NOT NULL AND mutation_completed_at >= mutation_started_at)
    ),
    CONSTRAINT source_cleanup_item_state_check CHECK (state = ANY (ARRAY['queued', 'claimed', 'recovery_required', 'retry_pending', 'cancelled', 'stale'])),
    CONSTRAINT source_cleanup_item_live_borrower_check CHECK (
        state NOT IN ('queued', 'claimed', 'recovery_required', 'retry_pending')
        OR (source_snapshot IS NOT NULL AND jsonb_typeof(source_snapshot) = 'object' AND borrower_revision IS NOT NULL)
    ),
    CONSTRAINT source_cleanup_item_snapshot_object_check CHECK (source_snapshot IS NULL OR jsonb_typeof(source_snapshot) = 'object')
);

CREATE INDEX source_cleanup_item_state_idx ON public.source_cleanup_item(state);
CREATE INDEX source_cleanup_item_operation_idx ON public.source_cleanup_item(operation_id);



CREATE FUNCTION public.check_publication_operation_owner_trigger() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME='operation' THEN
        PERFORM public.check_publication_operation_owner(COALESCE(NEW.id,OLD.id));
    ELSE
        IF TG_OP <> 'INSERT' THEN PERFORM public.check_publication_operation_owner(OLD.operation_id); END IF;
        IF TG_OP <> 'DELETE' AND (TG_OP='INSERT' OR NEW.operation_id IS DISTINCT FROM OLD.operation_id) THEN
            PERFORM public.check_publication_operation_owner(NEW.operation_id);
        ELSIF TG_OP <> 'DELETE' THEN PERFORM public.check_publication_operation_owner(NEW.operation_id); END IF;
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER publication_operation_owner_operation
AFTER INSERT OR UPDATE OR DELETE ON public.operation DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_publication_operation_owner_trigger();
CREATE CONSTRAINT TRIGGER publication_operation_owner_attempt
AFTER INSERT OR UPDATE OR DELETE ON public.track_publication_attempt DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_publication_operation_owner_trigger();
CREATE CONSTRAINT TRIGGER publication_operation_owner_cleanup_item
AFTER INSERT OR UPDATE OR DELETE ON public.source_cleanup_item DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_publication_operation_owner_trigger();

-- Former migration: 20261017000007_publication_source_hold.tx.up.sql
ALTER TABLE public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_operation_epoch_unique UNIQUE (operation_id, epoch);

CREATE TABLE public.publication_source_hold (
    operation_id uuid NOT NULL,
    application_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    epoch uuid NOT NULL,
    hold_uuid uuid NOT NULL,
    source_root_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    work_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    CONSTRAINT publication_source_hold_pkey PRIMARY KEY (operation_id),
    CONSTRAINT publication_source_hold_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id)
        REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_attempt_fkey FOREIGN KEY (operation_id, epoch)
        REFERENCES public.track_publication_attempt(operation_id, epoch) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_root_fkey FOREIGN KEY (source_root_id)
        REFERENCES public.source_root(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_location_fkey FOREIGN KEY (source_location_id)
        REFERENCES public.source_location(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_work_fkey FOREIGN KEY (work_id)
        REFERENCES public.source_analysis_work(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_variant_fkey FOREIGN KEY (media_variant_id)
        REFERENCES public.media_variant(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_uuid_key UNIQUE (hold_uuid),
    CONSTRAINT publication_source_hold_attempt_check CHECK (application_attempt > 0),
    CONSTRAINT publication_source_hold_job_check CHECK (river_job_id > 0),
    CONSTRAINT publication_source_hold_configured_path_check CHECK (configured_path <> ''),
    CONSTRAINT publication_source_hold_inventory_path_check CHECK (inventory_path <> ''),
    CONSTRAINT publication_source_hold_relative_path_check CHECK (relative_path <> ''),
    CONSTRAINT publication_source_hold_size_check CHECK (size_bytes >= 0)
);

CREATE INDEX publication_source_hold_source_idx
    ON public.publication_source_hold(source_root_id, source_location_id, work_id, media_variant_id);

-- Former migration: 20261018000001_publication_manual_lyrics_receipt.tx.up.sql
CREATE TABLE public.publication_manual_lyrics_receipt (
    position_id uuid NOT NULL,
    epoch uuid NOT NULL,
    desired_revision bigint NOT NULL,
    confirmed_generation uuid NOT NULL,
    published_generation uuid NOT NULL,
    desired_state text NOT NULL,
    desired_sha256 text,
    warning_code text,
    recorded_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT publication_manual_lyrics_receipt_pkey PRIMARY KEY (position_id),
    CONSTRAINT publication_manual_lyrics_receipt_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT publication_manual_lyrics_receipt_revision_check CHECK (desired_revision >= 0),
    CONSTRAINT publication_manual_lyrics_receipt_state_check CHECK (
        (desired_state = 'value' AND desired_sha256 IS NOT NULL AND desired_sha256 ~ '^[0-9a-f]{64}$') OR
        (desired_state = 'explicit_empty' AND desired_sha256 IS NULL)
    ),
    CONSTRAINT publication_manual_lyrics_receipt_warning_check CHECK (
        warning_code IS NULL OR warning_code IN ('readback_mismatch')
    )
);

-- Former migration: 20261020000001_publication_reconciliation.tx.up.sql
ALTER TABLE public.operation DROP CONSTRAINT operation_kind_check;
ALTER TABLE public.operation ADD CONSTRAINT operation_kind_check CHECK (kind = ANY (ARRAY[
    'install','activate','delete','move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
    'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
    'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1',
    'matching_apply_v1','publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1'
]));
ALTER TABLE public.operation DROP CONSTRAINT operation_mutation_has_target_installation;
ALTER TABLE public.operation ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    state NOT IN ('queued','running') OR target_installation_id IS NOT NULL
);
ALTER TABLE public.operation DROP CONSTRAINT operation_target_identity;
ALTER TABLE public.operation ADD CONSTRAINT operation_target_identity CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    COALESCE(input_snapshot ->> 'target_identity','') <> ''
);

-- Keep recovery snapshots closed to unapproved fields. This check was present
-- in the discarded publication transition and must survive the fresh-instance
-- squash because retry grants carry durable recovery authority.
ALTER TABLE public.operation ADD CONSTRAINT operation_publication_snapshot_check CHECK ((
    kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1','retry_publication_sources_v1') OR
    (jsonb_typeof(input_snapshot) = 'object'
     AND input_snapshot - (CASE
         WHEN kind = 'retry_publication_sources_v1'
             THEN ARRAY['version','position_id','output_epoch','original_operation_id','original_claim_uuid']
         WHEN kind = 'publish_track_v1'
             THEN ARRAY['version','position_id','output_epoch','intent_kind']
         ELSE ARRAY['version','position_id','output_epoch']
     END) = '{}'::jsonb
     AND input_snapshot ?& (CASE
         WHEN kind = 'retry_publication_sources_v1'
             THEN ARRAY['version','position_id','output_epoch','original_operation_id','original_claim_uuid']
         ELSE ARRAY['version','position_id','output_epoch']
     END)
     AND jsonb_typeof(input_snapshot->'version')='number' AND input_snapshot->>'version'='1'
     AND jsonb_typeof(input_snapshot->'position_id')='string'
     AND (input_snapshot->>'position_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND jsonb_typeof(input_snapshot->'output_epoch')='string'
     AND (input_snapshot->>'output_epoch') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND ((kind='publish_track_v1' AND input_snapshot ? 'intent_kind'
           AND jsonb_typeof(input_snapshot->'intent_kind')='string'
           AND input_snapshot->>'intent_kind' IN ('audio','rewrite','delete','reorganize'))
       OR (kind='cleanup_publication_sources_v1' AND NOT (input_snapshot ? 'intent_kind'))
       OR (kind='retry_publication_sources_v1'
           AND jsonb_typeof(input_snapshot->'original_operation_id')='string'
           AND (input_snapshot->>'original_operation_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
           AND jsonb_typeof(input_snapshot->'original_claim_uuid')='string'
           AND (input_snapshot->>'original_claim_uuid') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')))
) IS TRUE);
ALTER TABLE public.operation DROP CONSTRAINT operation_source_analysis_target_shape;
ALTER TABLE public.operation ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
    (kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1') AND source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND target_installation_id IS NULL AND target_source_root_id IS NULL AND target_source_location_id IS NULL
        AND provider_source_id IS NULL AND provider_configuration_identity IS NULL AND provider_cache_key IS NULL
        AND NOT provider_explicit_refresh AND (NOT tools_read_required OR kind='publish_track_v1'))
    OR (kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1') AND (((source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND (NOT tools_read_required OR kind = 'scan_source'))
      OR (source_analysis_mode = 'batch' AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target)
      OR (source_analysis_mode = 'single_step' AND
          (((state IN ('queued','running') AND target_work_id IS NOT NULL AND target_step IN ('sha256','probe','fingerprint','metadata'))
            OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))
            AND (NOT rerun_target OR (state IN ('queued','running') AND target_step='fingerprint') OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))))) IS TRUE))
);
ALTER TABLE public.operation ADD CONSTRAINT operation_publication_reconciliation_snapshot_check CHECK (
    kind <> 'publication_reconcile_v1' OR (
    (jsonb_typeof(input_snapshot)='object'
     AND input_snapshot - ARRAY['version','position_id','output_epoch']='{}'::jsonb
     AND input_snapshot ?& ARRAY['version','position_id','output_epoch']
     AND jsonb_typeof(input_snapshot->'version')='number' AND input_snapshot->>'version'='1'
     AND jsonb_typeof(input_snapshot->'position_id')='string'
     AND (input_snapshot->>'position_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND jsonb_typeof(input_snapshot->'output_epoch')='string'
     AND (input_snapshot->>'output_epoch') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE)
);

CREATE TABLE public.publication_reconciliation_request (
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL REFERENCES public.release_track(id),
    requested_generation bigint NOT NULL DEFAULT 0 CHECK (requested_generation >= 0),
    reconciled_generation bigint NOT NULL DEFAULT 0 CHECK (reconciled_generation >= 0 AND reconciled_generation <= requested_generation),
    PRIMARY KEY (output_epoch, position_id)
);
CREATE TABLE public.publication_reconciliation_dirty_scope (
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    scope text NOT NULL CHECK (scope <> ''),
    generation bigint NOT NULL CHECK (generation > 0),
    PRIMARY KEY (output_epoch, position_id, scope),
    FOREIGN KEY (output_epoch, position_id) REFERENCES public.publication_reconciliation_request(output_epoch, position_id) ON DELETE CASCADE
);
CREATE TABLE public.publication_reconciliation_event (
    id uuid PRIMARY KEY,
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    operation_id uuid NOT NULL UNIQUE REFERENCES public.operation(id),
    observed_generation bigint NOT NULL CHECK (observed_generation > 0),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','dispatched','completed','cancelled')),
    registered_job_id bigint,
    delivery_epoch bigint NOT NULL DEFAULT 0 CHECK (delivery_epoch >= 0),
    highest_delivery_attempt integer NOT NULL DEFAULT 0 CHECK (highest_delivery_attempt >= 0),
    delivery_claimed boolean NOT NULL DEFAULT false,
    claim_token uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    FOREIGN KEY (output_epoch, position_id) REFERENCES public.publication_reconciliation_request(output_epoch, position_id),
    CONSTRAINT publication_reconciliation_delivery_shape CHECK ((
      (state='pending' AND registered_job_id IS NULL AND NOT delivery_claimed AND highest_delivery_attempt=0 AND delivery_epoch=0)
      OR (state='dispatched' AND registered_job_id > 0 AND delivery_epoch > 0 AND (NOT delivery_claimed OR highest_delivery_attempt > 0) AND (delivery_claimed = (claim_token IS NOT NULL)))
      OR (state IN ('completed','cancelled') AND NOT delivery_claimed AND (registered_job_id IS NULL OR registered_job_id > 0))
    ) IS TRUE AND (delivery_claimed = (claim_token IS NOT NULL)))
);
CREATE UNIQUE INDEX publication_reconciliation_one_pending ON public.publication_reconciliation_event(output_epoch,position_id) WHERE state='pending';
CREATE UNIQUE INDEX publication_reconciliation_one_dispatched ON public.publication_reconciliation_event(output_epoch,position_id) WHERE state='dispatched';
CREATE FUNCTION public.guard_publication_reconciliation_event_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.output_epoch IS DISTINCT FROM OLD.output_epoch OR NEW.position_id IS DISTINCT FROM OLD.position_id THEN
        RAISE EXCEPTION 'publication reconciliation event identity is immutable' USING ERRCODE='23514';
    END IF;
    IF (OLD.state <> 'pending' AND NEW.observed_generation IS DISTINCT FROM OLD.observed_generation)
       OR NEW.observed_generation < OLD.observed_generation
       OR NEW.delivery_epoch < OLD.delivery_epoch
       OR (NEW.delivery_epoch=OLD.delivery_epoch AND NEW.highest_delivery_attempt < OLD.highest_delivery_attempt)
       OR (OLD.state <> 'pending' AND NEW.registered_job_id IS DISTINCT FROM OLD.registered_job_id AND NEW.delivery_epoch<=OLD.delivery_epoch)
       OR (OLD.state IN ('completed','cancelled') AND NEW.state IS DISTINCT FROM OLD.state) THEN
        RAISE EXCEPTION 'publication reconciliation event frontier cannot move backwards' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER publication_reconciliation_event_update BEFORE UPDATE ON public.publication_reconciliation_event
FOR EACH ROW EXECUTE FUNCTION public.guard_publication_reconciliation_event_update();
CREATE FUNCTION public.check_publication_reconciliation_owner(p_operation_id uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE op public.operation%ROWTYPE;
BEGIN
    SELECT * INTO op FROM public.operation WHERE id=p_operation_id;
    IF NOT FOUND THEN RETURN; END IF;
    IF op.kind <> 'publication_reconcile_v1' THEN
        IF EXISTS (SELECT 1 FROM public.publication_reconciliation_event WHERE operation_id=op.id) THEN
            RAISE EXCEPTION 'publication reconciliation event requires its matching operation kind' USING ERRCODE='23514';
        END IF;
        RETURN;
    END IF;
    IF op.state IN ('queued','running') OR EXISTS (SELECT 1 FROM public.publication_reconciliation_event WHERE operation_id=op.id) THEN
        IF NOT EXISTS (SELECT 1 FROM public.publication_reconciliation_event e
            JOIN public.publication_reconciliation_request r USING (output_epoch,position_id)
            WHERE e.operation_id=op.id AND e.position_id=(op.input_snapshot->>'position_id')::uuid
              AND e.output_epoch=(op.input_snapshot->>'output_epoch')::uuid AND e.observed_generation<=r.requested_generation
              AND (op.state NOT IN ('queued','running') OR e.state IN ('pending','dispatched'))) THEN
            RAISE EXCEPTION 'publication reconciliation operation requires a matching event' USING ERRCODE='23514';
        END IF;
    END IF;
END $$;
CREATE FUNCTION public.check_publication_reconciliation_owner_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME='operation' THEN
        PERFORM public.check_publication_reconciliation_owner(COALESCE(NEW.id,OLD.id));
    ELSE
        IF TG_OP <> 'INSERT' THEN PERFORM public.check_publication_reconciliation_owner(OLD.operation_id); END IF;
        IF TG_OP <> 'DELETE' THEN PERFORM public.check_publication_reconciliation_owner(NEW.operation_id); END IF;
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER publication_reconciliation_owner_operation AFTER INSERT OR UPDATE OR DELETE ON public.operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_publication_reconciliation_owner_trigger();
CREATE CONSTRAINT TRIGGER publication_reconciliation_owner_event AFTER INSERT OR UPDATE OR DELETE ON public.publication_reconciliation_event DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_publication_reconciliation_owner_trigger();

-- Former migration: 20261021000001_publication_reconciliation_parking.tx.up.sql
ALTER TABLE public.publication_reconciliation_request
    ADD COLUMN evaluated_generation bigint,
    ADD COLUMN parked_reason text,
    ADD COLUMN retry_not_before timestamptz,
    ADD CONSTRAINT publication_reconciliation_parking_shape CHECK (
        (evaluated_generation IS NULL AND parked_reason IS NULL AND retry_not_before IS NULL)
        OR (evaluated_generation IS NOT NULL AND evaluated_generation >= 0
            AND (parked_reason IS NULL OR parked_reason <> ''))
    );

-- Former migration: 20261022000001_initial_lyrics_projection.tx.up.sql
ALTER TABLE track_publication
    ADD COLUMN initial_lyrics jsonb;

ALTER TABLE track_publication
    ADD CONSTRAINT track_publication_initial_lyrics_object
    CHECK (initial_lyrics IS NULL OR jsonb_typeof(initial_lyrics) = 'object');

-- Former migration: 20261023000001_source_cleanup_owner.tx.up.sql
ALTER TABLE public.source_cleanup_item
    ADD CONSTRAINT source_cleanup_item_id_operation_key UNIQUE (id, operation_id);

CREATE TABLE public.source_cleanup_owner (
    item_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    application_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    owner_uuid uuid NOT NULL,
    source_root_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    work_id uuid NOT NULL,
    CONSTRAINT source_cleanup_owner_pkey PRIMARY KEY (item_id),
    CONSTRAINT source_cleanup_owner_item_fkey FOREIGN KEY (item_id, operation_id)
        REFERENCES public.source_cleanup_item(id, operation_id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_owner_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id)
        REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_owner_application_attempt_check CHECK (application_attempt > 0),
    CONSTRAINT source_cleanup_owner_river_job_id_check CHECK (river_job_id > 0),
    CONSTRAINT source_cleanup_owner_owner_uuid_key UNIQUE (owner_uuid),
    CONSTRAINT source_cleanup_owner_source_location_id_key UNIQUE (source_location_id),
    CONSTRAINT source_cleanup_owner_work_id_key UNIQUE (work_id)
);


ALTER TABLE public.source_cleanup_owner ADD CONSTRAINT source_cleanup_owner_item_owner_key UNIQUE (item_id, owner_uuid);
ALTER TABLE public.publication_execution_claim ADD CONSTRAINT publication_execution_claim_delivery_identity_key
    UNIQUE (operation_id, application_attempt, river_job_id, claim_uuid);

CREATE FUNCTION public.valid_source_cleanup_explicit_retry_binding(b jsonb, op_id uuid, app_attempt integer,
    job_id bigint, claim_id uuid, pos_id uuid, retry_epoch uuid, generation uuid) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN jsonb_typeof(b)='object' THEN COALESCE(
        b ?& ARRAY['version','original_operation_id','original_application_attempt','original_river_job_id','original_river_attempt',
            'original_river_kind','original_claim_uuid','original_boot_uuid','original_delivery_token','original_claim_state',
            'epoch','position_id','publication_generation','items']
        AND b - ARRAY['version','original_operation_id','original_application_attempt','original_river_job_id','original_river_attempt',
            'original_river_kind','original_claim_uuid','original_boot_uuid','original_delivery_token','original_claim_state',
            'epoch','position_id','publication_generation','items'] = '{}'::jsonb
        AND jsonb_typeof(b->'version')='number' AND b->>'version'='1'
        AND jsonb_typeof(b->'original_operation_id')='string' AND b->>'original_operation_id'=op_id::text AND op_id<>'00000000-0000-0000-0000-000000000000'::uuid
        AND CASE WHEN jsonb_typeof(b->'original_application_attempt')='number' THEN (b->>'original_application_attempt')::numeric=app_attempt AND app_attempt>0 ELSE false END
        AND CASE WHEN jsonb_typeof(b->'original_river_job_id')='number' THEN (b->>'original_river_job_id')::numeric=job_id AND job_id>0 ELSE false END
        AND CASE WHEN jsonb_typeof(b->'original_river_attempt')='number' THEN (b->>'original_river_attempt')::numeric>0 AND (b->>'original_river_attempt')::numeric=trunc((b->>'original_river_attempt')::numeric) ELSE false END
        AND jsonb_typeof(b->'original_river_kind')='string' AND b->>'original_river_kind'='cleanup_publication_sources_v1'
        AND jsonb_typeof(b->'original_claim_uuid')='string' AND b->>'original_claim_uuid'=claim_id::text
        AND jsonb_typeof(b->'original_boot_uuid')='string' AND (b->>'original_boot_uuid') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND b->>'original_boot_uuid'<>'00000000-0000-0000-0000-000000000000'
        AND jsonb_typeof(b->'original_delivery_token')='string' AND b->>'original_delivery_token'<>''
        AND jsonb_typeof(b->'original_claim_state')='string' AND b->>'original_claim_state'='recovery_required'
        AND jsonb_typeof(b->'epoch')='string' AND b->>'epoch'=retry_epoch::text AND retry_epoch<>'00000000-0000-0000-0000-000000000000'::uuid
        AND jsonb_typeof(b->'position_id')='string' AND b->>'position_id'=pos_id::text AND pos_id<>'00000000-0000-0000-0000-000000000000'::uuid
        AND jsonb_typeof(b->'publication_generation')='string' AND b->>'publication_generation'=generation::text AND generation<>'00000000-0000-0000-0000-000000000000'::uuid
        AND CASE WHEN jsonb_typeof(b->'items')='array' THEN jsonb_array_length(b->'items')>0
        AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(b->'items') AS elements(item)
            WHERE CASE WHEN jsonb_typeof(item)='object' THEN
                  NOT item ?& ARRAY['item_id','state','safe_error','source_snapshot','borrower_revision','mutation_claim_uuid',
                   'mutation_started_at','mutation_completed_at','owner_uuid','source_root_id','source_location_id','work_id']
               OR item - ARRAY['item_id','state','safe_error','source_snapshot','borrower_revision','mutation_claim_uuid',
                   'mutation_started_at','mutation_completed_at','owner_uuid','source_root_id','source_location_id','work_id'] <> '{}'::jsonb
               OR jsonb_typeof(item->'item_id') IS DISTINCT FROM 'string' OR (item->>'item_id') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               OR jsonb_typeof(item->'state') IS DISTINCT FROM 'string' OR item->>'state' NOT IN ('claimed','recovery_required')
               OR jsonb_typeof(item->'safe_error') NOT IN ('string','null')
               OR jsonb_typeof(item->'source_snapshot') IS DISTINCT FROM 'object' OR item->'source_snapshot'='{}'::jsonb
               OR jsonb_typeof(item->'borrower_revision') IS DISTINCT FROM 'string' OR (item->>'borrower_revision') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               OR jsonb_typeof(item->'mutation_claim_uuid') NOT IN ('string','null')
               OR jsonb_typeof(item->'mutation_started_at') NOT IN ('string','null')
               OR jsonb_typeof(item->'mutation_completed_at') NOT IN ('string','null')
               OR jsonb_typeof(item->'owner_uuid') IS DISTINCT FROM 'string' OR (item->>'owner_uuid') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               OR jsonb_typeof(item->'source_root_id') IS DISTINCT FROM 'string' OR (item->>'source_root_id') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               OR jsonb_typeof(item->'source_location_id') IS DISTINCT FROM 'string' OR (item->>'source_location_id') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               OR jsonb_typeof(item->'work_id') IS DISTINCT FROM 'string' OR (item->>'work_id') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               ELSE true END)
        ELSE false END
    , false) ELSE false END
$$;


CREATE TABLE public.source_cleanup_explicit_retry (
    id uuid NOT NULL PRIMARY KEY,
    recovery_operation_id uuid NOT NULL UNIQUE REFERENCES public.operation(id) ON DELETE RESTRICT,
    original_operation_id uuid NOT NULL,
    original_application_attempt integer NOT NULL,
    original_river_job_id bigint NOT NULL,
    original_claim_uuid uuid NOT NULL,
    position_id uuid NOT NULL REFERENCES public.release_track(id) ON DELETE RESTRICT,
    epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    binding jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    consumed_application_attempt integer,
    consumed_river_job_id bigint,
    consumed_claim_uuid uuid,
    CONSTRAINT source_cleanup_explicit_retry_original_operation_fkey
        FOREIGN KEY (original_operation_id, original_application_attempt, original_river_job_id)
        REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_explicit_retry_original_claim_fkey
        FOREIGN KEY (original_operation_id, original_claim_uuid)
        REFERENCES public.publication_execution_claim(operation_id, claim_uuid) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_explicit_retry_one_outstanding UNIQUE (original_operation_id, original_claim_uuid),
    CONSTRAINT source_cleanup_explicit_retry_consumption_complete CHECK (
        (consumed_application_attempt IS NULL AND consumed_river_job_id IS NULL AND consumed_claim_uuid IS NULL)
        OR (consumed_application_attempt IS NOT NULL AND consumed_river_job_id IS NOT NULL AND consumed_claim_uuid IS NOT NULL
            AND consumed_application_attempt > 0 AND consumed_river_job_id > 0)
    ),
    CONSTRAINT source_cleanup_explicit_retry_binding_contract CHECK (
        public.valid_source_cleanup_explicit_retry_binding(binding, original_operation_id,
            original_application_attempt, original_river_job_id, original_claim_uuid, position_id, epoch, publication_generation) IS TRUE
    )
);


ALTER TABLE public.source_cleanup_explicit_retry ADD CONSTRAINT source_cleanup_explicit_retry_consumed_claim_fkey
    FOREIGN KEY (recovery_operation_id, consumed_application_attempt, consumed_river_job_id, consumed_claim_uuid)
    REFERENCES public.publication_execution_claim(operation_id, application_attempt, river_job_id, claim_uuid) ON DELETE RESTRICT;

CREATE TABLE public.source_cleanup_explicit_retry_item (
    retry_id uuid NOT NULL REFERENCES public.source_cleanup_explicit_retry(id) ON DELETE CASCADE,
    original_operation_id uuid NOT NULL,
    item_id uuid NOT NULL,
    owner_uuid uuid NOT NULL,
    PRIMARY KEY (retry_id, item_id),
    FOREIGN KEY (item_id, original_operation_id)
        REFERENCES public.source_cleanup_item(id, operation_id) ON DELETE RESTRICT,
    FOREIGN KEY (item_id, owner_uuid)
        REFERENCES public.source_cleanup_owner(item_id, owner_uuid) ON DELETE RESTRICT
);

CREATE OR REPLACE FUNCTION public.check_publication_operation_owner(p_operation_id uuid) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE op public.operation%ROWTYPE;
BEGIN
    SELECT * INTO op FROM public.operation WHERE id=p_operation_id;
    IF NOT FOUND OR op.state NOT IN ('queued','running') THEN RETURN; END IF;
    IF op.kind='publish_track_v1' AND NOT EXISTS (
        SELECT 1 FROM public.track_publication_attempt a
        WHERE a.operation_id=op.id AND a.position_id=(op.input_snapshot->>'position_id')::uuid
          AND a.epoch=(op.input_snapshot->>'output_epoch')::uuid AND a.intent_kind=op.input_snapshot->>'intent_kind'
    ) THEN RAISE EXCEPTION 'active publication operation requires a matching attempt' USING ERRCODE='23514'; END IF;
    IF op.kind='cleanup_publication_sources_v1' AND NOT EXISTS (
        SELECT 1 FROM public.source_cleanup_item i
        WHERE i.operation_id=op.id AND i.position_id=(op.input_snapshot->>'position_id')::uuid
          AND i.epoch=(op.input_snapshot->>'output_epoch')::uuid
    ) THEN RAISE EXCEPTION 'active cleanup operation requires a matching item' USING ERRCODE='23514'; END IF;
    IF op.kind='retry_publication_sources_v1' AND NOT EXISTS (
        SELECT 1 FROM public.source_cleanup_explicit_retry r
        WHERE r.recovery_operation_id=op.id
          AND r.position_id=(op.input_snapshot->>'position_id')::uuid
          AND r.epoch=(op.input_snapshot->>'output_epoch')::uuid
          AND r.original_operation_id=(op.input_snapshot->>'original_operation_id')::uuid
          AND r.original_claim_uuid=(op.input_snapshot->>'original_claim_uuid')::uuid
          AND (SELECT count(*) FROM public.source_cleanup_explicit_retry_item i WHERE i.retry_id=r.id) =
              (SELECT count(*) FROM public.source_cleanup_item i WHERE i.operation_id=r.original_operation_id)
          AND NOT EXISTS (
              SELECT 1 FROM public.source_cleanup_item item
              LEFT JOIN public.source_cleanup_owner owner ON owner.item_id=item.id
              LEFT JOIN public.source_cleanup_explicit_retry_item retry_item
                ON retry_item.item_id=item.id AND retry_item.retry_id=r.id
               AND retry_item.original_operation_id=item.operation_id AND retry_item.owner_uuid=owner.owner_uuid
              WHERE item.operation_id=r.original_operation_id AND retry_item.item_id IS NULL
          )
    ) THEN RAISE EXCEPTION 'active source cleanup retry requires an exact grant' USING ERRCODE='23514'; END IF;
END $$;

CREATE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE op_id uuid;
BEGIN
    IF TG_TABLE_NAME='operation' THEN
        PERFORM public.check_publication_operation_owner(COALESCE(NEW.id,OLD.id));
    ELSIF TG_TABLE_NAME='source_cleanup_explicit_retry' THEN
        IF TG_OP <> 'INSERT' THEN PERFORM public.check_publication_operation_owner(OLD.recovery_operation_id); END IF;
        IF TG_OP <> 'DELETE' THEN PERFORM public.check_publication_operation_owner(NEW.recovery_operation_id); END IF;
    ELSE
        SELECT recovery_operation_id INTO op_id FROM public.source_cleanup_explicit_retry WHERE id=COALESCE(NEW.retry_id,OLD.retry_id);
        IF op_id IS NOT NULL THEN PERFORM public.check_publication_operation_owner(op_id); END IF;
    END IF;
    RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER publication_operation_owner_retry_operation
AFTER INSERT OR UPDATE OR DELETE ON public.operation DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();
CREATE CONSTRAINT TRIGGER publication_operation_owner_retry_grant
AFTER INSERT OR UPDATE OR DELETE ON public.source_cleanup_explicit_retry DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();
CREATE CONSTRAINT TRIGGER publication_operation_owner_retry_item
AFTER INSERT OR UPDATE OR DELETE ON public.source_cleanup_explicit_retry_item DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();

CREATE FUNCTION public.protect_source_cleanup_explicit_retry_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.consumed_claim_uuid IS NOT NULL AND
       ROW(NEW.consumed_application_attempt, NEW.consumed_river_job_id, NEW.consumed_claim_uuid)
       IS DISTINCT FROM ROW(OLD.consumed_application_attempt, OLD.consumed_river_job_id, OLD.consumed_claim_uuid) THEN
        RAISE EXCEPTION 'source cleanup retry consumption is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.recovery_operation_id IS DISTINCT FROM OLD.recovery_operation_id
       OR NEW.original_operation_id IS DISTINCT FROM OLD.original_operation_id
       OR NEW.original_application_attempt IS DISTINCT FROM OLD.original_application_attempt
       OR NEW.original_river_job_id IS DISTINCT FROM OLD.original_river_job_id
       OR NEW.original_claim_uuid IS DISTINCT FROM OLD.original_claim_uuid
       OR NEW.position_id IS DISTINCT FROM OLD.position_id OR NEW.epoch IS DISTINCT FROM OLD.epoch
       OR NEW.publication_generation IS DISTINCT FROM OLD.publication_generation
       OR NEW.binding IS DISTINCT FROM OLD.binding OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'source cleanup retry binding is immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER source_cleanup_explicit_retry_binding_immutable
BEFORE UPDATE ON public.source_cleanup_explicit_retry
FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_explicit_retry_binding();

CREATE FUNCTION public.protect_source_cleanup_explicit_retry_item() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE op_state text;
BEGIN
    IF TG_OP='UPDATE' THEN
        RAISE EXCEPTION 'source cleanup retry item binding is immutable' USING ERRCODE='23514';
    END IF;
    SELECT op.state INTO op_state FROM public.source_cleanup_explicit_retry retry
    JOIN public.operation op ON op.id=retry.recovery_operation_id WHERE retry.id=OLD.retry_id;
    IF op_state IN ('queued','running') THEN
        RAISE EXCEPTION 'active source cleanup retry item binding is immutable' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
END $$;
CREATE TRIGGER source_cleanup_explicit_retry_item_immutable
BEFORE UPDATE OR DELETE ON public.source_cleanup_explicit_retry_item
FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_explicit_retry_item();


CREATE TABLE public.source_cleanup_recovery_state (
    original_operation_id uuid PRIMARY KEY REFERENCES public.operation(id) ON DELETE RESTRICT,
    revision uuid NOT NULL,
    returned_recovery_operation_id uuid NOT NULL,
    returned_application_attempt integer NOT NULL CHECK (returned_application_attempt > 0),
    returned_river_job_id bigint NOT NULL CHECK (returned_river_job_id > 0),
    returned_river_attempt integer NOT NULL CHECK (returned_river_attempt > 0),
    returned_claim_uuid uuid NOT NULL CHECK (returned_claim_uuid <> '00000000-0000-0000-0000-000000000000'::uuid),
    returned_boot_uuid uuid NOT NULL CHECK (returned_boot_uuid <> '00000000-0000-0000-0000-000000000000'::uuid),
    returned_delivery_token text NOT NULL CHECK (returned_delivery_token <> ''),
    proof jsonb NOT NULL CHECK (jsonb_typeof(proof)='object' AND proof <> '{}'::jsonb),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (original_operation_id,revision)
);

CREATE TABLE public.source_cleanup_recovery_item (
    original_operation_id uuid NOT NULL,
    item_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('never_started','uncertain','native_removed','absent_after_explicit_retry','original_native_completed')),
    started_at timestamptz,
    completed_at timestamptz,
    PRIMARY KEY (original_operation_id,item_id),
    FOREIGN KEY (original_operation_id) REFERENCES public.source_cleanup_recovery_state(original_operation_id) ON DELETE RESTRICT,
    FOREIGN KEY (item_id,original_operation_id) REFERENCES public.source_cleanup_item(id,operation_id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_recovery_item_shape CHECK (
      ((kind='never_started' AND started_at IS NULL AND completed_at IS NULL)
       OR (kind='uncertain' AND started_at IS NOT NULL AND completed_at IS NULL)
       OR (kind IN ('native_removed','absent_after_explicit_retry','original_native_completed')
           AND started_at IS NOT NULL AND completed_at IS NOT NULL AND completed_at >= started_at)) IS TRUE)
);

CREATE TABLE public.source_cleanup_explicit_retry_resolution (
    retry_id uuid NOT NULL REFERENCES public.source_cleanup_explicit_retry(id) ON DELETE RESTRICT,
    item_id uuid NOT NULL REFERENCES public.source_cleanup_item(id) ON DELETE RESTRICT,
    recovery_operation_id uuid NOT NULL REFERENCES public.operation(id) ON DELETE RESTRICT,
    application_attempt integer NOT NULL CHECK (application_attempt > 0),
    river_job_id bigint NOT NULL CHECK (river_job_id > 0),
    claim_uuid uuid NOT NULL CHECK (claim_uuid <> '00000000-0000-0000-0000-000000000000'::uuid),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    resolution_kind text CHECK (resolution_kind IN ('native_removed','absent_after_explicit_retry','original_native_completed')),
    PRIMARY KEY (retry_id,item_id),
    CONSTRAINT source_cleanup_retry_resolution_completion_shape CHECK (
        ((completed_at IS NULL AND resolution_kind IS NULL)
         OR (completed_at IS NOT NULL AND completed_at >= started_at AND resolution_kind IS NOT NULL)) IS TRUE)
);

ALTER TABLE public.source_cleanup_explicit_retry ADD COLUMN frontier_revision uuid;
ALTER TABLE public.source_cleanup_explicit_retry ADD CONSTRAINT source_cleanup_retry_frontier_revision_fkey
    FOREIGN KEY (original_operation_id,frontier_revision)
    REFERENCES public.source_cleanup_recovery_state(original_operation_id,revision)
    ON DELETE RESTRICT;

-- The transfer proof is frozen while the grant, incoming rows, and resolution
-- rows are all still present. The trigger consuming it runs later in the same
-- transaction, after the old grant and resolutions have been retired.
CREATE TABLE public.source_cleanup_frontier_transfer_proof (
    transaction_id bigint NOT NULL,
    original_operation_id uuid NOT NULL,
    recovery_operation_id uuid NOT NULL,
    proof jsonb NOT NULL CHECK (jsonb_typeof(proof)='object' AND proof <> '{}'::jsonb),
    PRIMARY KEY (transaction_id,original_operation_id,recovery_operation_id)
);

CREATE FUNCTION public.compute_source_cleanup_frontier_transfer_proof(p_original_operation_id uuid,
    p_recovery_operation_id uuid) RETURNS jsonb
LANGUAGE plpgsql VOLATILE AS $$
DECLARE
    grant_row public.source_cleanup_explicit_retry%ROWTYPE;
    recovery public.operation%ROWTYPE;
    returned_claim public.publication_execution_claim%ROWTYPE;
    original public.operation%ROWTYPE;
    original_claim uuid;
    incoming jsonb := '[]'::jsonb;
    resolutions jsonb := '[]'::jsonb;
    folded jsonb := '[]'::jsonb;
    item_row public.source_cleanup_item%ROWTYPE;
    current_item public.source_cleanup_recovery_item%ROWTYPE;
    resolution_row public.source_cleanup_explicit_retry_resolution%ROWTYPE;
    have_current boolean;
    found_resolution boolean;
    item_kind text;
    item_started timestamptz;
    item_completed timestamptz;
    compatible boolean;
    result jsonb;
BEGIN
    IF p_original_operation_id IS NULL OR p_recovery_operation_id IS NULL THEN
        RAISE EXCEPTION 'source cleanup frontier proof requires both operation identities' USING ERRCODE='23514';
    END IF;
    SELECT * INTO recovery FROM public.operation WHERE id=p_recovery_operation_id;
    IF NOT FOUND OR recovery.kind <> 'retry_publication_sources_v1' OR recovery.state <> 'failed'
       OR recovery.stage <> 'cleanup_retry_recovery_required' OR recovery.attempt <= 0 OR recovery.river_job_id IS NULL THEN
        RAISE EXCEPTION 'source cleanup frontier proof requires an exact failed recovery operation' USING ERRCODE='23514';
    END IF;
    IF recovery.input_snapshot->>'original_operation_id' IS DISTINCT FROM p_original_operation_id::text THEN
        RAISE EXCEPTION 'source cleanup recovery snapshot names another original operation' USING ERRCODE='23514';
    END IF;
    SELECT * INTO returned_claim FROM public.publication_execution_claim
    WHERE operation_id=recovery.id AND application_attempt=recovery.attempt AND river_job_id=recovery.river_job_id
      AND state='recovery_required';
    IF NOT FOUND OR (SELECT count(*) FROM public.publication_execution_claim WHERE operation_id=recovery.id)<>1
       OR returned_claim.river_attempt <= 0 OR returned_claim.claim_uuid IS NULL
       OR returned_claim.boot_uuid IS NULL OR returned_claim.delivery_token IS NULL OR returned_claim.delivery_token='' THEN
        RAISE EXCEPTION 'source cleanup frontier proof requires an exact returned recovery claim' USING ERRCODE='23514';
    END IF;
    SELECT * INTO original FROM public.operation WHERE id=p_original_operation_id;
    IF NOT FOUND OR original.kind <> 'cleanup_publication_sources_v1' OR original.state <> 'failed'
       OR original.stage <> 'cleanup_recovery_required' OR original.safe_error IS NULL OR original.safe_error=''
       OR original.finished_at IS NULL OR original.river_job_id IS NULL THEN
        RAISE EXCEPTION 'source cleanup frontier proof requires a failed original cleanup' USING ERRCODE='23514';
    END IF;
    SELECT claim_uuid INTO original_claim FROM public.publication_execution_claim
    WHERE operation_id=original.id AND application_attempt=original.attempt AND river_job_id=original.river_job_id
      AND state='recovery_required';
    IF original_claim IS NULL OR (SELECT count(*) FROM public.publication_execution_claim WHERE operation_id=original.id)<>1 THEN
        RAISE EXCEPTION 'source cleanup frontier proof requires the original execution claim' USING ERRCODE='23514';
    END IF;
    IF recovery.input_snapshot->>'original_claim_uuid' IS DISTINCT FROM original_claim::text THEN
        RAISE EXCEPTION 'source cleanup recovery snapshot names another original claim' USING ERRCODE='23514';
    END IF;
    SELECT * INTO grant_row FROM public.source_cleanup_explicit_retry
    WHERE original_operation_id=p_original_operation_id AND recovery_operation_id=p_recovery_operation_id
      AND consumed_application_attempt=returned_claim.application_attempt
      AND consumed_river_job_id=returned_claim.river_job_id AND consumed_claim_uuid=returned_claim.claim_uuid;
    IF NOT FOUND OR grant_row.original_claim_uuid <> original_claim
       OR grant_row.original_application_attempt<>original.attempt OR grant_row.original_river_job_id<>original.river_job_id
       OR grant_row.position_id::text IS DISTINCT FROM original.input_snapshot->>'position_id'
       OR grant_row.epoch::text IS DISTINCT FROM original.input_snapshot->>'output_epoch' THEN
        RAISE EXCEPTION 'source cleanup frontier proof requires the exact consumed grant' USING ERRCODE='23514';
    END IF;
    SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY i.item_id),'[]'::jsonb) INTO incoming
    FROM public.source_cleanup_recovery_item i WHERE i.original_operation_id=p_original_operation_id;
    IF grant_row.frontier_revision IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM public.source_cleanup_recovery_state s WHERE s.original_operation_id=p_original_operation_id
          AND s.revision=grant_row.frontier_revision AND jsonb_typeof(s.proof->'folded_items')='array'
          AND s.proof->'folded_items'=incoming
    ) THEN
        RAISE EXCEPTION 'source cleanup grant pins a stale or mismatched frontier revision' USING ERRCODE='23514';
    END IF;
    IF grant_row.frontier_revision IS NULL AND EXISTS (
        SELECT 1 FROM public.source_cleanup_recovery_state WHERE original_operation_id=p_original_operation_id
    ) THEN
        RAISE EXCEPTION 'current source cleanup frontier cannot be transferred through an unpinned grant' USING ERRCODE='23514';
    END IF;
    IF jsonb_array_length(incoming)>0 AND (
        jsonb_array_length(incoming)<>(SELECT count(*) FROM public.source_cleanup_item WHERE operation_id=p_original_operation_id)
        OR EXISTS (SELECT 1 FROM public.source_cleanup_item i WHERE i.operation_id=p_original_operation_id
            AND NOT EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item r
                WHERE r.original_operation_id=p_original_operation_id AND r.item_id=i.id))
    ) THEN
        RAISE EXCEPTION 'incoming source cleanup frontier does not exactly cover original items' USING ERRCODE='23514';
    END IF;
    SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.item_id),'[]'::jsonb) INTO resolutions
    FROM public.source_cleanup_explicit_retry_resolution r
    WHERE r.retry_id=grant_row.id AND r.recovery_operation_id=p_recovery_operation_id
      AND r.application_attempt=returned_claim.application_attempt AND r.river_job_id=returned_claim.river_job_id
      AND r.claim_uuid=returned_claim.claim_uuid;

    FOR item_row IN SELECT * FROM public.source_cleanup_item WHERE operation_id=p_original_operation_id ORDER BY id LOOP
        SELECT * INTO current_item FROM public.source_cleanup_recovery_item
        WHERE original_operation_id=p_original_operation_id AND item_id=item_row.id;
        have_current := FOUND;
        IF have_current THEN
            item_kind := current_item.kind;
            item_started := current_item.started_at;
            item_completed := current_item.completed_at;
        ELSE
            item_kind := 'never_started';
            item_started := NULL;
            item_completed := NULL;
            IF item_row.mutation_claim_uuid=original_claim AND item_row.mutation_started_at IS NOT NULL THEN
                item_kind := 'uncertain';
                item_started := item_row.mutation_started_at;
                IF item_row.mutation_completed_at IS NOT NULL AND item_row.mutation_completed_at >= item_row.mutation_started_at THEN
                    item_kind := 'original_native_completed';
                    item_completed := item_row.mutation_completed_at;
                END IF;
            END IF;
        END IF;

        SELECT * INTO resolution_row FROM public.source_cleanup_explicit_retry_resolution
        WHERE retry_id=grant_row.id AND item_id=item_row.id AND recovery_operation_id=p_recovery_operation_id
          AND application_attempt=returned_claim.application_attempt AND river_job_id=returned_claim.river_job_id
          AND claim_uuid=returned_claim.claim_uuid;
        found_resolution := FOUND;
        IF found_resolution THEN
            IF resolution_row.completed_at IS NULL THEN
                IF item_kind NOT IN ('original_native_completed','native_removed','absent_after_explicit_retry') THEN
                    item_kind := 'uncertain';
                    item_started := resolution_row.started_at;
                    item_completed := NULL;
                END IF;
            ELSE
                compatible := (item_kind='original_native_completed' AND resolution_row.resolution_kind='original_native_completed')
                    OR (item_kind='native_removed' AND resolution_row.resolution_kind='native_removed')
                    OR (item_kind='absent_after_explicit_retry' AND resolution_row.resolution_kind='absent_after_explicit_retry')
                    OR (item_kind='uncertain' AND resolution_row.resolution_kind IN ('native_removed','absent_after_explicit_retry'))
                    OR (item_kind='never_started' AND resolution_row.resolution_kind='native_removed');
                IF NOT coalesce(compatible,false) THEN
                    RAISE EXCEPTION 'completed source cleanup resolution conflicts with incoming frontier' USING ERRCODE='23514';
                END IF;
                item_kind := resolution_row.resolution_kind;
                item_started := resolution_row.started_at;
                item_completed := resolution_row.completed_at;
            END IF;
        END IF;
        folded := folded || jsonb_build_array(jsonb_build_object(
            'original_operation_id',p_original_operation_id,'item_id',item_row.id,'kind',item_kind,
            'started_at',item_started,'completed_at',item_completed));
    END LOOP;
    IF jsonb_array_length(folded)=0 OR (jsonb_array_length(incoming)>0 AND jsonb_array_length(incoming)<>jsonb_array_length(folded)) THEN
        RAISE EXCEPTION 'source cleanup frontier proof has incomplete item coverage' USING ERRCODE='23514';
    END IF;
    result := jsonb_build_object(
        'version',1,'original_binding',grant_row.binding,'incoming_items',incoming,
        'latest_resolutions',resolutions,'folded_items',folded,
        'returned_recovery_operation_id',p_recovery_operation_id,
        'returned_application_attempt',returned_claim.application_attempt,
        'returned_river_job_id',returned_claim.river_job_id,
        'returned_river_attempt',returned_claim.river_attempt,
        'returned_claim_uuid',returned_claim.claim_uuid,
        'returned_boot_uuid',returned_claim.boot_uuid,
        'returned_delivery_token',returned_claim.delivery_token);
    RETURN result;
END $$;

CREATE FUNCTION public.source_cleanup_frontier_transfer_proof(p_original_operation_id uuid,
    p_recovery_operation_id uuid) RETURNS jsonb
LANGUAGE plpgsql VOLATILE AS $$
DECLARE canonical jsonb;
BEGIN
  canonical := public.compute_source_cleanup_frontier_transfer_proof(p_original_operation_id,p_recovery_operation_id);
  INSERT INTO public.source_cleanup_frontier_transfer_proof(transaction_id,original_operation_id,recovery_operation_id,proof)
  VALUES (txid_current(),p_original_operation_id,p_recovery_operation_id,canonical);
  RETURN canonical;
END $$;

CREATE FUNCTION public.validate_source_cleanup_frontier_transfer_proof() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE canonical jsonb;
BEGIN
  IF NEW.transaction_id IS DISTINCT FROM txid_current() THEN
    RAISE EXCEPTION 'source cleanup transfer proof is transaction-scoped' USING ERRCODE='23514';
  END IF;
  canonical := public.compute_source_cleanup_frontier_transfer_proof(NEW.original_operation_id,NEW.recovery_operation_id);
  IF NEW.proof IS DISTINCT FROM canonical THEN
    RAISE EXCEPTION 'source cleanup transfer proof is not canonical' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER source_cleanup_frontier_transfer_proof_canonical
BEFORE INSERT OR UPDATE ON public.source_cleanup_frontier_transfer_proof
FOR EACH ROW EXECUTE FUNCTION public.validate_source_cleanup_frontier_transfer_proof();

CREATE FUNCTION public.protect_source_cleanup_recovery_state() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE staged jsonb;
BEGIN
  IF TG_OP='DELETE' THEN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry WHERE original_operation_id=OLD.original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item WHERE original_operation_id=OLD.original_operation_id) THEN
      RAISE EXCEPTION 'pinned source cleanup frontier cannot be deleted' USING ERRCODE='23514';
    END IF;
    RETURN OLD;
  END IF;
  IF TG_OP='UPDATE' AND ROW(NEW.original_operation_id,NEW.revision,NEW.returned_recovery_operation_id,
      NEW.returned_application_attempt,NEW.returned_river_job_id,NEW.returned_river_attempt,
      NEW.returned_claim_uuid,NEW.returned_boot_uuid,NEW.returned_delivery_token,NEW.proof)
     IS NOT DISTINCT FROM ROW(OLD.original_operation_id,OLD.revision,OLD.returned_recovery_operation_id,
       OLD.returned_application_attempt,OLD.returned_river_job_id,OLD.returned_river_attempt,
       OLD.returned_claim_uuid,OLD.returned_boot_uuid,OLD.returned_delivery_token,OLD.proof) THEN
    RETURN NEW;
  END IF;
  IF TG_OP='UPDATE' AND (NEW.revision IS NOT DISTINCT FROM OLD.revision
      OR EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry
          WHERE original_operation_id=OLD.original_operation_id)) THEN
    RAISE EXCEPTION 'source cleanup frontier replacement requires a fresh revision and retired outgoing grant' USING ERRCODE='23514';
  END IF;
  SELECT proof INTO staged FROM public.source_cleanup_frontier_transfer_proof
    WHERE transaction_id=txid_current() AND original_operation_id=NEW.original_operation_id
      AND recovery_operation_id=NEW.returned_recovery_operation_id;
  IF staged IS NULL OR NEW.proof IS DISTINCT FROM staged
     OR NEW.returned_recovery_operation_id IS DISTINCT FROM (staged->>'returned_recovery_operation_id')::uuid
     OR NEW.returned_application_attempt IS DISTINCT FROM (staged->>'returned_application_attempt')::integer
     OR NEW.returned_river_job_id IS DISTINCT FROM (staged->>'returned_river_job_id')::bigint
     OR NEW.returned_river_attempt IS DISTINCT FROM (staged->>'returned_river_attempt')::integer
     OR NEW.returned_claim_uuid IS DISTINCT FROM (staged->>'returned_claim_uuid')::uuid
     OR NEW.returned_boot_uuid IS DISTINCT FROM (staged->>'returned_boot_uuid')::uuid
     OR NEW.returned_delivery_token IS DISTINCT FROM (staged->>'returned_delivery_token') THEN
    RAISE EXCEPTION 'source cleanup recovery state requires the canonical transfer proof' USING ERRCODE='23514';
  END IF;
  IF NEW.revision IS NULL OR NEW.revision='00000000-0000-0000-0000-000000000000'::uuid THEN
    RAISE EXCEPTION 'source cleanup recovery state revision is required' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER source_cleanup_recovery_state_protected BEFORE INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_state FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_recovery_state();

CREATE FUNCTION public.protect_source_cleanup_recovery_item() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE proof_row jsonb;
        op_state text;
        expected jsonb;
        old_expected jsonb;
BEGIN
  IF TG_OP='UPDATE' AND ROW(NEW.original_operation_id,NEW.item_id) IS DISTINCT FROM ROW(OLD.original_operation_id,OLD.item_id) THEN
    RAISE EXCEPTION 'source cleanup recovery item identity is immutable' USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' THEN
    SELECT proof INTO proof_row FROM public.source_cleanup_recovery_state WHERE original_operation_id=OLD.original_operation_id;
    SELECT op.state INTO op_state FROM public.operation op WHERE op.id=OLD.original_operation_id;
    IF EXISTS (SELECT 1 FROM public.source_cleanup_frontier_transfer_proof p
      WHERE p.transaction_id=txid_current() AND p.original_operation_id=OLD.original_operation_id
        AND EXISTS (SELECT 1 FROM jsonb_array_elements(p.proof->'incoming_items') AS e(value) WHERE e.value=to_jsonb(OLD)))
       OR NOT EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry WHERE original_operation_id=OLD.original_operation_id) THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'source cleanup recovery item cannot be deleted outside verified replacement or terminal retirement' USING ERRCODE='23514';
  END IF;
  SELECT proof INTO proof_row FROM public.source_cleanup_recovery_state WHERE original_operation_id=NEW.original_operation_id;
  SELECT e.value INTO expected FROM jsonb_array_elements(proof_row->'folded_items') AS e(value)
    WHERE e.value->>'original_operation_id'=NEW.original_operation_id::text AND e.value->>'item_id'=NEW.item_id::text;
  IF expected IS NULL OR expected IS DISTINCT FROM to_jsonb(NEW) THEN
    RAISE EXCEPTION 'source cleanup recovery item must exactly match the verified folded frontier' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' THEN
    SELECT e.value INTO old_expected FROM jsonb_array_elements(proof_row->'incoming_items') AS e(value)
      WHERE e.value->>'original_operation_id'=OLD.original_operation_id::text AND e.value->>'item_id'=OLD.item_id::text;
    IF old_expected IS NULL THEN
      RAISE EXCEPTION 'source cleanup recovery item update is not a verified replacement' USING ERRCODE='23514';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER source_cleanup_recovery_item_protected BEFORE INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_item FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_recovery_item();

CREATE FUNCTION public.protect_source_cleanup_retry_resolution() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
        grant_row public.source_cleanup_explicit_retry%ROWTYPE;
        recovery_state public.source_cleanup_recovery_state%ROWTYPE;
        staged jsonb;
        folded_item jsonb;
        retry_state text;
BEGIN
    IF TG_OP='DELETE' THEN
        SELECT * INTO grant_row FROM public.source_cleanup_explicit_retry WHERE id=OLD.retry_id;
        SELECT * INTO recovery_state FROM public.source_cleanup_recovery_state WHERE original_operation_id=grant_row.original_operation_id;
        SELECT proof INTO staged FROM public.source_cleanup_frontier_transfer_proof
          WHERE transaction_id=txid_current() AND original_operation_id=grant_row.original_operation_id
            AND recovery_operation_id=grant_row.recovery_operation_id;
        IF FOUND AND grant_row.consumed_application_attempt=OLD.application_attempt
           AND grant_row.consumed_river_job_id=OLD.river_job_id AND grant_row.consumed_claim_uuid=OLD.claim_uuid
           AND staged IS NOT NULL AND staged->'original_binding'=grant_row.binding
           AND EXISTS (SELECT 1 FROM jsonb_array_elements(staged->'latest_resolutions') AS e(value) WHERE e.value=to_jsonb(OLD)) THEN
             SELECT e.value INTO folded_item FROM jsonb_array_elements(staged->'folded_items') AS e(value) WHERE e.value->>'item_id'=OLD.item_id::text;
            IF OLD.completed_at IS NULL THEN
                IF folded_item IS NOT NULL AND folded_item->>'kind' IN ('uncertain','native_removed','absent_after_explicit_retry','original_native_completed') THEN RETURN OLD; END IF;
            ELSIF OLD.resolution_kind='original_native_completed' AND folded_item->>'kind'='original_native_completed'
               OR OLD.resolution_kind='native_removed' AND folded_item->>'kind'='native_removed'
               OR OLD.resolution_kind='absent_after_explicit_retry' AND folded_item->>'kind'='absent_after_explicit_retry' THEN
                RETURN OLD;
            END IF;
        END IF;
        IF OLD.completed_at IS NULL OR OLD.resolution_kind IS NULL THEN
            RAISE EXCEPTION 'active source cleanup retry resolution start is immutable outside verified replacement' USING ERRCODE='23514';
        END IF;
        SELECT operation.state INTO retry_state
        FROM public.source_cleanup_explicit_retry retry_grant
        JOIN public.operation ON operation.id=retry_grant.recovery_operation_id WHERE retry_grant.id=OLD.retry_id;
        IF NOT FOUND OR retry_state IS NULL OR retry_state IN ('queued','running') THEN
            RAISE EXCEPTION 'source cleanup retry resolution owner is unavailable' USING ERRCODE='23514';
        END IF;
        RETURN OLD;
    END IF;
    IF ROW(NEW.retry_id, NEW.item_id, NEW.recovery_operation_id, NEW.application_attempt,
        NEW.river_job_id, NEW.claim_uuid, NEW.started_at)
       IS DISTINCT FROM ROW(OLD.retry_id, OLD.item_id, OLD.recovery_operation_id, OLD.application_attempt,
        OLD.river_job_id, OLD.claim_uuid, OLD.started_at) THEN
        RAISE EXCEPTION 'source cleanup retry resolution identity and start are immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.completed_at IS NOT NULL OR OLD.resolution_kind IS NOT NULL THEN
        RAISE EXCEPTION 'completed source cleanup retry resolution is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.completed_at IS NULL OR NEW.resolution_kind IS NULL THEN
        RAISE EXCEPTION 'source cleanup retry resolution completion must be atomic' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER source_cleanup_retry_resolution_immutable
BEFORE UPDATE OR DELETE ON public.source_cleanup_explicit_retry_resolution
FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_retry_resolution();

CREATE FUNCTION public.check_source_cleanup_recovery_coverage() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE op_id uuid;
        state_row public.source_cleanup_recovery_state%ROWTYPE;
        grant_count integer;
        op_state text;
BEGIN
   IF TG_OP='DELETE' THEN op_id:=OLD.original_operation_id; ELSE op_id:=NEW.original_operation_id; END IF;
   SELECT * INTO state_row FROM public.source_cleanup_recovery_state WHERE original_operation_id=op_id;
   IF NOT FOUND THEN
     IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry WHERE original_operation_id=op_id AND frontier_revision IS NOT NULL) THEN
       RAISE EXCEPTION 'source cleanup grant cannot pin a missing current frontier' USING ERRCODE='23514';
      END IF;
     IF EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item WHERE original_operation_id=op_id)
        OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state WHERE original_operation_id=op_id) THEN
       RAISE EXCEPTION 'source cleanup frontier retirement is incomplete' USING ERRCODE='23514';
      END IF;
      IF TG_TABLE_NAME='source_cleanup_explicit_retry' THEN
        IF TG_OP='DELETE' THEN
          IF OLD.frontier_revision IS NULL THEN RETURN NULL; END IF;
        ELSE
          IF NEW.frontier_revision IS NULL THEN RETURN NULL; END IF;
        END IF;
      END IF;
      SELECT state INTO op_state FROM public.operation WHERE id=op_id;
     IF op_state IS DISTINCT FROM 'succeeded' THEN
       RAISE EXCEPTION 'source cleanup frontier can only retire with successful original settlement' USING ERRCODE='23514';
     END IF;
     RETURN NULL;
   END IF;
   SELECT count(*) INTO grant_count FROM public.source_cleanup_explicit_retry WHERE original_operation_id=op_id;
   IF grant_count<>1 OR EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry WHERE original_operation_id=op_id
       AND (frontier_revision IS NULL OR frontier_revision IS DISTINCT FROM state_row.revision)) THEN
     RAISE EXCEPTION 'current source cleanup frontier must be pinned by its exact grant revision' USING ERRCODE='23514';
   END IF;
   IF (SELECT count(*) FROM public.source_cleanup_item WHERE operation_id=op_id)=0
      OR (SELECT count(*) FROM public.source_cleanup_recovery_item WHERE original_operation_id=op_id)
         <>(SELECT count(*) FROM public.source_cleanup_item WHERE operation_id=op_id)
      OR EXISTS (SELECT 1 FROM public.source_cleanup_item i LEFT JOIN public.source_cleanup_recovery_item r
         ON r.original_operation_id=i.operation_id AND r.item_id=i.id
         WHERE i.operation_id=op_id AND r.item_id IS NULL)
      OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item r
          WHERE r.original_operation_id=op_id AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(state_row.proof->'folded_items') AS f(value)
             WHERE f.value=to_jsonb(r))) THEN
     RAISE EXCEPTION 'source cleanup recovery frontier must exactly cover and match its folded proof' USING ERRCODE='23514';
   END IF;
   RETURN NULL;
END $$;

CREATE FUNCTION public.protect_source_cleanup_retry_frontier() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.frontier_revision IS DISTINCT FROM OLD.frontier_revision THEN
    RAISE EXCEPTION 'source cleanup retry frontier revision is immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER source_cleanup_retry_frontier_immutable BEFORE UPDATE ON public.source_cleanup_explicit_retry
FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_retry_frontier();

CREATE FUNCTION public.finish_source_cleanup_frontier_transfer_proof() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state s
      JOIN public.source_cleanup_explicit_retry g ON g.original_operation_id=s.original_operation_id
        AND g.frontier_revision=s.revision
      WHERE s.original_operation_id=NEW.original_operation_id AND s.returned_recovery_operation_id=NEW.recovery_operation_id
        AND g.recovery_operation_id<>s.returned_recovery_operation_id
        AND s.proof=NEW.proof) THEN
    RAISE EXCEPTION 'source cleanup transfer proof was not consumed by its current pinned frontier' USING ERRCODE='23514';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item i
      WHERE i.original_operation_id=NEW.original_operation_id)
      OR EXISTS (SELECT 1 FROM jsonb_array_elements(NEW.proof->'folded_items') AS f(value)
        WHERE NOT EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item i
          WHERE i.original_operation_id=NEW.original_operation_id AND to_jsonb(i)=f.value)) THEN
    RAISE EXCEPTION 'source cleanup transfer proof did not produce its complete folded frontier' USING ERRCODE='23514';
  END IF;
  DELETE FROM public.source_cleanup_frontier_transfer_proof WHERE transaction_id=NEW.transaction_id
    AND original_operation_id=NEW.original_operation_id AND recovery_operation_id=NEW.recovery_operation_id;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER source_cleanup_recovery_state_coverage AFTER INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_state DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();
CREATE CONSTRAINT TRIGGER source_cleanup_recovery_item_coverage AFTER INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_item DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();
CREATE CONSTRAINT TRIGGER source_cleanup_retry_grant_frontier_coverage AFTER INSERT OR UPDATE OR DELETE
ON public.source_cleanup_explicit_retry DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();
CREATE CONSTRAINT TRIGGER source_cleanup_frontier_transfer_proof_consumed
AFTER INSERT ON public.source_cleanup_frontier_transfer_proof DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.finish_source_cleanup_frontier_transfer_proof();

-- Former migration: 20261027000001_source_cleanup_completion.tx.up.sql
CREATE TABLE public.source_cleanup_completion (
    work_id uuid PRIMARY KEY,
    source_root_id uuid NOT NULL REFERENCES public.source_root(id) ON DELETE CASCADE,
    source_location_id uuid NOT NULL,
    media_variant_id uuid NOT NULL REFERENCES public.media_variant(id) ON DELETE CASCADE,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    mtime timestamptz NOT NULL,
    kind text NOT NULL CHECK (kind IN ('native_removed', 'absent_after_explicit_retry')),
    handled_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

COMMENT ON TABLE public.source_cleanup_completion IS
    'Current root-owned namespace exclusion, retained until an accepted observation of its relative path; snapshot identities do not assert live work.';

-- Former migration: 20261028000001_current_source_cleanup_request.tx.up.sql
CREATE TABLE publication_source_cleanup_request (
    originating_operation_id uuid PRIMARY KEY REFERENCES operation(id) ON DELETE RESTRICT,
    position_id uuid NOT NULL REFERENCES release_track(id) ON DELETE CASCADE,
    output_epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_attempted_at timestamptz,
    CONSTRAINT publication_source_cleanup_request_position_epoch_key UNIQUE (position_id, output_epoch),
    CONSTRAINT publication_source_cleanup_request_nonzero_witness CHECK (
        output_epoch <> '00000000-0000-0000-0000-000000000000'::uuid AND
        publication_generation <> '00000000-0000-0000-0000-000000000000'::uuid
    )
);

CREATE INDEX publication_source_cleanup_request_position_idx
    ON publication_source_cleanup_request(position_id);

-- Former migration: 20261029000001_release_cover_authority.tx.up.sql
CREATE TABLE public.release_publication_cover (
    release_id uuid NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    mode text NOT NULL DEFAULT 'automatic',
    selected_generation uuid,
    selected_bytes bytea,
    selected_mime text,
    selected_sha256 bytea,
    selected_size bigint,
    pending_generation uuid,
    pending_bytes bytea,
    pending_mime text,
    pending_sha256 bytea,
    pending_size bigint,
    acquisition_owner uuid,
    warning text,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT release_publication_cover_pkey PRIMARY KEY (release_id),
    CONSTRAINT release_publication_cover_release_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE RESTRICT,
    CONSTRAINT release_publication_cover_revision_check CHECK (revision > 0),
    CONSTRAINT release_publication_cover_mode_check CHECK (mode IN ('automatic', 'manual')),
    CONSTRAINT release_publication_cover_acquisition_owner_check CHECK (
        (acquisition_owner IS NULL OR acquisition_owner <> '00000000-0000-0000-0000-000000000000') IS TRUE
    ),
    CONSTRAINT release_publication_cover_acquisition_mode_check CHECK (
        (acquisition_owner IS NULL OR mode = 'automatic') IS TRUE
    ),
    CONSTRAINT release_publication_cover_selected_tuple_check CHECK ((
        (selected_generation IS NULL AND selected_bytes IS NULL AND selected_mime IS NULL AND selected_sha256 IS NULL AND selected_size IS NULL)
        OR (selected_generation IS NOT NULL AND selected_generation <> '00000000-0000-0000-0000-000000000000'
            AND selected_bytes IS NOT NULL AND octet_length(selected_bytes) > 0
            AND selected_mime IN ('image/png', 'image/jpeg') AND selected_sha256 IS NOT NULL AND octet_length(selected_sha256) = 32
            AND selected_sha256 = sha256(selected_bytes)
            AND selected_size = octet_length(selected_bytes) AND selected_size > 0)
    ) IS TRUE),
    CONSTRAINT release_publication_cover_pending_tuple_check CHECK ((
        (pending_generation IS NULL AND pending_bytes IS NULL AND pending_mime IS NULL AND pending_sha256 IS NULL AND pending_size IS NULL)
        OR (pending_generation IS NOT NULL AND pending_generation <> '00000000-0000-0000-0000-000000000000'
            AND pending_bytes IS NOT NULL AND octet_length(pending_bytes) > 0
            AND pending_mime IN ('image/png', 'image/jpeg') AND pending_sha256 IS NOT NULL AND octet_length(pending_sha256) = 32
            AND pending_sha256 = sha256(pending_bytes)
            AND pending_size = octet_length(pending_bytes) AND pending_size > 0)
    ) IS TRUE),
    CONSTRAINT release_publication_cover_manual_pending_check CHECK (
        (pending_generation IS NULL OR mode = 'manual') IS TRUE
    ),
    CONSTRAINT release_publication_cover_warning_check CHECK (
        warning IS NULL OR length(warning) > 0
    )
);

CREATE INDEX release_publication_cover_acquisition_idx
    ON public.release_publication_cover(release_id) WHERE acquisition_owner IS NOT NULL;

-- Former migration: 20261029000002_position_source_authority.tx.up.sql
CREATE TABLE position_source_authority (
    position_id uuid PRIMARY KEY REFERENCES release_track(id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('manual', 'automatic')),
    revision uuid NOT NULL,
    publication_epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    source_location_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Former migration: 20261029000003_publication_attempt_cover_input.tx.up.sql
ALTER TABLE public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_operation_position_key UNIQUE (operation_id, position_id);
ALTER TABLE public.release_track
    ADD CONSTRAINT release_track_id_release_key UNIQUE (id, release_id);

CREATE TABLE public.publication_attempt_cover_input (
    operation_id uuid NOT NULL,
    position_id uuid NOT NULL,
    release_id uuid NOT NULL,
    authority_revision bigint NOT NULL,
    authority_mode text NOT NULL,
    state text NOT NULL,
    generation uuid,
    image_bytes bytea,
    image_mime text,
    image_sha256 bytea,
    image_size bigint,
    CONSTRAINT publication_attempt_cover_input_pkey PRIMARY KEY (operation_id),
    CONSTRAINT publication_attempt_cover_input_attempt_fkey FOREIGN KEY (operation_id, position_id)
        REFERENCES public.track_publication_attempt(operation_id, position_id) ON DELETE CASCADE,
    CONSTRAINT publication_attempt_cover_input_release_position_fkey FOREIGN KEY (position_id, release_id)
        REFERENCES public.release_track(id, release_id) ON DELETE RESTRICT,
    CONSTRAINT publication_attempt_cover_input_revision_check CHECK (authority_revision > 0),
    CONSTRAINT publication_attempt_cover_input_mode_check CHECK (authority_mode IN ('automatic', 'manual')),
    CONSTRAINT publication_attempt_cover_input_state_check CHECK (state IN ('absent', 'value')),
    CONSTRAINT publication_attempt_cover_input_tuple_check CHECK ((
        (state = 'absent' AND generation IS NULL AND image_bytes IS NULL AND image_mime IS NULL AND image_sha256 IS NULL AND image_size IS NULL)
        OR
        (state = 'value' AND generation IS NOT NULL AND generation <> '00000000-0000-0000-0000-000000000000'
            AND image_bytes IS NOT NULL AND octet_length(image_bytes) > 0
            AND image_mime IN ('image/png', 'image/jpeg')
            AND image_sha256 IS NOT NULL AND octet_length(image_sha256) = 32
            AND image_sha256 = sha256(image_bytes)
            AND image_size = octet_length(image_bytes) AND image_size > 0)
    ) IS TRUE)
);

CREATE FUNCTION public.reject_publication_attempt_cover_input_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'publication attempt cover inputs are immutable';
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.track_publication_attempt
        WHERE operation_id = OLD.operation_id AND position_id = OLD.position_id
    ) THEN
        RAISE EXCEPTION 'cannot delete publication attempt cover input while its attempt exists';
    END IF;
    RETURN OLD;
END
$$;

CREATE TRIGGER publication_attempt_cover_input_immutable
    BEFORE DELETE OR UPDATE ON public.publication_attempt_cover_input
    FOR EACH ROW EXECUTE FUNCTION public.reject_publication_attempt_cover_input_mutation();

-- Former migration: 20261030000001_failed_publication_tool_holds.tx.up.sql


-- Former migration: 20261030000002_publication_cover_embedding.tx.up.sql
ALTER TABLE public.track_publication
    ADD COLUMN cover_embedding jsonb;

ALTER TABLE public.track_publication
    ADD CONSTRAINT track_publication_cover_embedding_projection_check
    CHECK (
        cover_embedding IS NULL OR (
            (jsonb_typeof(cover_embedding) = 'object') IS TRUE
            AND (cover_embedding ?& ARRAY[
                'version', 'generation', 'receipt_operation_id', 'receipt_release_id',
                'receipt_revision', 'receipt_mode', 'requested_generation',
                'requested_sha256', 'outcome', 'warning_code'
            ]) IS TRUE
            AND (jsonb_typeof(cover_embedding->'version') = 'number') IS TRUE
            AND ((cover_embedding->>'version') = '1') IS TRUE
            AND (jsonb_typeof(cover_embedding->'generation') = 'string') IS TRUE
            AND ((cover_embedding->>'generation') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_operation_id') = 'string') IS TRUE
            AND ((cover_embedding->>'receipt_operation_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_release_id') = 'string') IS TRUE
            AND ((cover_embedding->>'receipt_release_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_revision') = 'number') IS TRUE
            AND ((cover_embedding->>'receipt_revision') ~ '^[1-9][0-9]*$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_mode') = 'string') IS TRUE
            AND ((cover_embedding->>'receipt_mode') IN ('automatic', 'manual')) IS TRUE
            AND (jsonb_typeof(cover_embedding->'requested_generation') = 'string') IS TRUE
            AND ((cover_embedding->>'requested_generation') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'requested_sha256') = 'string') IS TRUE
            AND ((cover_embedding->>'requested_sha256') ~ '^[0-9a-f]{64}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'outcome') = 'string') IS TRUE
            AND (jsonb_typeof(cover_embedding->'warning_code') = 'string') IS TRUE
            AND (
                ((cover_embedding->>'outcome') = 'verified' AND (cover_embedding->>'warning_code') = '')
                OR ((cover_embedding->>'outcome') = 'write_failed' AND (cover_embedding->>'warning_code') = 'cover_write_failed')
                OR ((cover_embedding->>'outcome') = 'read_failed' AND (cover_embedding->>'warning_code') = 'cover_read_failed')
                OR ((cover_embedding->>'outcome') = 'mismatch' AND (cover_embedding->>'warning_code') = 'cover_mismatch')
            ) IS TRUE
        )
    );

-- Former migration: 20261030000003_source_metadata_media_facts.tx.up.sql
ALTER TABLE public.media_metadata_result
    ADD COLUMN cover_observed boolean NOT NULL DEFAULT false,
    ADD COLUMN cover_data bytea,
    ADD COLUMN cover_mime text,
    ADD COLUMN cover_sha256 bytea,
    ADD COLUMN cover_size_bytes bigint,
    ADD COLUMN properties jsonb,
    ADD CONSTRAINT media_metadata_result_cover_facts_check CHECK (
        (
            cover_data IS NULL AND cover_mime IS NULL AND cover_sha256 IS NULL AND cover_size_bytes IS NULL
        ) OR (
            cover_data IS NOT NULL AND cover_mime IS NOT NULL AND cover_sha256 IS NOT NULL AND cover_size_bytes IS NOT NULL
            AND cover_observed IS TRUE
            AND cover_mime LIKE 'image/%'
            AND octet_length(cover_sha256) = 32
            AND cover_size_bytes = octet_length(cover_data)
        )
    );

ALTER TABLE public.track_source
    ADD COLUMN source_metadata_result_id uuid,
    ADD CONSTRAINT track_source_source_metadata_result_fk
        FOREIGN KEY (source_metadata_result_id)
        REFERENCES public.media_metadata_result(id) ON DELETE RESTRICT;

CREATE INDEX track_source_source_metadata_result_idx
    ON public.track_source(source_metadata_result_id)
    WHERE source_metadata_result_id IS NOT NULL;

-- Former migration: 20261030000004_preserve_original_cover_facts.tx.up.sql
ALTER TABLE public.release_publication_cover
    DROP CONSTRAINT release_publication_cover_selected_tuple_check,
    DROP CONSTRAINT release_publication_cover_pending_tuple_check,
    ADD CONSTRAINT release_publication_cover_selected_tuple_check CHECK ((
        (selected_generation IS NULL AND selected_bytes IS NULL AND selected_mime IS NULL AND selected_sha256 IS NULL AND selected_size IS NULL)
        OR (selected_generation IS NOT NULL AND selected_generation <> '00000000-0000-0000-0000-000000000000'
            AND selected_bytes IS NOT NULL AND octet_length(selected_bytes) > 0
            AND selected_mime IN ('image/png', 'image/jpeg', 'image/gif', 'image/tiff', 'image/webp', 'image/bmp')
            AND selected_sha256 IS NOT NULL AND octet_length(selected_sha256) = 32
            AND selected_sha256 = sha256(selected_bytes)
            AND selected_size = octet_length(selected_bytes) AND selected_size > 0)
    ) IS TRUE),
    ADD CONSTRAINT release_publication_cover_pending_tuple_check CHECK ((
        (pending_generation IS NULL AND pending_bytes IS NULL AND pending_mime IS NULL AND pending_sha256 IS NULL AND pending_size IS NULL)
        OR (pending_generation IS NOT NULL AND pending_generation <> '00000000-0000-0000-0000-000000000000'
            AND pending_bytes IS NOT NULL AND octet_length(pending_bytes) > 0
            AND pending_mime IN ('image/png', 'image/jpeg', 'image/gif', 'image/tiff', 'image/webp', 'image/bmp')
            AND pending_sha256 IS NOT NULL AND octet_length(pending_sha256) = 32
            AND pending_sha256 = sha256(pending_bytes)
            AND pending_size = octet_length(pending_bytes) AND pending_size > 0)
    ) IS TRUE);

ALTER TABLE public.publication_attempt_cover_input
    DROP CONSTRAINT publication_attempt_cover_input_tuple_check,
    ADD CONSTRAINT publication_attempt_cover_input_tuple_check CHECK ((
        (state = 'absent' AND generation IS NULL AND image_bytes IS NULL AND image_mime IS NULL AND image_sha256 IS NULL AND image_size IS NULL)
        OR
        (state = 'value' AND generation IS NOT NULL AND generation <> '00000000-0000-0000-0000-000000000000'
            AND image_bytes IS NOT NULL AND octet_length(image_bytes) > 0
            AND image_mime IN ('image/png', 'image/jpeg', 'image/gif', 'image/tiff', 'image/webp', 'image/bmp')
            AND image_sha256 IS NOT NULL AND octet_length(image_sha256) = 32
            AND image_sha256 = sha256(image_bytes)
            AND image_size = octet_length(image_bytes) AND image_size > 0)
    ) IS TRUE);

ALTER TABLE public.media_metadata_result
    ADD COLUMN cover_error text,
    ADD CONSTRAINT media_metadata_result_cover_error_check CHECK (
        cover_error IS NULL OR (length(cover_error) > 0 AND cover_observed IS TRUE AND cover_data IS NULL)
    );

ALTER TABLE public.track_publication
    DROP CONSTRAINT track_publication_cover_embedding_projection_check,
    ADD CONSTRAINT track_publication_cover_embedding_projection_check CHECK (
        cover_embedding IS NULL OR (
            (jsonb_typeof(cover_embedding) = 'object') IS TRUE
            AND (cover_embedding ?& ARRAY[
                'version', 'generation', 'receipt_operation_id', 'receipt_release_id',
                'receipt_revision', 'receipt_mode', 'requested_generation',
                'requested_sha256', 'outcome', 'warning_code'
            ]) IS TRUE
            AND (jsonb_typeof(cover_embedding->'version') = 'number') IS TRUE
            AND ((cover_embedding->>'version') IN ('1', '2')) IS TRUE
            AND (jsonb_typeof(cover_embedding->'generation') = 'string') IS TRUE
            AND ((cover_embedding->>'generation') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_operation_id') = 'string') IS TRUE
            AND ((cover_embedding->>'receipt_operation_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_release_id') = 'string') IS TRUE
            AND ((cover_embedding->>'receipt_release_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_revision') = 'number') IS TRUE
            AND ((cover_embedding->>'receipt_revision') ~ '^[1-9][0-9]*$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'receipt_mode') = 'string') IS TRUE
            AND ((cover_embedding->>'receipt_mode') IN ('automatic', 'manual')) IS TRUE
            AND (jsonb_typeof(cover_embedding->'requested_generation') = 'string') IS TRUE
            AND ((cover_embedding->>'requested_generation') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'requested_sha256') = 'string') IS TRUE
            AND ((cover_embedding->>'requested_sha256') ~ '^[0-9a-f]{64}$') IS TRUE
            AND (jsonb_typeof(cover_embedding->'outcome') = 'string') IS TRUE
            AND (jsonb_typeof(cover_embedding->'warning_code') = 'string') IS TRUE
            AND (
                ((cover_embedding->>'version') = '1'
                    AND NOT (cover_embedding ?| ARRAY['embedded_sha256', 'embedded_mime', 'embedded_size']))
                OR ((cover_embedding->>'version') = '2' AND (
                    ((cover_embedding->>'outcome') = 'verified'
                        AND (cover_embedding ?& ARRAY['embedded_sha256', 'embedded_mime', 'embedded_size'])
                        AND (jsonb_typeof(cover_embedding->'embedded_sha256') = 'string')
                        AND ((cover_embedding->>'embedded_sha256') ~ '^[0-9a-f]{64}$')
                        AND (jsonb_typeof(cover_embedding->'embedded_mime') = 'string')
                        AND ((cover_embedding->>'embedded_mime') = 'image/png')
                        AND (jsonb_typeof(cover_embedding->'embedded_size') = 'number')
                        AND ((cover_embedding->>'embedded_size') ~ '^[1-9][0-9]*$'))
                    OR ((cover_embedding->>'outcome') <> 'verified'
                        AND NOT (cover_embedding ?| ARRAY['embedded_sha256', 'embedded_mime', 'embedded_size']))
                ))
            ) IS TRUE
            AND (
                ((cover_embedding->>'outcome') = 'verified' AND (cover_embedding->>'warning_code') = '')
                OR ((cover_embedding->>'outcome') = 'write_failed' AND (cover_embedding->>'warning_code') = 'cover_write_failed')
                OR ((cover_embedding->>'outcome') = 'read_failed' AND (cover_embedding->>'warning_code') = 'cover_read_failed')
                OR ((cover_embedding->>'outcome') = 'mismatch' AND (cover_embedding->>'warning_code') = 'cover_mismatch')
            ) IS TRUE
        )
    );

-- Former migration: 20261030000005_obsolete_publication_output_cleanup.tx.up.sql
ALTER TABLE public.operation DROP CONSTRAINT operation_kind_check;
ALTER TABLE public.operation ADD CONSTRAINT operation_kind_check CHECK (kind = ANY (ARRAY[
    'install','activate','delete','move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
    'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
    'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1',
    'matching_apply_v1','publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1',
    'obsolete_publication_output_cleanup_v1'
]));
ALTER TABLE public.operation DROP CONSTRAINT operation_mutation_has_target_installation;
ALTER TABLE public.operation ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1','obsolete_publication_output_cleanup_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    state NOT IN ('queued','running') OR target_installation_id IS NOT NULL
);

ALTER TABLE public.publication_namespace_claim ADD CONSTRAINT publication_namespace_claim_cleanup_identity_unique
    UNIQUE (id, epoch, owner_position_id, relative_path, node_kind);

ALTER TABLE public.operation DROP CONSTRAINT operation_target_identity;
ALTER TABLE public.operation ADD CONSTRAINT operation_target_identity CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1','obsolete_publication_output_cleanup_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    COALESCE(input_snapshot ->> 'target_identity','') <> ''
);
ALTER TABLE public.operation ADD CONSTRAINT operation_obsolete_output_cleanup_shape_check CHECK ((
    kind <> 'obsolete_publication_output_cleanup_v1' OR (
        input_snapshot = jsonb_build_object('operation_id', id::text)
        AND source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL
        AND target_installation_id IS NULL AND target_source_root_id IS NULL AND target_source_location_id IS NULL
        AND provider_source_id IS NULL AND provider_configuration_identity IS NULL AND provider_cache_key IS NULL
        AND NOT rerun_target AND NOT tools_read_required AND NOT provider_explicit_refresh
    )
) IS TRUE);

CREATE TABLE public.obsolete_publication_output_cleanup (
    operation_id uuid NOT NULL,
    epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    generation uuid NOT NULL,
    relative_path text NOT NULL,
    native_path text NOT NULL,
    output_root_identity jsonb NOT NULL,
    parent_identity jsonb NOT NULL,
    namespace_claim_id uuid NOT NULL,
    claim_node_kind text GENERATED ALWAYS AS ('file'::text) STORED NOT NULL,
    file_size bigint NOT NULL,
    modified_at timestamp with time zone NOT NULL,
    sha256 bytea NOT NULL,
    state text NOT NULL,
    safe_error text,
    CONSTRAINT obsolete_publication_output_cleanup_pkey PRIMARY KEY (operation_id),
    CONSTRAINT obsolete_publication_output_cleanup_claim_unique UNIQUE (namespace_claim_id),
    CONSTRAINT obsolete_publication_output_cleanup_operation_fkey FOREIGN KEY (operation_id)
        REFERENCES public.operation(id) ON DELETE RESTRICT,
    CONSTRAINT obsolete_publication_output_cleanup_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT obsolete_publication_output_cleanup_claim_fkey FOREIGN KEY (namespace_claim_id, epoch, position_id, relative_path, claim_node_kind)
        REFERENCES public.publication_namespace_claim(id, epoch, owner_position_id, relative_path, node_kind) ON DELETE RESTRICT,
    CONSTRAINT obsolete_publication_output_cleanup_reservation_fkey FOREIGN KEY (namespace_claim_id, position_id, generation)
        REFERENCES public.publication_namespace_reservation(namespace_claim_id, position_id, generation) ON DELETE RESTRICT,
    CONSTRAINT obsolete_publication_output_cleanup_epoch_fkey FOREIGN KEY (epoch)
        REFERENCES public.publication_namespace_state(output_epoch) ON DELETE RESTRICT,
    CONSTRAINT obsolete_publication_output_cleanup_ids_check CHECK (
        operation_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND epoch <> '00000000-0000-0000-0000-000000000000'::uuid
        AND position_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND generation <> '00000000-0000-0000-0000-000000000000'::uuid
        AND namespace_claim_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT obsolete_publication_output_cleanup_path_check CHECK (
        relative_path <> '' AND relative_path !~ '(^/|\\|:|(^|/)\.\.?(/|$))' AND native_path <> ''
    ),
    CONSTRAINT obsolete_publication_output_cleanup_native_path_check CHECK (
        (native_path ~ '^/' OR native_path ~ '^[A-Za-z]:[/\\\\]') IS TRUE
    ),
    CONSTRAINT obsolete_publication_output_cleanup_directory_identities_check CHECK ((
        jsonb_typeof(output_root_identity) = 'object' AND jsonb_typeof(parent_identity) = 'object'
        AND output_root_identity ?& ARRAY['platform','volume','file_id']
        AND parent_identity ?& ARRAY['platform','volume','file_id']
        AND jsonb_typeof(output_root_identity->'platform') = 'string'
        AND jsonb_typeof(output_root_identity->'volume') = 'string'
        AND jsonb_typeof(output_root_identity->'file_id') = 'string'
        AND jsonb_typeof(parent_identity->'platform') = 'string'
        AND jsonb_typeof(parent_identity->'volume') = 'string'
        AND jsonb_typeof(parent_identity->'file_id') = 'string'
        AND output_root_identity->>'platform' <> '' AND output_root_identity->>'volume' <> '' AND output_root_identity->>'file_id' <> ''
        AND parent_identity->>'platform' <> '' AND parent_identity->>'volume' <> '' AND parent_identity->>'file_id' <> ''
        AND (output_root_identity->>'platform' = parent_identity->>'platform') IS TRUE
    ) IS TRUE),
    CONSTRAINT obsolete_publication_output_cleanup_size_check CHECK (file_size >= 0),
    CONSTRAINT obsolete_publication_output_cleanup_sha256_check CHECK (octet_length(sha256) = 32),
    CONSTRAINT obsolete_publication_output_cleanup_state_check CHECK (state = ANY (ARRAY['queued', 'running', 'failed'])),
    CONSTRAINT obsolete_publication_output_cleanup_error_check CHECK (
        (state = 'failed' AND safe_error IS NOT NULL AND safe_error <> '')
        OR (state <> 'failed' AND safe_error IS NULL)
    )
);

CREATE INDEX obsolete_publication_output_cleanup_state_idx
    ON public.obsolete_publication_output_cleanup(state);

-- Former migration: 20261030000006_publication_reconciliation_tool_reads.tx.up.sql
ALTER TABLE public.operation DROP CONSTRAINT operation_source_analysis_target_shape;
ALTER TABLE public.operation ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
    (kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1') AND source_analysis_mode IS NULL
        AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND target_installation_id IS NULL AND target_source_root_id IS NULL AND target_source_location_id IS NULL
        AND provider_source_id IS NULL AND provider_configuration_identity IS NULL AND provider_cache_key IS NULL
        AND NOT provider_explicit_refresh AND (NOT tools_read_required OR kind IN ('publish_track_v1','publication_reconcile_v1')))
    OR (kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1') AND (((source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND (NOT tools_read_required OR kind = 'scan_source'))
      OR (source_analysis_mode = 'batch' AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target)
      OR (source_analysis_mode = 'single_step' AND
          (((state IN ('queued','running') AND target_work_id IS NOT NULL AND target_step IN ('sha256','probe','fingerprint','metadata'))
            OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))
            AND (NOT rerun_target OR (state IN ('queued','running') AND target_step='fingerprint') OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))))) IS TRUE))
);

-- Former migration: 20261030000007_retire_publication_mutation_journal.tx.up.sql
LOCK TABLE public.track_publication_attempt IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.track_publication_attempt
        WHERE mutation_journal IS DISTINCT FROM '[]'::jsonb
           OR phase NOT IN ('queued', 'preparing', 'staged', 'validated', 'exposing')
    ) THEN
        RAISE EXCEPTION 'cannot retire publication mutation journal while attempts retain journal or retired phases';
    END IF;
END;
$$;

ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_mutation_journal_array_check,
    DROP CONSTRAINT track_publication_attempt_frozen_inputs_phase_check,
    DROP CONSTRAINT track_publication_attempt_phase_check,
    DROP COLUMN mutation_journal,
    ADD CONSTRAINT track_publication_attempt_phase_check
        CHECK (phase = ANY (ARRAY['queued', 'preparing', 'staged', 'validated', 'exposing'])),
    ADD CONSTRAINT track_publication_attempt_frozen_inputs_phase_check
        CHECK (frozen_inputs IS NOT NULL AND jsonb_typeof(frozen_inputs) = 'object');

-- Former migration: 20261031000001_canonical_tool_versions.tx.up.sql
DO $migration$
DECLARE
    installation record;
    attempt record;
    selection jsonb;
    migrated_tools jsonb;
    output jsonb;
    canonical_version text;
    executable_output text;
    executable_name text;
    ffmpeg_token text;
    ffprobe_token text;
    selected_token text;
    selected_output text;
    logical_executable text;
    banner_fields text[];
    evidence_fields text[];
BEGIN
    ALTER TABLE public.tool_installation RENAME COLUMN executable_versions TO executable_verification_output;
    ALTER TABLE public.tool_installation ADD COLUMN version text;

    FOR installation IN SELECT id, package_kind, platform_goos, state, executable_verification_output FROM public.tool_installation LOOP
        IF installation.state <> 'ready' THEN CONTINUE; END IF;
        output := installation.executable_verification_output;
        IF jsonb_typeof(output) IS DISTINCT FROM 'object' THEN
            RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: malformed verification output', installation.id;
        END IF;
        logical_executable := CASE WHEN installation.package_kind = 'ffmpeg' THEN 'ffmpeg' ELSE 'fpcalc' END;
        executable_name := logical_executable;
        IF installation.platform_goos = 'windows' THEN executable_name := executable_name || '.exe'; END IF;
        IF jsonb_typeof(output -> executable_name) IS DISTINCT FROM 'string' THEN
            RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: missing verification output for %', installation.id, executable_name;
        END IF;
        executable_output := output ->> executable_name;
        banner_fields := regexp_split_to_array(regexp_replace(split_part(executable_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
        IF coalesce(array_length(banner_fields, 1), 0) < 3 OR lower(banner_fields[1]) <> logical_executable OR lower(banner_fields[2]) <> 'version' OR banner_fields[3] = '' THEN
            RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: malformed verification output', installation.id;
        END IF;
        selected_token := banner_fields[3];
        IF installation.package_kind = 'ffmpeg' THEN
            executable_name := 'ffprobe' || CASE WHEN installation.platform_goos = 'windows' THEN '.exe' ELSE '' END;
            IF jsonb_typeof(output -> executable_name) IS DISTINCT FROM 'string' THEN
                RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: missing verification output for %', installation.id, executable_name;
            END IF;
            ffmpeg_token := selected_token;
            selected_output := output ->> executable_name;
            banner_fields := regexp_split_to_array(regexp_replace(split_part(selected_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(banner_fields, 1), 0) < 3 OR lower(banner_fields[1]) <> 'ffprobe' OR lower(banner_fields[2]) <> 'version' OR banner_fields[3] = '' THEN
                RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: malformed verification output for %', installation.id, executable_name;
            END IF;
            ffprobe_token := banner_fields[3];
            canonical_version := 'ffmpeg ' || ffmpeg_token || ' + ffprobe ' || ffprobe_token;
        ELSE
            canonical_version := selected_token;
        END IF;
        UPDATE public.tool_installation SET version = canonical_version WHERE id = installation.id;
    END LOOP;

    -- Publication inputs are retained execution authority. Rebind only when the
    -- exact installation, relative path, executable and captured raw banner agree.
    FOR attempt IN SELECT operation_id, frozen_inputs FROM public.track_publication_attempt
        WHERE frozen_inputs IS NOT NULL LOOP
        IF jsonb_typeof(attempt.frozen_inputs -> 'tools') IS DISTINCT FROM 'array' THEN
            RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: tools array is missing', attempt.operation_id;
        END IF;
        migrated_tools := '[]'::jsonb;
        FOR selection IN SELECT value FROM jsonb_array_elements(attempt.frozen_inputs -> 'tools') LOOP
            IF jsonb_typeof(selection) IS DISTINCT FROM 'object' OR
               jsonb_typeof(selection -> 'package_kind') IS DISTINCT FROM 'string' OR selection ->> 'package_kind' = '' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: selection package kind is missing', attempt.operation_id;
            END IF;
            IF selection ->> 'package_kind' <> 'ffmpeg' THEN
                migrated_tools := migrated_tools || jsonb_build_array(selection);
                CONTINUE;
            END IF;
            IF jsonb_typeof(selection -> 'installation_id') IS DISTINCT FROM 'string' OR selection ->> 'installation_id' = '' OR
               jsonb_typeof(selection -> 'relative_path') IS DISTINCT FROM 'string' OR selection ->> 'relative_path' = '' OR
               jsonb_typeof(selection -> 'version') IS DISTINCT FROM 'string' OR selection ->> 'version' = '' OR
               jsonb_typeof(selection -> 'version_banner') IS DISTINCT FROM 'string' OR selection ->> 'version_banner' = '' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: frozen selection identity and evidence must be nonempty strings', attempt.operation_id;
            END IF;
            SELECT i.version, i.relative_path, i.executable_verification_output, i.platform_goos
              INTO installation
              FROM public.tool_installation i
             WHERE i.id::text = selection ->> 'installation_id' AND i.package_kind = 'ffmpeg';
            IF NOT FOUND OR installation.version IS NULL OR installation.relative_path IS DISTINCT FROM selection ->> 'relative_path' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: installation identity or path differs', attempt.operation_id;
            END IF;
            executable_name := selection ->> 'executable';
            IF executable_name NOT IN ('ffmpeg', 'ffprobe') THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: unexpected executable', attempt.operation_id;
            END IF;
            IF installation.platform_goos = 'windows' THEN executable_name := executable_name || '.exe'; END IF;
            IF jsonb_typeof(installation.executable_verification_output -> executable_name) IS DISTINCT FROM 'string' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: raw executable evidence is missing', attempt.operation_id;
            END IF;
            selected_output := installation.executable_verification_output ->> executable_name;
            logical_executable := selection ->> 'executable';
            banner_fields := regexp_split_to_array(regexp_replace(split_part(selected_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(banner_fields, 1), 0) < 3 OR lower(banner_fields[1]) <> logical_executable OR lower(banner_fields[2]) <> 'version' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: malformed raw executable banner', attempt.operation_id;
            END IF;
            selected_token := banner_fields[3];
            ffmpeg_token := (regexp_match(installation.version, '^ffmpeg ([^ ]+) \+ ffprobe ([^ ]+)$'))[1];
            ffprobe_token := (regexp_match(installation.version, '^ffmpeg ([^ ]+) \+ ffprobe ([^ ]+)$'))[2];
            executable_output := installation.executable_verification_output ->> ('ffmpeg' || CASE WHEN installation.platform_goos = 'windows' THEN '.exe' ELSE '' END);
            evidence_fields := regexp_split_to_array(regexp_replace(split_part(executable_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(evidence_fields, 1), 0) < 3 OR lower(evidence_fields[1]) <> 'ffmpeg' OR lower(evidence_fields[2]) <> 'version' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: malformed ffmpeg evidence', attempt.operation_id;
            END IF;
            IF evidence_fields[3] IS DISTINCT FROM ffmpeg_token THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: ffmpeg evidence differs', attempt.operation_id;
            END IF;
            executable_output := installation.executable_verification_output ->> ('ffprobe' || CASE WHEN installation.platform_goos = 'windows' THEN '.exe' ELSE '' END);
            evidence_fields := regexp_split_to_array(regexp_replace(split_part(executable_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(evidence_fields, 1), 0) < 3 OR lower(evidence_fields[1]) <> 'ffprobe' OR lower(evidence_fields[2]) <> 'version' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: malformed ffprobe evidence', attempt.operation_id;
            END IF;
            IF selected_token IS NULL OR selected_token IS DISTINCT FROM selection ->> 'version' OR
               regexp_replace(selected_output, $trim$^\s+|\s+$$trim$, '', 'g') IS DISTINCT FROM selection ->> 'version_banner' OR
               ffmpeg_token IS NULL OR ffprobe_token IS NULL OR evidence_fields[3] IS DISTINCT FROM ffprobe_token THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: frozen selection is not proven by installation evidence', attempt.operation_id;
            END IF;
            migrated_tools := migrated_tools || jsonb_build_array(jsonb_set(selection, '{version}', to_jsonb(installation.version), false));
        END LOOP;
        UPDATE public.track_publication_attempt
           SET frozen_inputs = jsonb_set(frozen_inputs, '{tools}', migrated_tools, false)
         WHERE operation_id = attempt.operation_id;
    END LOOP;

    ALTER TABLE public.tool_installation ADD CONSTRAINT tool_installation_ready_version_check
        CHECK (state <> 'ready' OR (version IS NOT NULL AND version <> ''));
END $migration$;

-- Former migration: 20261101000001_release_media_and_manual_credits.tx.up.sql
CREATE TABLE release_medium (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    release_id uuid NOT NULL REFERENCES local_release(id) ON DELETE CASCADE,
    medium integer NOT NULL CHECK (medium >= 0),
    title text NOT NULL DEFAULT '',
    format text NOT NULL DEFAULT '',
    UNIQUE (release_id, medium)
);

INSERT INTO release_medium (release_id, medium)
SELECT DISTINCT release_id, medium FROM release_track;

ALTER TABLE release_track
    ADD CONSTRAINT release_track_medium_fk
    FOREIGN KEY (release_id, medium) REFERENCES release_medium(release_id, medium);

CREATE FUNCTION ensure_release_medium_for_track() RETURNS trigger
LANGUAGE plpgsql AS $function$
BEGIN
    INSERT INTO release_medium (release_id, medium)
    VALUES (NEW.release_id, NEW.medium)
    ON CONFLICT (release_id, medium) DO NOTHING;
    RETURN NEW;
END;
$function$;

CREATE TRIGGER release_track_ensure_medium
BEFORE INSERT OR UPDATE OF release_id, medium ON release_track
FOR EACH ROW EXECUTE FUNCTION ensure_release_medium_for_track();

ALTER TABLE release_artist ADD COLUMN role text NOT NULL DEFAULT '';
ALTER TABLE track_artist ADD COLUMN role text NOT NULL DEFAULT '';
ALTER TABLE recording_artist ADD COLUMN role text NOT NULL DEFAULT '';
ALTER TABLE release_track
    ADD COLUMN duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0);
