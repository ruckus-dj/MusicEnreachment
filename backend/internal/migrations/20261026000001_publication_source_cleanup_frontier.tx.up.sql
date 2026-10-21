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

ALTER TABLE public.source_cleanup_explicit_retry ADD COLUMN frontier_revision uuid;
ALTER TABLE public.source_cleanup_explicit_retry ADD CONSTRAINT source_cleanup_retry_frontier_revision_fkey
    FOREIGN KEY (original_operation_id,frontier_revision)
    REFERENCES public.source_cleanup_recovery_state(original_operation_id,revision)
    ON DELETE RESTRICT;

CREATE FUNCTION public.check_source_cleanup_recovery_coverage() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE op_id uuid;
BEGIN
  IF TG_OP='DELETE' THEN op_id:=OLD.original_operation_id; ELSE op_id:=NEW.original_operation_id; END IF;
  IF EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state WHERE original_operation_id=op_id)
     AND ((SELECT count(*) FROM public.source_cleanup_item WHERE operation_id=op_id)=0
       OR (SELECT count(*) FROM public.source_cleanup_recovery_item WHERE original_operation_id=op_id)
          <>(SELECT count(*) FROM public.source_cleanup_item WHERE operation_id=op_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_item i LEFT JOIN public.source_cleanup_recovery_item r
          ON r.original_operation_id=i.operation_id AND r.item_id=i.id
          WHERE i.operation_id=op_id AND r.item_id IS NULL)) THEN
    RAISE EXCEPTION 'source cleanup recovery frontier must cover every original item' USING ERRCODE='23514';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER source_cleanup_recovery_state_coverage AFTER INSERT OR UPDATE
ON public.source_cleanup_recovery_state DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();
CREATE CONSTRAINT TRIGGER source_cleanup_recovery_item_coverage AFTER INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_item DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();

CREATE FUNCTION public.protect_source_cleanup_recovery_item() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE active boolean;
BEGIN
  IF TG_OP='DELETE' THEN
    SELECT EXISTS(SELECT 1 FROM public.source_cleanup_explicit_retry WHERE original_operation_id=OLD.original_operation_id) INTO active;
    IF active THEN RAISE EXCEPTION 'pinned source cleanup frontier item cannot be deleted' USING ERRCODE='23514'; END IF;
    RETURN OLD;
  END IF;
  IF TG_OP='UPDATE' THEN
    IF ROW(NEW.original_operation_id,NEW.item_id) IS DISTINCT FROM ROW(OLD.original_operation_id,OLD.item_id) THEN
      RAISE EXCEPTION 'source cleanup recovery item identity is immutable' USING ERRCODE='23514';
    END IF;
    SELECT EXISTS(SELECT 1 FROM public.source_cleanup_explicit_retry WHERE original_operation_id=OLD.original_operation_id) INTO active;
    IF active THEN RAISE EXCEPTION 'pinned source cleanup frontier is immutable' USING ERRCODE='23514'; END IF;
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;
CREATE TRIGGER source_cleanup_recovery_item_protected BEFORE INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_item FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_recovery_item();

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

CREATE OR REPLACE FUNCTION public.protect_source_cleanup_recovery_item() RETURNS trigger
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

CREATE OR REPLACE FUNCTION public.protect_source_cleanup_retry_resolution() RETURNS trigger
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

CREATE OR REPLACE FUNCTION public.check_source_cleanup_recovery_coverage() RETURNS trigger
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

CREATE OR REPLACE FUNCTION public.protect_source_cleanup_retry_frontier() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.frontier_revision IS DISTINCT FROM OLD.frontier_revision THEN
    RAISE EXCEPTION 'source cleanup retry frontier revision is immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

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

DROP TRIGGER source_cleanup_recovery_state_coverage ON public.source_cleanup_recovery_state;
CREATE CONSTRAINT TRIGGER source_cleanup_recovery_state_coverage AFTER INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_state DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();
DROP TRIGGER source_cleanup_recovery_item_coverage ON public.source_cleanup_recovery_item;
CREATE CONSTRAINT TRIGGER source_cleanup_recovery_item_coverage AFTER INSERT OR UPDATE OR DELETE
ON public.source_cleanup_recovery_item DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();
CREATE CONSTRAINT TRIGGER source_cleanup_retry_grant_frontier_coverage AFTER INSERT OR UPDATE OR DELETE
ON public.source_cleanup_explicit_retry DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();
CREATE CONSTRAINT TRIGGER source_cleanup_frontier_transfer_proof_consumed
AFTER INSERT ON public.source_cleanup_frontier_transfer_proof DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.finish_source_cleanup_frontier_transfer_proof();
