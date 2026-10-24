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
