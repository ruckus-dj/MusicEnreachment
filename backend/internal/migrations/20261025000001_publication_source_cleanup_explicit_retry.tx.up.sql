ALTER TABLE public.operation DROP CONSTRAINT operation_kind_check;
ALTER TABLE public.operation ADD CONSTRAINT operation_kind_check CHECK (kind = ANY (ARRAY[
    'install','activate','delete','move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
    'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
    'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1',
    'matching_apply_v1','publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1'
]));

ALTER TABLE public.operation DROP CONSTRAINT operation_mutation_has_target_installation;
ALTER TABLE public.operation ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    state NOT IN ('queued','running') OR target_installation_id IS NOT NULL
);

ALTER TABLE public.operation DROP CONSTRAINT operation_target_identity;
ALTER TABLE public.operation ADD CONSTRAINT operation_target_identity CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    COALESCE(input_snapshot ->> 'target_identity','') <> ''
);

ALTER TABLE public.operation DROP CONSTRAINT operation_source_analysis_target_shape;
ALTER TABLE public.operation ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
    (kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1') AND source_analysis_mode IS NULL
        AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND target_installation_id IS NULL AND target_source_root_id IS NULL AND target_source_location_id IS NULL
        AND provider_source_id IS NULL AND provider_configuration_identity IS NULL AND provider_cache_key IS NULL
        AND NOT provider_explicit_refresh AND (NOT tools_read_required OR kind='publish_track_v1'))
    OR (kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1') AND (((source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND (NOT tools_read_required OR kind = 'scan_source'))
      OR (source_analysis_mode = 'batch' AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target)
      OR (source_analysis_mode = 'single_step' AND
          (((state IN ('queued','running') AND target_work_id IS NOT NULL AND target_step IN ('sha256','probe','fingerprint','metadata'))
            OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))
            AND (NOT rerun_target OR (state IN ('queued','running') AND target_step='fingerprint') OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))))) IS TRUE))
);

ALTER TABLE public.operation DROP CONSTRAINT operation_publication_snapshot_check;
ALTER TABLE public.operation ADD CONSTRAINT operation_publication_snapshot_check CHECK (
    kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1','retry_publication_sources_v1') OR
    (jsonb_typeof(input_snapshot) = 'object'
     AND input_snapshot ?& ARRAY['version','position_id','output_epoch']
     AND jsonb_typeof(input_snapshot->'version')='number' AND input_snapshot->>'version'='1'
     AND jsonb_typeof(input_snapshot->'position_id')='string'
     AND (input_snapshot->>'position_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND jsonb_typeof(input_snapshot->'output_epoch')='string'
     AND (input_snapshot->>'output_epoch') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND ((kind='publish_track_v1' AND input_snapshot - ARRAY['version','position_id','output_epoch','intent_kind'] = '{}'::jsonb
           AND input_snapshot ? 'intent_kind' AND jsonb_typeof(input_snapshot->'intent_kind')='string'
           AND input_snapshot->>'intent_kind' IN ('audio','rewrite','delete','reorganize'))
       OR (kind='cleanup_publication_sources_v1' AND input_snapshot - ARRAY['version','position_id','output_epoch'] = '{}'::jsonb)
        OR (kind='retry_publication_sources_v1'
            AND input_snapshot ?& ARRAY['original_operation_id','original_claim_uuid']
            AND input_snapshot - ARRAY['version','position_id','output_epoch','original_operation_id','original_claim_uuid'] = '{}'::jsonb
           AND jsonb_typeof(input_snapshot->'original_operation_id')='string'
           AND (input_snapshot->>'original_operation_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
           AND jsonb_typeof(input_snapshot->'original_claim_uuid')='string'
            AND (input_snapshot->>'original_claim_uuid') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')) IS TRUE)
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
