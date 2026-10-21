DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry)
       OR EXISTS (SELECT 1 FROM public.operation WHERE kind='retry_publication_sources_v1') THEN
        RAISE EXCEPTION 'cannot roll back explicit source cleanup retry while grants or operations exist';
    END IF;
END $$;

DROP TRIGGER source_cleanup_explicit_retry_item_immutable ON public.source_cleanup_explicit_retry_item;
DROP FUNCTION public.protect_source_cleanup_explicit_retry_item();
DROP TRIGGER source_cleanup_explicit_retry_binding_immutable ON public.source_cleanup_explicit_retry;
DROP FUNCTION public.protect_source_cleanup_explicit_retry_binding();
DROP TRIGGER publication_operation_owner_retry_item ON public.source_cleanup_explicit_retry_item;
DROP TRIGGER publication_operation_owner_retry_grant ON public.source_cleanup_explicit_retry;
DROP TRIGGER publication_operation_owner_retry_operation ON public.operation;
DROP FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();
DROP TABLE public.source_cleanup_explicit_retry_item;
DROP TABLE public.source_cleanup_explicit_retry;
DROP FUNCTION public.valid_source_cleanup_explicit_retry_binding(jsonb, uuid, integer, bigint, uuid, uuid, uuid, uuid);
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
END $$;
ALTER TABLE public.publication_execution_claim DROP CONSTRAINT publication_execution_claim_delivery_identity_key;
ALTER TABLE public.source_cleanup_owner DROP CONSTRAINT source_cleanup_owner_item_owner_key;

ALTER TABLE public.operation DROP CONSTRAINT operation_publication_snapshot_check;
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
ALTER TABLE public.operation DROP CONSTRAINT operation_source_analysis_target_shape;
ALTER TABLE public.operation ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
    (kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1') AND source_analysis_mode IS NULL
        AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
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
ALTER TABLE public.operation ADD CONSTRAINT operation_publication_snapshot_check CHECK (
    kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1') OR
    (jsonb_typeof(input_snapshot) = 'object'
     AND input_snapshot - ARRAY['version','position_id','output_epoch','intent_kind'] = '{}'::jsonb
     AND input_snapshot ?& ARRAY['version','position_id','output_epoch']
     AND jsonb_typeof(input_snapshot->'version')='number' AND input_snapshot->>'version'='1'
     AND jsonb_typeof(input_snapshot->'position_id')='string'
     AND (input_snapshot->>'position_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND jsonb_typeof(input_snapshot->'output_epoch')='string'
     AND (input_snapshot->>'output_epoch') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND ((kind='publish_track_v1' AND input_snapshot ? 'intent_kind'
           AND jsonb_typeof(input_snapshot->'intent_kind')='string'
           AND input_snapshot->>'intent_kind' IN ('audio','rewrite','delete','reorganize'))
       OR (kind='cleanup_publication_sources_v1' AND NOT (input_snapshot ? 'intent_kind'))))
);

ALTER TABLE public.publication_namespace_claim DROP CONSTRAINT publication_namespace_claim_owner_scope_check;
ALTER TABLE public.publication_namespace_claim ADD CONSTRAINT publication_namespace_claim_owner_scope_check CHECK (
    (node_kind='file' AND owner_position_id IS NOT NULL AND directory_scope_key IS NULL)
    OR (node_kind='dir' AND owner_position_id IS NULL AND directory_scope_key IS NOT NULL AND directory_scope_key <> '')
);
