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
