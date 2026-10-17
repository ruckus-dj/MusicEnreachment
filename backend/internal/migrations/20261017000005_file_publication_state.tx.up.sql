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
    safe_error text,
    CONSTRAINT source_cleanup_item_pkey PRIMARY KEY (id),
    CONSTRAINT source_cleanup_item_operation_fkey FOREIGN KEY (operation_id)
        REFERENCES public.operation(id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_item_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_item_state_check CHECK (state = ANY (ARRAY['queued', 'claimed', 'recovery_required', 'retry_pending', 'cancelled', 'stale'])),
    CONSTRAINT source_cleanup_item_live_borrower_check CHECK (
        state NOT IN ('queued', 'claimed', 'recovery_required', 'retry_pending')
        OR (source_snapshot IS NOT NULL AND jsonb_typeof(source_snapshot) = 'object' AND borrower_revision IS NOT NULL)
    ),
    CONSTRAINT source_cleanup_item_snapshot_object_check CHECK (source_snapshot IS NULL OR jsonb_typeof(source_snapshot) = 'object')
);

CREATE INDEX source_cleanup_item_state_idx ON public.source_cleanup_item(state);
CREATE INDEX source_cleanup_item_operation_idx ON public.source_cleanup_item(operation_id);
