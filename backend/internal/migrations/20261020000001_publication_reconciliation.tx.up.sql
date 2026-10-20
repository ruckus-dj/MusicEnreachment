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
