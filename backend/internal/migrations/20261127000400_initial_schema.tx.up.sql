-- Fresh-instance baseline: complete application schema.

-- This is the single canonical initial migration. It describes the final
-- schema directly; historical ALTER/backfill steps are intentionally absent.
-- It owns the public application namespace only. River and other unrelated
-- objects in the same database are created and migrated by their own owners.

-- Key invariants retained by the schema:
--   * operation rows are serialized by their active tools/source scope;
--   * source analysis holds, work executions, and variant identities are
--     protected by triggers and cannot be silently rewired;
--   * matching receipts, acquisition seals, and apply requests are write-once
--     or append-only; retained reconciliation evidence blocks a destructive
--     rollback;
--   * publication and cleanup authority is immutable once recorded, and
--     local library relationships must remain reachable;
--   * provider ownership is kept on each referencing row, while graph
--     references may point at records owned by another provider;
--   * Cover Art Archive and artist-photo caches are durable, bounded, and
--     keyed by exact provider identities.
--
-- The matching fallback calculation receipt v4 ingress check is created valid:
-- a fresh database has no historical v3 receipts to retain.

SET LOCAL check_function_bodies = false;

-- Functions
CREATE FUNCTION public.assert_local_recording_has_release_track(p_recording_id uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    has_release_track boolean;
BEGIN
    -- Serialize checks for this recording, including checks under repeatable-read isolation.
    UPDATE public.local_recording SET id = id WHERE id = p_recording_id;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1 FROM public.release_track WHERE recording_id = p_recording_id
    ) INTO has_release_track;
    IF NOT has_release_track THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'local recording must have at least one release track';
    END IF;
END
$$;

CREATE FUNCTION public.assert_local_release_has_artist(p_release_id uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    has_artist boolean;
BEGIN
    -- Serialize checks for this release, including checks under repeatable-read isolation.
    UPDATE public.local_release SET id = id WHERE id = p_release_id;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1 FROM public.release_artist WHERE release_id = p_release_id
    ) INTO has_artist;
    IF NOT has_artist THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'local release must have at least one release artist';
    END IF;
END
$$;

CREATE FUNCTION public.assert_source_cleanup_root_abandonment_retired(p_original_operation_id uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE receipt public.source_cleanup_root_abandonment%ROWTYPE;
BEGIN
    SELECT * INTO receipt FROM public.source_cleanup_root_abandonment
      WHERE original_operation_id=p_original_operation_id AND transaction_id=pg_current_xact_id();
    IF NOT FOUND THEN RAISE EXCEPTION 'source cleanup root retirement lacks its current-transaction receipt' USING ERRCODE='23514'; END IF;
    IF EXISTS (SELECT 1 FROM public.source_root WHERE id=receipt.root_id)
       OR COALESCE((SELECT to_jsonb(p) FROM public.release_track p WHERE p.id=(receipt.evidence->>'position_id')::uuid),'null'::jsonb)
          IS DISTINCT FROM receipt.evidence->'position'
       OR EXISTS (SELECT 1 FROM public.publication_source_hold WHERE source_root_id=receipt.root_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_owner WHERE source_root_id=receipt.root_id)
       OR EXISTS (SELECT 1 FROM public.operation WHERE id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.publication_execution_claim WHERE operation_id=p_original_operation_id)
       OR (receipt.evidence->'retry_operation' IS NOT NULL AND receipt.evidence->'retry_operation'<>'null'::jsonb
           AND EXISTS (SELECT 1 FROM public.operation WHERE id=(receipt.evidence#>>'{retry_operation,id}')::uuid))
       OR (receipt.evidence->'retry_operation' IS NOT NULL AND receipt.evidence->'retry_operation'<>'null'::jsonb
           AND EXISTS (SELECT 1 FROM public.publication_execution_claim WHERE operation_id=(receipt.evidence#>>'{retry_operation,id}')::uuid))
       OR EXISTS (SELECT 1 FROM public.source_cleanup_item WHERE operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_owner WHERE operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry WHERE original_operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state WHERE original_operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item WHERE original_operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_frontier_transfer_proof WHERE original_operation_id=p_original_operation_id) THEN
        RAISE EXCEPTION 'source cleanup root abandonment graph is not fully retired' USING ERRCODE='23514';
    END IF;
     IF EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'retry_resolutions') e(value)
         JOIN public.source_cleanup_explicit_retry_resolution r ON to_jsonb(r)=e.value)
        OR EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'retry_resolutions') e(value)
         WHERE EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry_resolution r
           WHERE r.retry_id=(e.value->>'retry_id')::uuid AND r.item_id=(e.value->>'item_id')::uuid))
        OR EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'retry_items') e(value)
        JOIN public.source_cleanup_explicit_retry_item i ON to_jsonb(i)=e.value)
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'items') e(value)
        JOIN public.source_cleanup_item i ON i.id=(e.value->>'id')::uuid AND i.operation_id=(e.value->>'operation_id')::uuid)
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'owners') e(value)
        JOIN public.source_cleanup_owner o ON o.item_id=(e.value->>'item_id')::uuid AND o.operation_id=(e.value->>'operation_id')::uuid)
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'retry_items') e(value)
        WHERE EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry_item i
          WHERE i.retry_id=(e.value->>'retry_id')::uuid AND i.item_id=(e.value->>'item_id')::uuid))
       OR EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry g
          WHERE g.original_operation_id=p_original_operation_id)
       OR (receipt.evidence->'retry_grant' IS NOT NULL AND receipt.evidence->'retry_grant'<>'null'::jsonb
           AND EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry g
             WHERE g.id=(receipt.evidence#>>'{retry_grant,id}')::uuid OR
                   (g.original_operation_id=p_original_operation_id AND g.recovery_operation_id=(receipt.evidence#>>'{retry_grant,recovery_operation_id}')::uuid)))
       OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state s
          WHERE s.original_operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item r
          WHERE r.original_operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_frontier_transfer_proof p
          WHERE p.original_operation_id=p_original_operation_id)
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'river_jobs') e(value)
          JOIN public.river_job j ON j.id=(e.value->>'id')::bigint
          WHERE j.kind IS DISTINCT FROM e.value->>'kind' OR j.args IS DISTINCT FROM e.value->'args')
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(receipt.evidence->'river_jobs') e(value)
        JOIN public.river_job j ON j.id=(e.value->>'id')::bigint
        WHERE j.state IN ('running','available','pending','retryable','scheduled')) THEN
        RAISE EXCEPTION 'source cleanup root abandonment retained child evidence or runnable delivery' USING ERRCODE='23514';
    END IF;
    IF EXISTS (SELECT 1 FROM public.river_job j WHERE j.state IN ('running','available','pending','retryable','scheduled')
        AND ((j.kind='cleanup_publication_sources_v1' AND j.args->>'operation_id'=p_original_operation_id::text)
          OR (j.kind='retry_publication_sources_v1' AND j.args->>'operation_id'=receipt.evidence#>>'{retry_operation,id}'))) THEN
        RAISE EXCEPTION 'source cleanup root abandonment left an associated runnable delivery' USING ERRCODE='23514';
    END IF;
    IF (SELECT output_epoch FROM public.publication_namespace_state WHERE singleton=true)
       IS DISTINCT FROM (receipt.evidence->>'epoch')::uuid THEN
        RAISE EXCEPTION 'source cleanup root abandonment changed publication namespace epoch' USING ERRCODE='23514';
    END IF;
    IF COALESCE((SELECT to_jsonb(p) FROM public.track_publication p WHERE p.position_id=(receipt.evidence->>'position_id')::uuid),'null'::jsonb)
       IS DISTINCT FROM receipt.evidence->'current_publication'
       OR COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.namespace_claim_id,r.position_id,r.generation)
          FROM public.publication_namespace_reservation r WHERE r.position_id=(receipt.evidence->>'position_id')::uuid OR r.namespace_claim_id IN
            (SELECT c.id FROM public.publication_namespace_claim c WHERE c.owner_position_id=(receipt.evidence->>'position_id')::uuid
              OR EXISTS (SELECT 1 FROM public.publication_namespace_reservation owned
                WHERE owned.namespace_claim_id=c.id AND owned.position_id=(receipt.evidence->>'position_id')::uuid))),'[]'::jsonb)
          IS DISTINCT FROM receipt.evidence->'namespace_reservations'
       OR COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM public.publication_namespace_claim c
          WHERE c.owner_position_id=(receipt.evidence->>'position_id')::uuid OR EXISTS
            (SELECT 1 FROM public.publication_namespace_reservation r WHERE r.namespace_claim_id=c.id
              AND r.position_id=(receipt.evidence->>'position_id')::uuid)),'[]'::jsonb)
          IS DISTINCT FROM receipt.evidence->'namespace_claims' THEN
        RAISE EXCEPTION 'source cleanup root abandonment changed current publication namespace authority' USING ERRCODE='23514';
    END IF;
END $$;

CREATE FUNCTION public.capture_source_cleanup_root_abandonment() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE root_row public.source_root%ROWTYPE;
        position_row public.release_track%ROWTYPE;
        original public.operation%ROWTYPE;
        original_claim public.publication_execution_claim%ROWTYPE;
        retry public.source_cleanup_explicit_retry%ROWTYPE;
        retry_operation public.operation%ROWTYPE;
        retry_claim public.publication_execution_claim%ROWTYPE;
        v_epoch uuid;
        v_position_id uuid;
        location_count bigint;
        claim_count integer;
        recovery_claim_count integer;
        position_count integer;
        has_running_job boolean := false;
        job record;
        canonical jsonb;
BEGIN
    IF TG_OP <> 'INSERT' OR NEW.transaction_id IS DISTINCT FROM pg_current_xact_id()
       OR NEW.created_at IS DISTINCT FROM transaction_timestamp()
       OR NEW.evidence IS DISTINCT FROM '{}'::jsonb
       OR NEW.original_operation_id='00000000-0000-0000-0000-000000000000'::uuid
       OR NEW.root_id='00000000-0000-0000-0000-000000000000'::uuid THEN
        RAISE EXCEPTION 'source cleanup root abandonment receipt must be caller-empty and database-authored' USING ERRCODE='23514';
    END IF;

    -- Match root deletion's lock order; the validator is also safe when called
    -- directly because it reacquires every coordination gate.
    PERFORM pg_advisory_xact_lock(1297371734, 94017);
    PERFORM pg_advisory_xact_lock(1297371734, 3);
    PERFORM pg_advisory_xact_lock(1297371734, 4);
    IF EXISTS (SELECT 1 FROM public.output_reset_journal WHERE state <> 'finished') THEN
        RAISE EXCEPTION 'source cleanup root abandonment is blocked by output reset recovery' USING ERRCODE='23514';
    END IF;
    SELECT * INTO root_row FROM public.source_root WHERE id=NEW.root_id FOR UPDATE;
    IF NOT FOUND OR root_row.configured_path IS DISTINCT FROM NEW.confirmed_path THEN
        RAISE EXCEPTION 'source cleanup root abandonment confirmation does not match current root' USING ERRCODE='23514';
    END IF;
    SELECT count(*) INTO location_count FROM public.source_location WHERE source_root_id=NEW.root_id;
    IF location_count <> NEW.confirmed_location_count THEN
        RAISE EXCEPTION 'source cleanup root abandonment location confirmation changed' USING ERRCODE='23514';
    END IF;

    SELECT output_epoch INTO v_epoch FROM public.publication_namespace_state WHERE singleton=true FOR UPDATE;
    SELECT count(DISTINCT item.position_id) INTO position_count FROM public.source_cleanup_item item WHERE item.operation_id=NEW.original_operation_id;
    SELECT DISTINCT item.position_id INTO v_position_id
      FROM public.source_cleanup_item item WHERE item.operation_id=NEW.original_operation_id;
    IF position_count <> 1 OR v_position_id IS NULL THEN
        RAISE EXCEPTION 'source cleanup abandonment requires a complete nonempty cleanup graph' USING ERRCODE='23514';
    END IF;
    SELECT * INTO position_row FROM public.release_track WHERE id=v_position_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'source cleanup abandonment publication position is missing' USING ERRCODE='23514';
    END IF;
    SELECT * INTO original FROM public.operation WHERE id=NEW.original_operation_id FOR UPDATE;
    IF NOT FOUND OR original.kind <> 'cleanup_publication_sources_v1' OR original.state <> 'failed'
       OR original.stage <> 'cleanup_recovery_required' OR original.finished_at IS NULL
       OR original.safe_error IS NULL OR btrim(original.safe_error)='' OR original.river_job_id IS NULL
       OR original.attempt < 1 OR original.input_snapshot->>'position_id' IS DISTINCT FROM v_position_id::text
       OR original.input_snapshot->>'output_epoch' IS DISTINCT FROM v_epoch::text THEN
        RAISE EXCEPTION 'source cleanup original operation is not a valid failed recovery owner' USING ERRCODE='23514';
    END IF;
    SELECT count(*) INTO claim_count FROM public.publication_execution_claim WHERE operation_id=original.id;
    SELECT * INTO original_claim FROM public.publication_execution_claim
      WHERE operation_id=original.id ORDER BY application_attempt FOR UPDATE;
    IF claim_count <> 1 OR original_claim.state <> 'recovery_required'
       OR original_claim.application_attempt <> original.attempt
       OR original_claim.river_job_id <> original.river_job_id
       OR original_claim.epoch <> v_epoch OR original_claim.river_attempt <= 0
       OR original_claim.claim_uuid='00000000-0000-0000-0000-000000000000'::uuid
       OR original_claim.boot_uuid='00000000-0000-0000-0000-000000000000'::uuid
       OR original_claim.delivery_token='' THEN
        RAISE EXCEPTION 'source cleanup original recovery claim is not exact' USING ERRCODE='23514';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.source_cleanup_item WHERE operation_id=original.id)
       OR EXISTS (
          SELECT 1 FROM public.source_cleanup_item item
          LEFT JOIN public.source_cleanup_owner owner ON owner.item_id=item.id
          WHERE item.operation_id=original.id AND (owner.item_id IS NULL OR owner.operation_id<>original.id
            OR owner.application_attempt<>original.attempt OR owner.river_job_id<>original.river_job_id
            OR item.position_id<>v_position_id OR item.epoch<>v_epoch
            OR owner.source_location_id IS NULL OR owner.work_id IS NULL
            OR owner.source_root_id IS NULL
            OR item.source_snapshot->>'source_root_id' IS DISTINCT FROM owner.source_root_id::text
            OR item.source_snapshot->>'source_location_id' IS DISTINCT FROM owner.source_location_id::text
            OR item.source_snapshot->>'work_id' IS DISTINCT FROM owner.work_id::text
            OR NOT EXISTS (SELECT 1 FROM public.source_location location
                WHERE location.id=owner.source_location_id AND location.source_root_id=owner.source_root_id)
            OR NOT EXISTS (SELECT 1 FROM public.source_analysis_work work
                WHERE work.id=owner.work_id AND work.source_root_id=owner.source_root_id
                  AND work.location_id=owner.source_location_id))
       )
       OR EXISTS (SELECT 1 FROM public.source_cleanup_owner owner
          WHERE owner.operation_id=original.id AND NOT EXISTS
            (SELECT 1 FROM public.source_cleanup_item item WHERE item.id=owner.item_id AND item.operation_id=original.id))
       OR NOT EXISTS (SELECT 1 FROM public.source_cleanup_owner WHERE operation_id=original.id AND source_root_id=NEW.root_id) THEN
        RAISE EXCEPTION 'source cleanup owners do not exactly cover the failed original graph and requested root' USING ERRCODE='23514';
    END IF;

    SELECT * INTO retry FROM public.source_cleanup_explicit_retry WHERE original_operation_id=original.id FOR UPDATE;
    IF FOUND THEN
        SELECT * INTO retry_operation FROM public.operation WHERE id=retry.recovery_operation_id FOR UPDATE;
        IF retry.position_id<>v_position_id OR retry.epoch<>v_epoch
           OR retry.original_application_attempt<>original.attempt OR retry.original_river_job_id<>original.river_job_id
           OR retry.original_claim_uuid<>original_claim.claim_uuid
           OR retry_operation.kind<>'retry_publication_sources_v1' OR retry_operation.state<>'failed'
            OR retry_operation.input_snapshot->>'position_id' IS DISTINCT FROM v_position_id::text
            OR retry_operation.input_snapshot->>'output_epoch' IS DISTINCT FROM v_epoch::text
           OR retry_operation.input_snapshot->>'original_operation_id' IS DISTINCT FROM original.id::text
           OR retry_operation.input_snapshot->>'original_claim_uuid' IS DISTINCT FROM original_claim.claim_uuid::text
           OR (SELECT count(*) FROM public.source_cleanup_explicit_retry_item WHERE retry_id=retry.id) <>
              (SELECT count(*) FROM public.source_cleanup_item WHERE operation_id=original.id)
           OR EXISTS (SELECT 1 FROM public.source_cleanup_item item
               LEFT JOIN public.source_cleanup_owner owner ON owner.item_id=item.id
               LEFT JOIN public.source_cleanup_explicit_retry_item binding ON binding.retry_id=retry.id
                 AND binding.original_operation_id=item.operation_id AND binding.item_id=item.id AND binding.owner_uuid=owner.owner_uuid
               WHERE item.operation_id=original.id AND binding.item_id IS NULL) THEN
            RAISE EXCEPTION 'source cleanup retry grant or item binding is not exact' USING ERRCODE='23514';
        END IF;
        SELECT count(*) INTO recovery_claim_count FROM public.publication_execution_claim WHERE operation_id=retry.recovery_operation_id;
        IF retry.consumed_claim_uuid IS NOT NULL THEN
            SELECT * INTO retry_claim FROM public.publication_execution_claim
              WHERE operation_id=retry.recovery_operation_id ORDER BY application_attempt FOR UPDATE;
            IF recovery_claim_count<>1 OR retry_claim.state<>'recovery_required'
               OR retry_claim.application_attempt<>retry.consumed_application_attempt
               OR retry_claim.river_job_id<>retry.consumed_river_job_id
               OR retry_claim.claim_uuid<>retry.consumed_claim_uuid
               OR retry_claim.epoch<>v_epoch OR retry_claim.epoch<>retry.epoch
               OR retry_claim.river_attempt<=0
               OR retry_claim.claim_uuid='00000000-0000-0000-0000-000000000000'::uuid
               OR retry_claim.boot_uuid='00000000-0000-0000-0000-000000000000'::uuid
               OR retry_claim.delivery_token=''
               OR retry_operation.attempt<>retry.consumed_application_attempt
               OR retry_operation.river_job_id IS DISTINCT FROM retry.consumed_river_job_id
               OR NOT EXISTS (SELECT 1 FROM public.river_job retry_job
                   WHERE retry_job.id=retry.consumed_river_job_id
                     AND retry_job.kind='retry_publication_sources_v1'
                     AND retry_job.args->>'operation_id'=retry.recovery_operation_id::text)
               OR retry_operation.finished_at IS NULL OR retry_operation.safe_error IS NULL
               OR btrim(retry_operation.safe_error)=''
               OR retry_operation.stage<>'cleanup_retry_recovery_required' THEN
                RAISE EXCEPTION 'consumed source cleanup retry lacks its exact returned recovery claim' USING ERRCODE='23514';
            END IF;
            IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry_resolution resolution
                WHERE resolution.retry_id=retry.id AND
                  (resolution.recovery_operation_id<>retry.recovery_operation_id
                   OR resolution.application_attempt<>retry.consumed_application_attempt
                   OR resolution.river_job_id<>retry.consumed_river_job_id
                   OR resolution.claim_uuid<>retry.consumed_claim_uuid)) THEN
                RAISE EXCEPTION 'source cleanup retry resolutions do not match the consumed recovery delivery' USING ERRCODE='23514';
            END IF;
        ELSIF retry.consumed_application_attempt IS NOT NULL OR retry.consumed_river_job_id IS NOT NULL
           OR recovery_claim_count<>0
           OR EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry_resolution WHERE retry_id=retry.id) THEN
            RAISE EXCEPTION 'unconsumed source cleanup retry has partial or resolution evidence' USING ERRCODE='23514';
        END IF;
        IF retry.frontier_revision IS NULL THEN
            IF EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state WHERE original_operation_id=original.id)
               OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item WHERE original_operation_id=original.id) THEN
                RAISE EXCEPTION 'source cleanup retry has an unpinned frontier' USING ERRCODE='23514';
            END IF;
        ELSIF NOT EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state state
             WHERE state.original_operation_id=original.id AND state.revision=retry.frontier_revision
               AND (SELECT count(*) FROM public.source_cleanup_recovery_item i WHERE i.original_operation_id=original.id)=
                   (SELECT count(*) FROM public.source_cleanup_item i WHERE i.operation_id=original.id)
               AND NOT EXISTS (SELECT 1 FROM public.source_cleanup_item i
                   WHERE i.operation_id=original.id AND NOT EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item r
                       WHERE r.original_operation_id=original.id AND r.item_id=i.id))
               AND NOT EXISTS (SELECT 1 FROM public.source_cleanup_recovery_item r
                   WHERE r.original_operation_id=original.id AND NOT EXISTS
                     (SELECT 1 FROM jsonb_array_elements(state.proof->'folded_items') f(value) WHERE f.value=to_jsonb(r)))) THEN
            RAISE EXCEPTION 'source cleanup retry frontier is not the exact pinned original-item frontier' USING ERRCODE='23514';
        END IF;
        IF EXISTS (SELECT 1 FROM public.source_cleanup_frontier_transfer_proof WHERE original_operation_id=original.id) THEN
            RAISE EXCEPTION 'source cleanup retry has an in-flight frontier transfer' USING ERRCODE='23514';
        END IF;
    END IF;

    -- Lock every associated delivery in stable ID order, including ID-only
    -- River payloads. A recorded ID with the wrong kind or arguments is unsafe.
    FOR job IN SELECT id,kind,args,state FROM public.river_job
        WHERE id=original.river_job_id OR
          (kind='cleanup_publication_sources_v1' AND args->>'operation_id'=original.id::text) OR
          (retry.recovery_operation_id IS NOT NULL AND
            (id=(SELECT river_job_id FROM public.operation WHERE id=retry.recovery_operation_id) OR
             (kind='retry_publication_sources_v1' AND args->>'operation_id'=retry.recovery_operation_id::text)))
        ORDER BY id FOR UPDATE
    LOOP
        IF (job.id=original.river_job_id AND (job.kind IS DISTINCT FROM 'cleanup_publication_sources_v1' OR job.args->>'operation_id' IS DISTINCT FROM original.id::text))
           OR (retry.recovery_operation_id IS NOT NULL AND job.id=(SELECT river_job_id FROM public.operation WHERE id=retry.recovery_operation_id)
               AND (job.kind IS DISTINCT FROM 'retry_publication_sources_v1' OR job.args->>'operation_id' IS DISTINCT FROM retry.recovery_operation_id::text)) THEN
            RAISE EXCEPTION 'source cleanup abandonment has a conflicting River delivery identity' USING ERRCODE='23514';
        END IF;
        IF job.state='running' THEN
            has_running_job := true;
        END IF;
    END LOOP;

    canonical := jsonb_build_object(
      'version',1,'root_id',NEW.root_id,'root',to_jsonb(root_row),
      'original_operation_id',original.id,'original_operation',to_jsonb(original),
      'position',to_jsonb(position_row),
      'original_claim',to_jsonb(original_claim),
      'items',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.id) FROM public.source_cleanup_item i WHERE i.operation_id=original.id),'[]'::jsonb),
      'owners',COALESCE((SELECT jsonb_agg(to_jsonb(o) ORDER BY o.item_id) FROM public.source_cleanup_owner o WHERE o.operation_id=original.id),'[]'::jsonb),
      'retry_grant',(SELECT to_jsonb(r) FROM public.source_cleanup_explicit_retry r WHERE r.original_operation_id=original.id),
      'retry_operation',(SELECT to_jsonb(o) FROM public.operation o JOIN public.source_cleanup_explicit_retry r ON r.recovery_operation_id=o.id WHERE r.original_operation_id=original.id),
      'retry_claim',(SELECT to_jsonb(c) FROM public.publication_execution_claim c JOIN public.source_cleanup_explicit_retry r ON r.recovery_operation_id=c.operation_id WHERE r.original_operation_id=original.id),
      'retry_items',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.item_id) FROM public.source_cleanup_explicit_retry_item i JOIN public.source_cleanup_explicit_retry r ON r.id=i.retry_id WHERE r.original_operation_id=original.id),'[]'::jsonb),
      'retry_resolutions',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.item_id) FROM public.source_cleanup_explicit_retry_resolution r JOIN public.source_cleanup_explicit_retry g ON g.id=r.retry_id WHERE g.original_operation_id=original.id),'[]'::jsonb),
      'recovery_state',(SELECT to_jsonb(s) FROM public.source_cleanup_recovery_state s WHERE s.original_operation_id=original.id),
      'recovery_items',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY i.item_id) FROM public.source_cleanup_recovery_item i WHERE i.original_operation_id=original.id),'[]'::jsonb),
      'river_jobs',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',j.id,'kind',j.kind,'args',j.args,'state',j.state) ORDER BY j.id)
          FROM public.river_job j WHERE j.id=original.river_job_id OR
            (j.kind='cleanup_publication_sources_v1' AND j.args->>'operation_id'=original.id::text) OR
            (retry.recovery_operation_id IS NOT NULL AND (j.id=(SELECT river_job_id FROM public.operation WHERE id=retry.recovery_operation_id)
              OR (j.kind='retry_publication_sources_v1' AND j.args->>'operation_id'=retry.recovery_operation_id::text)))),'[]'::jsonb),
      'position_id',v_position_id,'epoch',v_epoch,
      'current_publication',(SELECT to_jsonb(p) FROM public.track_publication p WHERE p.position_id=v_position_id),
      'namespace_reservations',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.namespace_claim_id,r.position_id,r.generation)
          FROM public.publication_namespace_reservation r WHERE r.position_id=v_position_id OR r.namespace_claim_id IN
            (SELECT c.id FROM public.publication_namespace_claim c WHERE c.owner_position_id=v_position_id
              OR EXISTS (SELECT 1 FROM public.publication_namespace_reservation owned
                WHERE owned.namespace_claim_id=c.id AND owned.position_id=v_position_id))),'[]'::jsonb),
      'namespace_claims',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM public.publication_namespace_claim c
          WHERE c.owner_position_id=v_position_id OR EXISTS (SELECT 1 FROM public.publication_namespace_reservation r
            WHERE r.namespace_claim_id=c.id AND r.position_id=v_position_id)),'[]'::jsonb));
    IF has_running_job THEN
        RAISE EXCEPTION 'source cleanup abandonment has a running River delivery' USING ERRCODE='23514';
    END IF;
    NEW.evidence := canonical;
    RETURN NEW;
END $$;

CREATE FUNCTION public.check_local_recording_release_track_parent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.id IS NOT DISTINCT FROM OLD.id THEN
        RETURN NULL;
    END IF;
    PERFORM public.assert_local_recording_has_release_track(NEW.id);
    RETURN NULL;
END
$$;

CREATE FUNCTION public.check_local_release_artist_parent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.id IS NOT DISTINCT FROM OLD.id THEN
        RETURN NULL;
    END IF;
    PERFORM public.assert_local_release_has_artist(NEW.id);
    RETURN NULL;
END
$$;

CREATE FUNCTION public.check_manual_matching_receipt_retirement() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    receipt public.manual_matching_receipt%ROWTYPE;
    owner_operation public.operation%ROWTYPE;
    authorized_release boolean;
BEGIN
    SELECT * INTO receipt FROM public.manual_matching_receipt WHERE request_id = NEW.request_id;
    IF NOT FOUND OR receipt.retired_by_operation_id IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT * INTO owner_operation FROM public.operation WHERE id = receipt.retired_by_operation_id;
    authorized_release :=
        (owner_operation.input_snapshot->'scope'->>'kind'='release'
         AND owner_operation.input_snapshot->'scope'->>'release_id'=receipt.original_local_release_id::text)
        OR (owner_operation.input_snapshot->'scope'->>'kind'='artist'
            AND owner_operation.input_snapshot->'scope'->'artist_release_ids' @> jsonb_build_array(receipt.original_local_release_id::text));
    IF NOT FOUND OR owner_operation.kind <> 'delete_library_v1' OR owner_operation.state <> 'succeeded'
       OR receipt.local_release_id IS NOT NULL OR EXISTS (SELECT 1 FROM public.local_release WHERE id = receipt.original_local_release_id)
       OR authorized_release IS NOT TRUE
       OR NOT (owner_operation.input_snapshot->'scope'->'manual_receipt_request_ids' @> jsonb_build_array(receipt.request_id::text))
       OR jsonb_array_length(owner_operation.input_snapshot->'residual_publication_ids') <> 0 THEN
        RAISE EXCEPTION 'manual matching receipt retirement did not settle with its library deletion';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION public.check_publication_operation_owner(p_operation_id uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.check_publication_operation_owner_trigger() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.check_publication_reconciliation_owner(p_operation_id uuid) RETURNS void
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.check_publication_reconciliation_owner_trigger() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_TABLE_NAME='operation' THEN
        PERFORM public.check_publication_reconciliation_owner(COALESCE(NEW.id,OLD.id));
    ELSE
        IF TG_OP <> 'INSERT' THEN PERFORM public.check_publication_reconciliation_owner(OLD.operation_id); END IF;
        IF TG_OP <> 'DELETE' THEN PERFORM public.check_publication_reconciliation_owner(NEW.operation_id); END IF;
    END IF;
    RETURN NULL;
END $$;

CREATE FUNCTION public.check_release_artist_old_parent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' OR NEW.release_id IS DISTINCT FROM OLD.release_id THEN
        PERFORM public.assert_local_release_has_artist(OLD.release_id);
    END IF;
    RETURN NULL;
END
$$;

CREATE FUNCTION public.check_release_track_old_parent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM public.assert_local_release_has_artist(OLD.release_id);
        IF OLD.recording_id IS NOT NULL THEN
            PERFORM public.assert_local_recording_has_release_track(OLD.recording_id);
        END IF;
        RETURN NULL;
    END IF;

    IF NEW.release_id IS DISTINCT FROM OLD.release_id THEN
        PERFORM public.assert_local_release_has_artist(OLD.release_id);
    END IF;
    IF NEW.recording_id IS DISTINCT FROM OLD.recording_id AND OLD.recording_id IS NOT NULL THEN
        PERFORM public.assert_local_recording_has_release_track(OLD.recording_id);
    END IF;
    RETURN NULL;
END
$$;

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
    retained_failed_publication boolean;
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
        retained_failed_publication := operation_row.kind='publish_track_v1' AND operation_row.state='failed'
            AND EXISTS (SELECT 1 FROM track_publication_attempt a WHERE a.operation_id=operation_row.id
                AND a.position_id=(operation_row.input_snapshot->>'position_id')::uuid
                AND a.epoch=(operation_row.input_snapshot->>'output_epoch')::uuid);
        IF work_hold_count <> 0 OR has_execution OR
           (operation_row.state NOT IN ('queued', 'running') AND tool_hold_count <> 0 AND NOT retained_failed_publication) THEN
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
    -- A later settlement may clear an exact execution tuple in the same
    -- transaction. Deferred events for its earlier state must not validate an
    -- obsolete NEW tuple against the now-terminal operation.
    IF NOT EXISTS (SELECT 1 FROM source_analysis_step current_step
        WHERE current_step.work_id=work_id_value AND current_step.step=step_value
          AND current_step.execution_operation_id=operation_id_value) THEN
        RETURN NULL;
    END IF;
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

CREATE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.check_source_cleanup_recovery_coverage() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE op_id uuid;
        state_row public.source_cleanup_recovery_state%ROWTYPE;
        grant_count integer;
        op_state text;
BEGIN
   IF TG_OP='DELETE' THEN op_id:=OLD.original_operation_id; ELSE op_id:=NEW.original_operation_id; END IF;
    SELECT * INTO state_row FROM public.source_cleanup_recovery_state WHERE original_operation_id=op_id;
    IF NOT FOUND THEN
      IF EXISTS (SELECT 1 FROM public.source_cleanup_root_abandonment
          WHERE original_operation_id=op_id AND transaction_id=pg_current_xact_id()) THEN
        PERFORM public.assert_source_cleanup_root_abandonment_retired(op_id);
        RETURN NULL;
      END IF;
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

CREATE FUNCTION public.check_source_cleanup_root_abandonment_retired() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM public.assert_source_cleanup_root_abandonment_retired(NEW.original_operation_id);
    RETURN NULL;
END $$;

CREATE FUNCTION public.compute_source_cleanup_frontier_transfer_proof(p_original_operation_id uuid, p_recovery_operation_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.ensure_release_medium_for_track() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    INSERT INTO release_medium (release_id, medium)
    VALUES (NEW.release_id, NEW.medium)
    ON CONFLICT (release_id, medium) DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.finish_source_cleanup_frontier_transfer_proof() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.guard_library_deletion_operation_update() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.kind <> 'delete_library_v1' THEN
        RETURN NEW;
    END IF;
    IF NEW.kind <> OLD.kind
       OR NEW.input_snapshot->'scope' IS DISTINCT FROM OLD.input_snapshot->'scope'
       OR NEW.input_snapshot->'dependencies' IS DISTINCT FROM OLD.input_snapshot->'dependencies'
       OR NEW.input_snapshot->'ownership_witnesses' IS DISTINCT FROM OLD.input_snapshot->'ownership_witnesses'
       OR NEW.input_snapshot->>'scope_digest' IS DISTINCT FROM OLD.input_snapshot->>'scope_digest'
       OR NEW.input_snapshot->>'confirmation_digest' IS DISTINCT FROM OLD.input_snapshot->>'confirmation_digest'
       OR NEW.input_snapshot->>'schema_version' IS DISTINCT FROM OLD.input_snapshot->>'schema_version'
       OR NOT ((OLD.input_snapshot->'residual_publication_ids') @> (NEW.input_snapshot->'residual_publication_ids')) THEN
        RAISE EXCEPTION 'library deletion confirmation and residual scope are immutable except for settlement';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.guard_manual_matching_receipt_retirement() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    owner_operation public.operation%ROWTYPE;
    authorized_release boolean;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.local_release_id IS NULL THEN
            RAISE EXCEPTION 'manual matching receipt must initially reference a live release';
        END IF;
        NEW.original_local_release_id := NEW.local_release_id;
        NEW.retired_by_operation_id := NULL;
        RETURN NEW;
    END IF;

    IF (to_jsonb(NEW) - ARRAY['local_release_id','retired_by_operation_id','updated_at'])
       IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['local_release_id','retired_by_operation_id','updated_at']) THEN
        RAISE EXCEPTION 'manual matching receipt proof is immutable';
    END IF;
    IF OLD.local_release_id IS NULL OR OLD.retired_by_operation_id IS NOT NULL
       OR NEW.local_release_id IS NOT NULL OR NEW.retired_by_operation_id IS NULL THEN
        RAISE EXCEPTION 'manual matching receipt can only be retired once';
    END IF;

    SELECT * INTO owner_operation
    FROM public.operation
    WHERE id = NEW.retired_by_operation_id
    FOR UPDATE;
    IF NOT FOUND OR owner_operation.kind <> 'delete_library_v1' OR owner_operation.state <> 'running'
       OR owner_operation.river_job_id IS NULL
       OR jsonb_array_length(owner_operation.input_snapshot->'residual_publication_ids') <> 0 THEN
        RAISE EXCEPTION 'manual matching receipt retirement lacks current library-deletion authority';
    END IF;
    authorized_release :=
        (owner_operation.input_snapshot->'scope'->>'kind'='release'
         AND owner_operation.input_snapshot->'scope'->>'release_id'=OLD.original_local_release_id::text)
        OR (owner_operation.input_snapshot->'scope'->>'kind'='artist'
            AND owner_operation.input_snapshot->'scope'->'artist_release_ids' @> jsonb_build_array(OLD.original_local_release_id::text));
    IF authorized_release IS NOT TRUE
       OR NOT (owner_operation.input_snapshot->'scope'->'manual_receipt_request_ids' @> jsonb_build_array(OLD.request_id::text)) THEN
        RAISE EXCEPTION 'manual matching receipt retirement lacks current library-deletion authority';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.guard_matching_artist_response() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.accepted_artist_response IS NOT NULL AND (
        NEW.accepted_artist_response IS DISTINCT FROM OLD.accepted_artist_response
        OR NEW.parent_run_id IS DISTINCT FROM OLD.parent_run_id
        OR NEW.request_id IS DISTINCT FROM OLD.request_id
    ) THEN
        RAISE EXCEPTION 'accepted artist matching response is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.guard_matching_local_recheck_incumbent() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.local_apply_operation_id IS DISTINCT FROM OLD.local_apply_operation_id
       OR NEW.incumbent_snapshot IS DISTINCT FROM OLD.incumbent_snapshot THEN
        RAISE EXCEPTION 'matching Local recheck incumbent evidence is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.guard_matching_local_recheck_publication() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.recheck_id IS DISTINCT FROM OLD.recheck_id
       OR NEW.output_epoch IS DISTINCT FROM OLD.output_epoch
       OR NEW.position_id IS DISTINCT FROM OLD.position_id
       OR NEW.candidate_generation IS DISTINCT FROM OLD.candidate_generation
       OR NEW.predecessor_position_id IS DISTINCT FROM OLD.predecessor_position_id
       OR NEW.predecessor_generation IS DISTINCT FROM OLD.predecessor_generation
       OR NEW.predecessor_claim_id IS DISTINCT FROM OLD.predecessor_claim_id
       OR NEW.same_path IS DISTINCT FROM OLD.same_path
       OR NEW.predecessor_snapshot IS DISTINCT FROM OLD.predecessor_snapshot
       OR (OLD.committed_at IS NOT NULL
           AND (NEW.committed_at IS DISTINCT FROM OLD.committed_at
                OR NEW.candidate_payload_sha256 IS DISTINCT FROM OLD.candidate_payload_sha256)) THEN
        RAISE EXCEPTION 'matching Local recheck publication membership is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.guard_publication_reconciliation_event_update() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.matching_run_retirement_is_authorized(target_run uuid) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    SELECT EXISTS (SELECT 1 FROM matching_run_retirement_authorization
                    WHERE run_id = target_run AND transaction_id = pg_current_xact_id())
$$;

CREATE FUNCTION public.prevent_release_artist_truncate_orphans() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '23514',
        MESSAGE = 'cannot truncate release_artist; delete the library graph transactionally';
END
$$;

CREATE FUNCTION public.prevent_release_track_truncate_orphans() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = '23514',
        MESSAGE = 'cannot truncate release_track; delete the library graph transactionally';
END
$$;

CREATE FUNCTION public.prevent_source_analysis_work_execution_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'source analysis work execution identity is immutable';
END;
$$;

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

CREATE FUNCTION public.protect_source_cleanup_explicit_retry_binding() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.protect_source_cleanup_explicit_retry_item() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.protect_source_cleanup_recovery_item() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.protect_source_cleanup_recovery_state() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.protect_source_cleanup_retry_frontier() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF NEW.frontier_revision IS DISTINCT FROM OLD.frontier_revision THEN
    RAISE EXCEPTION 'source cleanup retry frontier revision is immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;

CREATE FUNCTION public.protect_source_cleanup_retry_resolution() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
        IF EXISTS (
            SELECT 1
            FROM public.source_cleanup_root_abandonment abandonment
            WHERE abandonment.original_operation_id=grant_row.original_operation_id
              AND abandonment.transaction_id=pg_current_xact_id()
              AND EXISTS (SELECT 1 FROM jsonb_array_elements(abandonment.evidence->'retry_resolutions') e(value)
                  WHERE e.value=to_jsonb(OLD))
              AND abandonment.evidence->'retry_grant'=to_jsonb(grant_row)
        ) THEN
            RETURN OLD;
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

CREATE FUNCTION public.reject_local_matching_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'local matching receipts are immutable';
END;
$$;

CREATE FUNCTION public.reject_manual_matching_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'manual matching receipts are immutable';
END;
$$;

CREATE FUNCTION public.reject_manual_picker_search_result_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'manual picker search results are immutable';
END;
$$;

CREATE FUNCTION public.reject_matching_acquisition_phase_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching acquisition phase records are append-only';
END;
$$;

CREATE FUNCTION public.reject_matching_acquisition_seal_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching acquisition seals are write-once';
END;
$$;

CREATE FUNCTION public.reject_matching_apply_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(
        (SELECT parent_run_id FROM matching_apply_request WHERE apply_operation_id = OLD.apply_operation_id)) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching apply receipts are immutable';
END;
$$;

CREATE FUNCTION public.reject_matching_fallback_authority_seal_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback authority seals are write-once';
END;
$$;

CREATE FUNCTION public.reject_matching_fallback_calculation_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback calculation receipts are immutable';
END;
$$;

CREATE FUNCTION public.reject_matching_fallback_evaluation_handoff_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback evaluation handoffs are write-once';
END;
$$;

CREATE FUNCTION public.reject_matching_fallback_lookup_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback lookup receipts are immutable';
END;
$$;

CREATE FUNCTION public.reject_matching_fallback_request_registration_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback request registrations are immutable';
END;
$$;

CREATE FUNCTION public.reject_matching_fallback_search_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND matching_run_retirement_is_authorized(OLD.run_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching fallback search receipts are immutable';
END;
$$;

CREATE FUNCTION public.reject_matching_run_retirement_authorization_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.transaction_id = pg_current_xact_id() THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'matching run retirement authorizations are write-once';
END;
$$;

CREATE FUNCTION public.reject_publication_attempt_cover_input_mutation() RETURNS trigger
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

CREATE FUNCTION public.reject_publication_root_abandonment_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'publication root abandonment evidence is immutable';
END;
$$;

CREATE FUNCTION public.reject_source_cleanup_root_abandonment_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'source cleanup root abandonment evidence is immutable';
END $$;

CREATE FUNCTION public.source_analysis_immutable_result() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'source analysis result rows are immutable';
END $$;

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
    ELSIF OLD.invalidated_at IS NOT NULL AND NEW.invalidated_at IS DISTINCT FROM OLD.invalidated_at THEN
        RAISE EXCEPTION 'invalidated source analysis work marker is irreversible';
    ELSIF OLD.current_location_id IS NOT NULL AND NEW.current_location_id IS NULL AND NEW.invalidated_at IS NULL AND EXISTS (
        SELECT 1 FROM operation_source_work_hold hold
        JOIN operation operation ON operation.id=hold.operation_id
        WHERE hold.work_id=OLD.id AND operation.state IN ('queued','running')
    ) THEN
        RAISE EXCEPTION 'active source analysis work cannot be retired';
    END IF;
    IF NEW.invalidated_at IS NOT NULL AND NEW.current_location_id IS NOT NULL THEN
        RAISE EXCEPTION 'invalidated source analysis work cannot remain attached';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.source_analysis_work_execution_requires_live_identity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM 1 FROM source_analysis_work
        WHERE id=NEW.work_id AND current_location_id IS NOT NULL
          AND current_location_id=location_id AND invalidated_at IS NULL
        FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'invalidated source analysis work cannot start execution';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.source_analysis_work_hold_requires_current_attachment() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM 1 FROM source_analysis_work
        WHERE id=NEW.work_id AND current_location_id IS NOT NULL
          AND current_location_id=location_id AND invalidated_at IS NULL
        FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'retired source analysis work cannot be held';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.source_cleanup_frontier_transfer_proof(p_original_operation_id uuid, p_recovery_operation_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE canonical jsonb;
BEGIN
  canonical := public.compute_source_cleanup_frontier_transfer_proof(p_original_operation_id,p_recovery_operation_id);
  INSERT INTO public.source_cleanup_frontier_transfer_proof(transaction_id,original_operation_id,recovery_operation_id,proof)
  VALUES (txid_current(),p_original_operation_id,p_recovery_operation_id,canonical);
  RETURN canonical;
END $$;

CREATE FUNCTION public.source_fingerprint_result_identity_immutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR (OLD.source_sha256 IS NOT NULL AND NEW.source_sha256 IS DISTINCT FROM OLD.source_sha256) THEN
        RAISE EXCEPTION 'source fingerprint result identity is immutable';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.source_work_variant_identity_requires_live_identity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    PERFORM 1 FROM source_analysis_work
        WHERE id=NEW.work_id AND current_location_id IS NOT NULL
          AND current_location_id=location_id AND invalidated_at IS NULL
        FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'invalidated source analysis work cannot acquire media identity';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.valid_library_deletion_residual(snapshot jsonb, operation_state text) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $$
DECLARE residual_id text;
BEGIN
    IF NOT public.valid_library_deletion_uuid_array(snapshot->'residual_publication_ids') THEN
        RETURN false;
    END IF;
    IF operation_state = 'succeeded' AND jsonb_array_length(snapshot->'residual_publication_ids') <> 0 THEN
        RETURN false;
    END IF;
    FOR residual_id IN SELECT jsonb_array_elements_text(snapshot->'residual_publication_ids') LOOP
        IF NOT (snapshot->'scope'->'publication_ids' @> jsonb_build_array(residual_id)) THEN
            RETURN false;
        END IF;
    END LOOP;
    RETURN true;
END;
$$;

CREATE FUNCTION public.valid_library_deletion_uuid_array(values_json jsonb) RETURNS boolean
    LANGUAGE plpgsql IMMUTABLE
    AS $_$
BEGIN
    IF jsonb_typeof(values_json) IS DISTINCT FROM 'array' THEN
        RETURN false;
    END IF;
    RETURN NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(values_json) AS item(value)
        WHERE jsonb_typeof(item.value) IS DISTINCT FROM 'string'
           OR (item.value #>> '{}') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    );
END;
$_$;

CREATE FUNCTION public.valid_source_cleanup_explicit_retry_binding(b jsonb, op_id uuid, app_attempt integer, job_id bigint, claim_id uuid, pos_id uuid, retry_epoch uuid, generation uuid) RETURNS boolean
    LANGUAGE sql IMMUTABLE
    AS $_$
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
$_$;

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

CREATE FUNCTION public.validate_source_cleanup_frontier_transfer_proof() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

-- Tables and sequences
CREATE TABLE public.app_setting (
    setting_name text NOT NULL,
    setting_value text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.collection (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT collection_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT collection_metadata_version_check CHECK ((metadata_version >= 1))
);

CREATE TABLE public.collection_artist (
    collection_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    CONSTRAINT collection_artist_position_check CHECK (("position" >= 0))
);

CREATE TABLE public.collection_release (
    collection_id uuid NOT NULL,
    "position" integer NOT NULL,
    release_id uuid NOT NULL,
    CONSTRAINT collection_release_position_check CHECK (("position" >= 0))
);

CREATE TABLE public.cover_art_archive_image (
    release_mbid uuid NOT NULL,
    image_id text NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    front boolean DEFAULT false NOT NULL,
    back boolean DEFAULT false NOT NULL,
    types jsonb DEFAULT '[]'::jsonb NOT NULL,
    comment text DEFAULT ''::text NOT NULL,
    thumbnail_url text DEFAULT ''::text NOT NULL,
    listed boolean DEFAULT true NOT NULL,
    data bytea,
    mime text,
    sha256 bytea,
    size_bytes bigint,
    downloaded_at timestamp with time zone,
    download_error text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cover_art_archive_image_data_check CHECK ((((data IS NULL) AND (mime IS NULL) AND (sha256 IS NULL) AND (size_bytes IS NULL) AND (downloaded_at IS NULL)) OR ((data IS NOT NULL) AND (octet_length(data) > 0) AND (mime = ANY (ARRAY['image/png'::text, 'image/jpeg'::text])) AND (sha256 IS NOT NULL) AND (octet_length(sha256) = 32) AND (sha256 = sha256(data)) AND (size_bytes = octet_length(data)) AND (size_bytes > 0) AND (downloaded_at IS NOT NULL)))),
    CONSTRAINT cover_art_archive_image_download_error_check CHECK (((download_error IS NULL) OR (length(download_error) > 0))),
    CONSTRAINT cover_art_archive_image_id_check CHECK ((image_id ~ '^[1-9][0-9]*$'::text)),
    CONSTRAINT cover_art_archive_image_position_check CHECK (("position" >= 0)),
    CONSTRAINT cover_art_archive_image_types_check CHECK ((jsonb_typeof(types) = 'array'::text))
);

CREATE TABLE public.cover_art_archive_release (
    release_mbid uuid NOT NULL,
    front_status text DEFAULT 'unchecked'::text NOT NULL,
    front_image_id text,
    front_data bytea,
    front_mime text,
    front_sha256 bytea,
    front_size_bytes bigint,
    front_checked_at timestamp with time zone,
    front_error text,
    gallery_status text DEFAULT 'unchecked'::text NOT NULL,
    gallery_listed_at timestamp with time zone,
    gallery_checked_at timestamp with time zone,
    gallery_error text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cover_art_archive_release_front_error_check CHECK (((front_error IS NULL) OR (length(front_error) > 0))),
    CONSTRAINT cover_art_archive_release_front_status_check CHECK ((front_status = ANY (ARRAY['unchecked'::text, 'present'::text, 'absent'::text]))),
    CONSTRAINT cover_art_archive_release_front_tuple_check CHECK ((((front_status = 'unchecked'::text) AND (front_image_id IS NULL) AND (front_data IS NULL) AND (front_mime IS NULL) AND (front_sha256 IS NULL) AND (front_size_bytes IS NULL) AND (front_checked_at IS NULL)) OR ((front_status = 'absent'::text) AND (front_image_id IS NULL) AND (front_data IS NULL) AND (front_mime IS NULL) AND (front_sha256 IS NULL) AND (front_size_bytes IS NULL) AND (front_checked_at IS NOT NULL)) OR ((front_status = 'present'::text) AND (front_data IS NOT NULL) AND (octet_length(front_data) > 0) AND (front_mime = ANY (ARRAY['image/png'::text, 'image/jpeg'::text])) AND (front_sha256 IS NOT NULL) AND (octet_length(front_sha256) = 32) AND (front_sha256 = sha256(front_data)) AND (front_size_bytes = octet_length(front_data)) AND (front_size_bytes > 0) AND (front_checked_at IS NOT NULL)))),
    CONSTRAINT cover_art_archive_release_gallery_complete_check CHECK ((((gallery_status = 'unchecked'::text) AND (gallery_checked_at IS NULL)) OR ((gallery_status = 'complete'::text) AND (gallery_checked_at IS NOT NULL) AND (gallery_listed_at IS NOT NULL)))),
    CONSTRAINT cover_art_archive_release_gallery_error_check CHECK (((gallery_error IS NULL) OR (length(gallery_error) > 0))),
    CONSTRAINT cover_art_archive_release_gallery_status_check CHECK ((gallery_status = ANY (ARRAY['unchecked'::text, 'complete'::text])))
);

CREATE TABLE public.genre_catalog (
    singleton boolean DEFAULT true NOT NULL,
    items jsonb DEFAULT '[]'::jsonb NOT NULL,
    updated_at timestamp with time zone,
    last_attempt_at timestamp with time zone,
    last_error text,
    configuration_identity text DEFAULT ''::text NOT NULL,
    refresh_request jsonb DEFAULT '{"status": "idle"}'::jsonb NOT NULL,
    refresh_due_at timestamp with time zone,
    published_configuration_identity text,
    CONSTRAINT genre_catalog_items_check CHECK ((jsonb_typeof(items) = 'array'::text)),
    CONSTRAINT genre_catalog_refresh_request_check CHECK ((jsonb_typeof(refresh_request) = 'object'::text)),
    CONSTRAINT genre_catalog_singleton_check CHECK (singleton)
);

CREATE TABLE public.incoming_group (
    id uuid NOT NULL,
    manual boolean DEFAULT false NOT NULL,
    revision text NOT NULL,
    diagnostics jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_diagnostics_check CHECK ((jsonb_typeof(diagnostics) = 'array'::text))
);

CREATE TABLE public.incoming_group_matching_authority (
    group_id uuid NOT NULL,
    mode text NOT NULL,
    revision bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_matching_authority_mode_check CHECK ((mode = ANY (ARRAY['automatic'::text, 'manual'::text]))),
    CONSTRAINT incoming_group_matching_authority_revision_check CHECK ((revision >= 0))
);

CREATE TABLE public.incoming_group_member (
    group_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    ready boolean DEFAULT true NOT NULL
);

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

CREATE TABLE public.incoming_group_return_automatic_receipt (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_return_automatic_receipt_intent_hash_check CHECK ((intent_hash ~ '^[0-9a-f]{64}$'::text))
);

CREATE TABLE public.incoming_group_unlink_receipt (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    intent_hash text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_group_unlink_receipt_intent_hash_check CHECK ((length(intent_hash) = 64)),
    CONSTRAINT incoming_group_unlink_receipt_response_check CHECK ((jsonb_typeof(response) = 'object'::text))
);

CREATE TABLE public.incoming_grouping_state (
    singleton boolean DEFAULT true NOT NULL,
    revision bigint DEFAULT 0 NOT NULL,
    needs_refresh boolean DEFAULT true NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_grouping_state_revision_check CHECK ((revision >= 0)),
    CONSTRAINT incoming_grouping_state_singleton_check CHECK (singleton)
);

CREATE TABLE public.incoming_match_evaluation (
    group_id uuid NOT NULL,
    group_revision text NOT NULL,
    groups_revision text NOT NULL,
    grouping_epoch bigint NOT NULL,
    policy_version text NOT NULL,
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_configuration_identity text CONSTRAINT incoming_match_evaluation_provider_configuration_ident_not_null NOT NULL,
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
    CONSTRAINT incoming_match_evaluation_frozen_authority_all_or_none CHECK ((((frozen_authority_version IS NULL) AND (frozen_run_id IS NULL) AND (frozen_wait_revision IS NULL) AND (frozen_seal_digest IS NULL)) OR ((frozen_authority_version IS NOT NULL) AND (frozen_run_id IS NOT NULL) AND (frozen_wait_revision IS NOT NULL) AND (frozen_seal_digest IS NOT NULL) AND (frozen_authority_version = 1) AND (frozen_wait_revision > 0) AND (octet_length(frozen_seal_digest) = 32) AND (target_run_id IS NOT NULL) AND (fence_version = 2) AND (matching_authority_mode = 'automatic'::text)))),
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

CREATE TABLE public.incoming_match_evaluation_member (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    work_id uuid NOT NULL,
    source_sha256 bytea,
    CONSTRAINT incoming_match_evaluation_member_source_sha256_check CHECK (((source_sha256 IS NULL) OR (octet_length(source_sha256) = 32)))
);

CREATE TABLE public.incoming_matching_automatic_admission (
    fingerprint bytea NOT NULL,
    original_run_id uuid NOT NULL,
    original_operation_id uuid CONSTRAINT incoming_matching_automatic_admi_original_operation_id_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT incoming_matching_automatic_admission_fingerprint_check CHECK ((octet_length(fingerprint) = 32))
);

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

CREATE TABLE public.incoming_matching_reconcile_event (
    id uuid NOT NULL,
    source_operation_id uuid,
    source_attempt integer,
    reason text NOT NULL,
    source_input_snapshot jsonb CONSTRAINT incoming_matching_reconcile_even_source_input_snapshot_not_null NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    registered_job_id bigint,
    delivery_epoch bigint DEFAULT 0 NOT NULL,
    highest_delivery_attempt integer DEFAULT 0 CONSTRAINT incoming_matching_reconcile_e_highest_delivery_attempt_not_null NOT NULL,
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

CREATE TABLE public.library_artist_photo (
    config_identity text NOT NULL,
    artist_mbid uuid NOT NULL,
    image_url text NOT NULL,
    source_url text NOT NULL,
    author text DEFAULT ''::text NOT NULL,
    license text NOT NULL,
    license_url text,
    image_data bytea NOT NULL,
    image_mime text NOT NULL,
    image_sha256 bytea NOT NULL,
    image_size_bytes bigint NOT NULL,
    image_width integer NOT NULL,
    image_height integer NOT NULL,
    fetched_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_artist_photo_config_identity_check CHECK (((config_identity = btrim(config_identity)) AND (length(config_identity) > 0))),
    CONSTRAINT library_artist_photo_data_check CHECK (((octet_length(image_data) > 0) AND (octet_length(image_data) <= 1048576) AND (image_mime = ANY (ARRAY['image/png'::text, 'image/jpeg'::text, 'image/webp'::text, 'image/gif'::text])) AND (octet_length(image_sha256) = 32) AND (image_sha256 = sha256(image_data)) AND (image_size_bytes = octet_length(image_data)) AND (image_width > 0) AND (image_width <= 400) AND (image_height > 0) AND (image_height <= 400))),
    CONSTRAINT library_artist_photo_image_url_check CHECK ((image_url ~ '^https://(upload|thumb)\.wikimedia\.org/wikipedia/commons/'::text)),
    CONSTRAINT library_artist_photo_license_check CHECK (((license = btrim(license)) AND (length(license) > 0))),
    CONSTRAINT library_artist_photo_license_url_check CHECK (((license_url IS NULL) OR (license_url ~ '^https://creativecommons\.org/(licenses|publicdomain)/'::text))),
    CONSTRAINT library_artist_photo_source_url_check CHECK ((source_url ~ '^https://commons\.wikimedia\.org/wiki/File:'::text))
);

CREATE TABLE public.local_artist (
    id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    metadata_revision bigint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    catalog_visible boolean DEFAULT true NOT NULL,
    CONSTRAINT local_artist_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_artist_metadata_revision_check CHECK ((metadata_revision >= 1)),
    CONSTRAINT local_artist_metadata_version_check CHECK ((metadata_version >= 1))
);

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

CREATE TABLE public.local_recording (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    metadata_revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT local_recording_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_recording_metadata_revision_check CHECK ((metadata_revision >= 1)),
    CONSTRAINT local_recording_metadata_version_check CHECK ((metadata_version >= 1))
);

CREATE TABLE public.local_release (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    metadata_revision bigint DEFAULT 1 NOT NULL,
    primary_artist_id uuid,
    CONSTRAINT local_release_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_release_metadata_revision_check CHECK ((metadata_revision >= 1)),
    CONSTRAINT local_release_metadata_version_check CHECK ((metadata_version >= 1))
);

CREATE TABLE public.local_track (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    metadata_revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT local_track_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_track_metadata_revision_check CHECK ((metadata_revision >= 1)),
    CONSTRAINT local_track_metadata_version_check CHECK ((metadata_version >= 1))
);

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

CREATE TABLE public.manual_matching_receipt (
    request_id uuid NOT NULL,
    group_id uuid NOT NULL,
    local_release_id uuid,
    intent_hash text NOT NULL,
    preview_digest text NOT NULL,
    response jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    original_local_release_id uuid NOT NULL,
    retired_by_operation_id uuid,
    CONSTRAINT manual_matching_receipt_intent_hash_check CHECK ((intent_hash <> ''::text)),
    CONSTRAINT manual_matching_receipt_preview_digest_check CHECK ((preview_digest <> ''::text)),
    CONSTRAINT manual_matching_receipt_retirement_shape CHECK ((((local_release_id IS NOT NULL) AND (local_release_id = original_local_release_id) AND (retired_by_operation_id IS NULL)) OR ((local_release_id IS NULL) AND (retired_by_operation_id IS NOT NULL))))
);

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

CREATE TABLE public.matching_apply_receipt (
    apply_operation_id uuid NOT NULL,
    group_id uuid NOT NULL,
    outcome text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL,
    input_sha256 bytea,
    CONSTRAINT matching_apply_receipt_input_sha256_length CHECK (((input_sha256 IS NULL) OR (octet_length(input_sha256) = 32))),
    CONSTRAINT matching_apply_receipt_outcome_check CHECK ((outcome = ANY (ARRAY['applied'::text, 'review_required'::text])))
);

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
    CONSTRAINT matching_apply_request_v2_binding CHECK ((((schema_version = 1) AND (input_sha256 IS NULL) AND (frozen_authority_version IS NULL) AND (parent_execution_epoch IS NULL) AND (seal_wait_revision IS NULL) AND (seal_digest IS NULL) AND (target_capture_sha256 IS NULL)) OR ((schema_version = 2) AND (input_sha256 IS NOT NULL) AND (octet_length(input_sha256) = 32) AND (frozen_authority_version IS NOT NULL) AND (frozen_authority_version = 1) AND (parent_execution_epoch IS NOT NULL) AND (parent_execution_epoch > 0) AND (seal_wait_revision IS NOT NULL) AND (seal_wait_revision > 0) AND (seal_digest IS NOT NULL) AND (octet_length(seal_digest) = 32) AND (target_capture_sha256 IS NOT NULL) AND (octet_length(target_capture_sha256) = 32))))
);

CREATE TABLE public.matching_delivery (
    id bigint NOT NULL,
    run_id uuid NOT NULL,
    river_job_id bigint NOT NULL,
    registered_at timestamp with time zone DEFAULT now() NOT NULL,
    consumed_at timestamp with time zone,
    operation_attempt integer NOT NULL,
    CONSTRAINT matching_delivery_attempt_positive CHECK ((operation_attempt > 0))
);

CREATE SEQUENCE public.matching_delivery_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.matching_delivery_id_seq OWNED BY public.matching_delivery.id;

CREATE TABLE public.matching_fallback_authority_seal (
    run_id uuid NOT NULL,
    wait_revision bigint NOT NULL,
    schema_version integer NOT NULL,
    authority jsonb NOT NULL,
    authority_digest bytea NOT NULL,
    sealed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_fallback_authority_seal_authority_check CHECK ((jsonb_typeof(authority) = 'object'::text)),
    CONSTRAINT matching_fallback_authority_seal_authority_digest_check CHECK ((octet_length(authority_digest) = 32)),
    CONSTRAINT matching_fallback_authority_seal_schema_version_check CHECK ((schema_version = 1)),
    CONSTRAINT matching_fallback_authority_seal_wait_revision_check CHECK ((wait_revision > 0))
);

CREATE TABLE public.matching_fallback_calculation_receipt (
    run_id uuid NOT NULL,
    acquisition_revision bigint CONSTRAINT matching_fallback_calculation_rec_acquisition_revision_not_null NOT NULL,
    stage_index integer NOT NULL,
    schema_version integer NOT NULL,
    input_digest bytea NOT NULL,
    context jsonb NOT NULL,
    layout_decision jsonb CONSTRAINT matching_fallback_calculation_receip_edge_calculations_not_null NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    decision_digest bytea,
    CONSTRAINT matching_fallback_calculation_receip_acquisition_revision_check CHECK ((acquisition_revision > 0)),
    CONSTRAINT matching_fallback_calculation_receipt_context_check CHECK ((jsonb_typeof(context) = 'object'::text)),
    CONSTRAINT matching_fallback_calculation_receipt_input_digest_check CHECK ((octet_length(input_digest) = 32)),
    CONSTRAINT matching_fallback_calculation_receipt_stage_index_check CHECK ((stage_index >= 0))
);

CREATE TABLE public.matching_fallback_evaluation_handoff (
    run_id uuid NOT NULL,
    wait_revision bigint NOT NULL,
    provider_authority_version integer CONSTRAINT matching_fallback_evaluatio_provider_authority_version_not_null NOT NULL,
    seal_digest bytea NOT NULL,
    river_job_id bigint NOT NULL,
    operation_attempt integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_fallback_evaluation_h_provider_authority_version_check CHECK ((provider_authority_version = 1)),
    CONSTRAINT matching_fallback_evaluation_handoff_operation_attempt_check CHECK ((operation_attempt > 0)),
    CONSTRAINT matching_fallback_evaluation_handoff_seal_digest_check CHECK ((octet_length(seal_digest) = 32)),
    CONSTRAINT matching_fallback_evaluation_handoff_wait_revision_check CHECK ((wait_revision > 0))
);

CREATE TABLE public.matching_fallback_lookup_receipt (
    run_id uuid NOT NULL,
    request_id uuid NOT NULL,
    stage_index integer NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text CONSTRAINT matching_fallback_lookup_receip_configuration_identity_not_null NOT NULL,
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

CREATE TABLE public.matching_fallback_request_registration (
    run_id uuid NOT NULL,
    stage_index integer NOT NULL,
    role text NOT NULL,
    request_id uuid NOT NULL,
    provider_source_id uuid CONSTRAINT matching_fallback_request_registrat_provider_source_id_not_null NOT NULL,
    configuration_identity text CONSTRAINT matching_fallback_request_regis_configuration_identity_not_null NOT NULL,
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

CREATE TABLE public.matching_fallback_search_receipt (
    run_id uuid NOT NULL,
    request_id uuid NOT NULL,
    stage_index integer NOT NULL,
    provider_source_id uuid NOT NULL,
    configuration_identity text CONSTRAINT matching_fallback_search_receip_configuration_identity_not_null NOT NULL,
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
    payload_digest_version smallint DEFAULT 2 CONSTRAINT matching_fallback_search_receip_payload_digest_version_not_null NOT NULL,
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

CREATE TABLE public.matching_local_recheck (
    id uuid NOT NULL,
    local_apply_operation_id uuid NOT NULL,
    state text NOT NULL,
    next_recheck_at timestamp with time zone NOT NULL,
    incumbent_snapshot jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_local_recheck_incumbent_snapshot_check CHECK ((((jsonb_typeof(incumbent_snapshot) = 'object'::text) IS TRUE) AND ((incumbent_snapshot ? 'version'::text) IS TRUE) AND ((jsonb_typeof((incumbent_snapshot -> 'version'::text)) = 'number'::text) IS TRUE) AND (((incumbent_snapshot ->> 'version'::text) = '1'::text) IS TRUE))),
    CONSTRAINT matching_local_recheck_state_check CHECK ((state = ANY (ARRAY['waiting'::text, 'publishing'::text])))
);

CREATE TABLE public.matching_local_recheck_publication (
    operation_id uuid NOT NULL,
    recheck_id uuid NOT NULL,
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    candidate_generation uuid CONSTRAINT matching_local_recheck_publicatio_candidate_generation_not_null NOT NULL,
    predecessor_position_id uuid CONSTRAINT matching_local_recheck_publica_predecessor_position_id_not_null NOT NULL,
    predecessor_generation uuid CONSTRAINT matching_local_recheck_publicat_predecessor_generation_not_null NOT NULL,
    predecessor_claim_id uuid CONSTRAINT matching_local_recheck_publicatio_predecessor_claim_id_not_null NOT NULL,
    same_path boolean NOT NULL,
    predecessor_snapshot jsonb CONSTRAINT matching_local_recheck_publicatio_predecessor_snapshot_not_null NOT NULL,
    committed_at timestamp with time zone,
    candidate_payload_sha256 text,
    CONSTRAINT matching_local_recheck_publication_candidate_payload_check CHECK (((candidate_payload_sha256 IS NULL) OR ((NOT same_path) AND (candidate_payload_sha256 ~ '^[0-9a-f]{64}$'::text)))),
    CONSTRAINT matching_local_recheck_publication_predecessor_snapshot_check CHECK ((((jsonb_typeof(predecessor_snapshot) = 'object'::text) AND ((predecessor_snapshot -> 'version'::text) = '1'::jsonb) AND ((predecessor_snapshot ->> 'position_id'::text) = (predecessor_position_id)::text) AND ((predecessor_snapshot ->> 'output_epoch'::text) = (output_epoch)::text) AND ((predecessor_snapshot ->> 'generation'::text) = (predecessor_generation)::text) AND ((predecessor_snapshot ->> 'namespace_claim_id'::text) = (predecessor_claim_id)::text) AND (jsonb_typeof((predecessor_snapshot -> 'relative_path'::text)) = 'string'::text) AND ((predecessor_snapshot ->> 'relative_path'::text) <> ''::text) AND (jsonb_typeof((predecessor_snapshot -> 'current'::text)) = 'object'::text) AND (((predecessor_snapshot -> 'current'::text) ->> 'generation'::text) = (predecessor_generation)::text) AND (((predecessor_snapshot -> 'current'::text) ->> 'relative_path'::text) = (predecessor_snapshot ->> 'relative_path'::text)) AND ((position_id <> predecessor_position_id) OR same_path)) IS TRUE))
);

CREATE TABLE public.matching_local_recheck_source (
    recheck_id uuid NOT NULL,
    work_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    unlink_target text NOT NULL,
    CONSTRAINT matching_local_recheck_source_unlink_target_check CHECK ((unlink_target <> ''::text))
);

CREATE TABLE public.matching_provider_dependency (
    parent_run_id uuid NOT NULL,
    request_id uuid NOT NULL,
    cache_key text NOT NULL,
    terminal_outcome text,
    settled_at timestamp with time zone,
    accepted_artist_response jsonb,
    CONSTRAINT matching_artist_response_shape CHECK (((accepted_artist_response IS NULL) OR (((jsonb_typeof(accepted_artist_response) = 'object'::text) AND (jsonb_typeof((accepted_artist_response -> 'payload'::text)) = 'object'::text) AND ((accepted_artist_response ->> 'kind'::text) = ANY (ARRAY['provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_release_lookup'::text])) AND ((accepted_artist_response ->> 'cache_key'::text) = cache_key) AND ((NOT (accepted_artist_response ? 'revision'::text)) OR (jsonb_typeof((accepted_artist_response -> 'revision'::text)) = 'string'::text)) AND
CASE
    WHEN ((accepted_artist_response ->> 'kind'::text) = 'provider_release_lookup'::text) THEN ((jsonb_typeof((accepted_artist_response -> 'projection'::text)) = 'object'::text) AND (jsonb_typeof(((accepted_artist_response -> 'projection'::text) -> 'Source'::text)) = 'object'::text) AND (jsonb_typeof(((accepted_artist_response -> 'projection'::text) -> 'Releases'::text)) = 'array'::text) AND (jsonb_typeof(((accepted_artist_response -> 'projection'::text) -> 'Responses'::text)) = 'object'::text))
    ELSE (NOT (accepted_artist_response ? 'projection'::text))
END) IS TRUE))),
    CONSTRAINT matching_provider_dependency_cache_key_check CHECK ((length(btrim(cache_key)) > 0)),
    CONSTRAINT matching_provider_dependency_check CHECK (((terminal_outcome IS NULL) = (settled_at IS NULL))),
    CONSTRAINT matching_provider_dependency_terminal_outcome_check CHECK (((terminal_outcome IS NULL) OR (terminal_outcome = ANY (ARRAY['succeeded'::text, 'failed'::text]))))
);

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

CREATE TABLE public.matching_run_retirement_authorization (
    run_id uuid NOT NULL,
    transaction_id xid8 DEFAULT pg_current_xact_id() NOT NULL,
    authorized_at timestamp with time zone DEFAULT transaction_timestamp() NOT NULL,
    cleanup_cutoff timestamp with time zone DEFAULT (transaction_timestamp() - '24:00:00'::interval) NOT NULL,
    authorization_kind text DEFAULT 'automatic_fallback'::text CONSTRAINT matching_run_retirement_authorizati_authorization_kind_not_null NOT NULL,
    CONSTRAINT matching_run_retirement_authorization_authorization_kind_check CHECK ((authorization_kind = ANY (ARRAY['automatic_fallback'::text, 'obsolete_quiesced'::text]))),
    CONSTRAINT matching_run_retirement_authorization_obsolete_v2 CHECK (((authorization_kind <> 'obsolete_quiesced'::text) OR (cleanup_cutoff <= (authorized_at - '24:00:00'::interval))))
);

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

CREATE TABLE public.media_metadata_result (
    id uuid NOT NULL,
    source_sha256 bytea,
    observed_tags jsonb NOT NULL,
    provenance jsonb NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    winning_result_id uuid NOT NULL,
    applied_operation_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    cover_observed boolean DEFAULT false NOT NULL,
    cover_data bytea,
    cover_mime text,
    cover_sha256 bytea,
    cover_size_bytes bigint,
    properties jsonb,
    cover_error text,
    tag_encoding jsonb,
    CONSTRAINT media_metadata_result_cover_error_check CHECK (((cover_error IS NULL) OR ((length(cover_error) > 0) AND (cover_observed IS TRUE) AND (cover_data IS NULL)))),
    CONSTRAINT media_metadata_result_cover_facts_check CHECK ((((cover_data IS NULL) AND (cover_mime IS NULL) AND (cover_sha256 IS NULL) AND (cover_size_bytes IS NULL)) OR ((cover_data IS NOT NULL) AND (cover_mime IS NOT NULL) AND (cover_sha256 IS NOT NULL) AND (cover_size_bytes IS NOT NULL) AND (cover_observed IS TRUE) AND (cover_mime ~~ 'image/%'::text) AND (octet_length(cover_sha256) = 32) AND (cover_size_bytes = octet_length(cover_data))))),
    CONSTRAINT media_metadata_result_observed_tags_check CHECK ((jsonb_typeof(observed_tags) = 'object'::text)),
    CONSTRAINT media_metadata_result_provenance_check CHECK ((jsonb_typeof(provenance) = 'object'::text)),
    CONSTRAINT media_metadata_result_sha256_length CHECK (((source_sha256 IS NULL) OR (octet_length(source_sha256) = 32))),
    CONSTRAINT media_metadata_result_tag_encoding_check CHECK (((tag_encoding IS NULL) OR ((jsonb_typeof(tag_encoding) = 'object'::text) IS TRUE)))
);

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

CREATE TABLE public.obsolete_publication_output_cleanup (
    operation_id uuid NOT NULL,
    epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    generation uuid NOT NULL,
    relative_path text NOT NULL,
    native_path text NOT NULL,
    output_root_identity jsonb CONSTRAINT obsolete_publication_output_clean_output_root_identity_not_null NOT NULL,
    parent_identity jsonb NOT NULL,
    namespace_claim_id uuid NOT NULL,
    claim_node_kind text GENERATED ALWAYS AS ('file'::text) STORED NOT NULL,
    file_size bigint NOT NULL,
    modified_at timestamp with time zone NOT NULL,
    sha256 bytea NOT NULL,
    state text NOT NULL,
    safe_error text,
    CONSTRAINT obsolete_publication_output_cleanup_directory_identities_check CHECK ((((jsonb_typeof(output_root_identity) = 'object'::text) AND (jsonb_typeof(parent_identity) = 'object'::text) AND (output_root_identity ?& ARRAY['platform'::text, 'volume'::text, 'file_id'::text]) AND (parent_identity ?& ARRAY['platform'::text, 'volume'::text, 'file_id'::text]) AND (jsonb_typeof((output_root_identity -> 'platform'::text)) = 'string'::text) AND (jsonb_typeof((output_root_identity -> 'volume'::text)) = 'string'::text) AND (jsonb_typeof((output_root_identity -> 'file_id'::text)) = 'string'::text) AND (jsonb_typeof((parent_identity -> 'platform'::text)) = 'string'::text) AND (jsonb_typeof((parent_identity -> 'volume'::text)) = 'string'::text) AND (jsonb_typeof((parent_identity -> 'file_id'::text)) = 'string'::text) AND ((output_root_identity ->> 'platform'::text) <> ''::text) AND ((output_root_identity ->> 'volume'::text) <> ''::text) AND ((output_root_identity ->> 'file_id'::text) <> ''::text) AND ((parent_identity ->> 'platform'::text) <> ''::text) AND ((parent_identity ->> 'volume'::text) <> ''::text) AND ((parent_identity ->> 'file_id'::text) <> ''::text) AND (((output_root_identity ->> 'platform'::text) = (parent_identity ->> 'platform'::text)) IS TRUE)) IS TRUE)),
    CONSTRAINT obsolete_publication_output_cleanup_error_check CHECK ((((state = 'failed'::text) AND (safe_error IS NOT NULL) AND (safe_error <> ''::text)) OR ((state <> 'failed'::text) AND (safe_error IS NULL)))),
    CONSTRAINT obsolete_publication_output_cleanup_ids_check CHECK (((operation_id <> '00000000-0000-0000-0000-000000000000'::uuid) AND (epoch <> '00000000-0000-0000-0000-000000000000'::uuid) AND (position_id <> '00000000-0000-0000-0000-000000000000'::uuid) AND (generation <> '00000000-0000-0000-0000-000000000000'::uuid) AND (namespace_claim_id <> '00000000-0000-0000-0000-000000000000'::uuid))),
    CONSTRAINT obsolete_publication_output_cleanup_native_path_check CHECK ((((native_path ~ '^/'::text) OR (native_path ~ '^[A-Za-z]:[/\\\\]'::text)) IS TRUE)),
    CONSTRAINT obsolete_publication_output_cleanup_path_check CHECK (((relative_path <> ''::text) AND (relative_path !~ '(^/|\\|:|(^|/)\.\.?(/|$))'::text) AND (native_path <> ''::text))),
    CONSTRAINT obsolete_publication_output_cleanup_sha256_check CHECK ((octet_length(sha256) = 32)),
    CONSTRAINT obsolete_publication_output_cleanup_size_check CHECK ((file_size >= 0)),
    CONSTRAINT obsolete_publication_output_cleanup_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'failed'::text])))
);

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
    CONSTRAINT operation_kind_check CHECK ((kind = ANY (ARRAY['install'::text, 'activate'::text, 'delete'::text, 'delete_library_v1'::text, 'move_tools_root'::text, 'scan_source'::text, 'analyze_source'::text, 'cleanup_source_analysis_artifacts'::text, 'provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text, 'matching_evaluation_v1'::text, 'matching_apply_v1'::text, 'publish_track_v1'::text, 'cleanup_publication_sources_v1'::text, 'publication_reconcile_v1'::text, 'retry_publication_sources_v1'::text, 'obsolete_publication_output_cleanup_v1'::text, 'refresh_genre_catalog_v1'::text]))),
    CONSTRAINT operation_library_deletion_residual_subset CHECK (((kind <> 'delete_library_v1'::text) OR (public.valid_library_deletion_residual(input_snapshot, state) IS TRUE))),
    CONSTRAINT operation_library_deletion_shape CHECK (((kind <> 'delete_library_v1'::text) OR (((target_installation_id IS NULL) AND (target_source_root_id IS NULL) AND (target_source_location_id IS NULL) AND (source_analysis_mode IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (provider_source_id IS NULL) AND (provider_configuration_identity IS NULL) AND (provider_cache_key IS NULL) AND (provider_explicit_refresh IS FALSE) AND (jsonb_typeof(input_snapshot) = 'object'::text) AND (input_snapshot ?& ARRAY['schema_version'::text, 'scope'::text, 'scope_digest'::text, 'confirmation_digest'::text, 'ownership_witnesses'::text, 'residual_publication_ids'::text, 'dependencies'::text]) AND ((input_snapshot - ARRAY['schema_version'::text, 'scope'::text, 'scope_digest'::text, 'confirmation_digest'::text, 'ownership_witnesses'::text, 'residual_publication_ids'::text, 'dependencies'::text]) = '{}'::jsonb) AND ((input_snapshot ->> 'schema_version'::text) = '2'::text) AND (jsonb_typeof((input_snapshot -> 'scope'::text)) = 'object'::text) AND (jsonb_typeof((input_snapshot -> 'dependencies'::text)) = 'object'::text) AND (jsonb_typeof((input_snapshot -> 'ownership_witnesses'::text)) = 'array'::text) AND (jsonb_typeof((input_snapshot -> 'residual_publication_ids'::text)) = 'array'::text) AND (length((input_snapshot ->> 'scope_digest'::text)) = 64) AND ((input_snapshot ->> 'scope_digest'::text) ~ '^[0-9a-f]{64}$'::text) AND (length((input_snapshot ->> 'confirmation_digest'::text)) = 64) AND ((input_snapshot ->> 'confirmation_digest'::text) ~ '^[0-9a-f]{64}$'::text)) IS TRUE))),
    CONSTRAINT operation_library_deletion_target_shape CHECK (((kind <> 'delete_library_v1'::text) OR (((jsonb_typeof((input_snapshot -> 'scope'::text)) = 'object'::text) AND (jsonb_typeof(((input_snapshot -> 'scope'::text) -> 'publication_ids'::text)) = 'array'::text) AND (jsonb_typeof(((input_snapshot -> 'scope'::text) -> 'manual_receipt_request_ids'::text)) = 'array'::text) AND (public.valid_library_deletion_uuid_array(((input_snapshot -> 'scope'::text) -> 'publication_ids'::text)) IS TRUE) AND (public.valid_library_deletion_uuid_array(((input_snapshot -> 'scope'::text) -> 'manual_receipt_request_ids'::text)) IS TRUE) AND (length(((input_snapshot -> 'scope'::text) ->> 'dependency_digest'::text)) = 64) AND (((input_snapshot -> 'scope'::text) ->> 'dependency_digest'::text) ~ '^[0-9a-f]{64}$'::text) AND (((((input_snapshot -> 'scope'::text) ->> 'kind'::text) = ANY (ARRAY['publication'::text, 'release'::text])) AND ((input_snapshot -> 'scope'::text) ?& ARRAY['release_id'::text, 'kind'::text, 'publication_ids'::text, 'manual_receipt_request_ids'::text, 'dependency_digest'::text]) AND (((input_snapshot -> 'scope'::text) - ARRAY['release_id'::text, 'kind'::text, 'publication_ids'::text, 'manual_receipt_request_ids'::text, 'dependency_digest'::text]) = '{}'::jsonb) AND (((input_snapshot -> 'scope'::text) ->> 'release_id'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) AND ((((input_snapshot -> 'scope'::text) ->> 'kind'::text) = 'release'::text) OR ((jsonb_array_length(((input_snapshot -> 'scope'::text) -> 'publication_ids'::text)) = 1) AND (jsonb_array_length(((input_snapshot -> 'scope'::text) -> 'manual_receipt_request_ids'::text)) = 0)))) OR ((((input_snapshot -> 'scope'::text) ->> 'kind'::text) = 'artist'::text) AND ((input_snapshot -> 'scope'::text) ?& ARRAY['artist_id'::text, 'artist_release_ids'::text, 'artist_reference_release_ids'::text, 'warning_release_ids'::text, 'kind'::text, 'publication_ids'::text, 'manual_receipt_request_ids'::text, 'dependency_digest'::text]) AND (((input_snapshot -> 'scope'::text) - ARRAY['artist_id'::text, 'artist_release_ids'::text, 'artist_reference_release_ids'::text, 'warning_release_ids'::text, 'kind'::text, 'publication_ids'::text, 'manual_receipt_request_ids'::text, 'dependency_digest'::text]) = '{}'::jsonb) AND (((input_snapshot -> 'scope'::text) ->> 'artist_id'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) AND (jsonb_typeof(((input_snapshot -> 'scope'::text) -> 'artist_release_ids'::text)) = 'array'::text) AND (jsonb_typeof(((input_snapshot -> 'scope'::text) -> 'artist_reference_release_ids'::text)) = 'array'::text) AND (jsonb_typeof(((input_snapshot -> 'scope'::text) -> 'warning_release_ids'::text)) = 'array'::text) AND (public.valid_library_deletion_uuid_array(((input_snapshot -> 'scope'::text) -> 'artist_release_ids'::text)) IS TRUE) AND (public.valid_library_deletion_uuid_array(((input_snapshot -> 'scope'::text) -> 'artist_reference_release_ids'::text)) IS TRUE) AND (public.valid_library_deletion_uuid_array(((input_snapshot -> 'scope'::text) -> 'warning_release_ids'::text)) IS TRUE)))) IS TRUE))),
    CONSTRAINT operation_mutation_has_target_installation CHECK (((kind = ANY (ARRAY['publish_track_v1'::text, 'cleanup_publication_sources_v1'::text, 'publication_reconcile_v1'::text, 'retry_publication_sources_v1'::text, 'obsolete_publication_output_cleanup_v1'::text, 'refresh_genre_catalog_v1'::text, 'delete_library_v1'::text])) OR (kind = ANY (ARRAY['move_tools_root'::text, 'scan_source'::text, 'analyze_source'::text, 'cleanup_source_analysis_artifacts'::text, 'provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text, 'matching_evaluation_v1'::text, 'matching_apply_v1'::text])) OR (state <> ALL (ARRAY['queued'::text, 'running'::text])) OR (target_installation_id IS NOT NULL))),
    CONSTRAINT operation_normalized_analysis_shape CHECK ((((kind <> 'analyze_source'::text) OR (state <> ALL (ARRAY['queued'::text, 'running'::text])) OR (source_analysis_mode IS NOT NULL)) AND (((source_analysis_mode IS NULL) OR ((kind = 'analyze_source'::text) AND (((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_source_root_id IS NOT NULL) AND (((source_analysis_mode = 'batch'::text) AND (target_source_location_id IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL)) OR ((source_analysis_mode = 'single_step'::text) AND (target_source_location_id IS NOT NULL) AND (target_work_id IS NOT NULL) AND (target_step = ANY (ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text]))))) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_source_root_id IS NULL) AND (target_source_location_id IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL))))) IS TRUE))),
    CONSTRAINT operation_obsolete_output_cleanup_shape_check CHECK ((((kind <> 'obsolete_publication_output_cleanup_v1'::text) OR ((input_snapshot = jsonb_build_object('operation_id', (id)::text)) AND (source_analysis_mode IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (target_installation_id IS NULL) AND (target_source_root_id IS NULL) AND (target_source_location_id IS NULL) AND (provider_source_id IS NULL) AND (provider_configuration_identity IS NULL) AND (provider_cache_key IS NULL) AND (NOT rerun_target) AND (NOT tools_read_required) AND (NOT provider_explicit_refresh))) IS TRUE)),
    CONSTRAINT operation_provider_execution_epoch_check CHECK ((provider_execution_epoch >= 0)),
    CONSTRAINT operation_provider_intent CHECK ((((kind = ANY (ARRAY['provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text])) AND (provider_source_id IS NOT NULL) AND (COALESCE(provider_configuration_identity, ''::text) <> ''::text) AND (COALESCE(provider_cache_key, ''::text) <> ''::text)) OR ((kind <> ALL (ARRAY['provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text])) AND (provider_source_id IS NULL) AND (provider_configuration_identity IS NULL) AND (provider_cache_key IS NULL) AND (provider_explicit_refresh = false)))),
    CONSTRAINT operation_publication_reconciliation_snapshot_check CHECK (((kind <> 'publication_reconcile_v1'::text) OR (((jsonb_typeof(input_snapshot) = 'object'::text) AND ((input_snapshot - ARRAY['version'::text, 'position_id'::text, 'output_epoch'::text]) = '{}'::jsonb) AND (input_snapshot ?& ARRAY['version'::text, 'position_id'::text, 'output_epoch'::text]) AND (jsonb_typeof((input_snapshot -> 'version'::text)) = 'number'::text) AND ((input_snapshot ->> 'version'::text) = '1'::text) AND (jsonb_typeof((input_snapshot -> 'position_id'::text)) = 'string'::text) AND ((input_snapshot ->> 'position_id'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) AND (jsonb_typeof((input_snapshot -> 'output_epoch'::text)) = 'string'::text) AND ((input_snapshot ->> 'output_epoch'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text)) IS TRUE))),
    CONSTRAINT operation_publication_snapshot_check CHECK ((((kind <> ALL (ARRAY['publish_track_v1'::text, 'cleanup_publication_sources_v1'::text, 'retry_publication_sources_v1'::text])) OR ((jsonb_typeof(input_snapshot) = 'object'::text) AND ((input_snapshot -
CASE
    WHEN (kind = 'retry_publication_sources_v1'::text) THEN ARRAY['version'::text, 'position_id'::text, 'output_epoch'::text, 'original_operation_id'::text, 'original_claim_uuid'::text]
    WHEN (kind = 'publish_track_v1'::text) THEN ARRAY['version'::text, 'position_id'::text, 'output_epoch'::text, 'intent_kind'::text]
    ELSE ARRAY['version'::text, 'position_id'::text, 'output_epoch'::text]
END) = '{}'::jsonb) AND (input_snapshot ?&
CASE
    WHEN (kind = 'retry_publication_sources_v1'::text) THEN ARRAY['version'::text, 'position_id'::text, 'output_epoch'::text, 'original_operation_id'::text, 'original_claim_uuid'::text]
    ELSE ARRAY['version'::text, 'position_id'::text, 'output_epoch'::text]
END) AND (jsonb_typeof((input_snapshot -> 'version'::text)) = 'number'::text) AND ((input_snapshot ->> 'version'::text) = '1'::text) AND (jsonb_typeof((input_snapshot -> 'position_id'::text)) = 'string'::text) AND ((input_snapshot ->> 'position_id'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) AND (jsonb_typeof((input_snapshot -> 'output_epoch'::text)) = 'string'::text) AND ((input_snapshot ->> 'output_epoch'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) AND (((kind = 'publish_track_v1'::text) AND (input_snapshot ? 'intent_kind'::text) AND (jsonb_typeof((input_snapshot -> 'intent_kind'::text)) = 'string'::text) AND ((input_snapshot ->> 'intent_kind'::text) = ANY (ARRAY['audio'::text, 'rewrite'::text, 'delete'::text, 'reorganize'::text]))) OR ((kind = 'cleanup_publication_sources_v1'::text) AND (NOT (input_snapshot ? 'intent_kind'::text))) OR ((kind = 'retry_publication_sources_v1'::text) AND (jsonb_typeof((input_snapshot -> 'original_operation_id'::text)) = 'string'::text) AND ((input_snapshot ->> 'original_operation_id'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) AND (jsonb_typeof((input_snapshot -> 'original_claim_uuid'::text)) = 'string'::text) AND ((input_snapshot ->> 'original_claim_uuid'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text))))) IS TRUE)),
    CONSTRAINT operation_source_analysis_mode_valid CHECK (((source_analysis_mode IS NULL) OR (source_analysis_mode = ANY (ARRAY['batch'::text, 'single_step'::text])))),
    CONSTRAINT operation_source_analysis_selectors_kind CHECK (((source_analysis_mode IS NULL) OR (kind = 'analyze_source'::text))),
    CONSTRAINT operation_source_analysis_target_shape CHECK ((((kind = ANY (ARRAY['publish_track_v1'::text, 'cleanup_publication_sources_v1'::text, 'publication_reconcile_v1'::text, 'retry_publication_sources_v1'::text])) AND (source_analysis_mode IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (NOT rerun_target) AND (target_installation_id IS NULL) AND (target_source_root_id IS NULL) AND (target_source_location_id IS NULL) AND (provider_source_id IS NULL) AND (provider_configuration_identity IS NULL) AND (provider_cache_key IS NULL) AND (NOT provider_explicit_refresh) AND ((NOT tools_read_required) OR (kind = ANY (ARRAY['publish_track_v1'::text, 'publication_reconcile_v1'::text])))) OR ((kind <> ALL (ARRAY['publish_track_v1'::text, 'cleanup_publication_sources_v1'::text, 'publication_reconcile_v1'::text, 'retry_publication_sources_v1'::text])) AND ((((source_analysis_mode IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (NOT rerun_target) AND ((NOT tools_read_required) OR (kind = 'scan_source'::text))) OR ((source_analysis_mode = 'batch'::text) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (NOT rerun_target)) OR ((source_analysis_mode = 'single_step'::text) AND ((((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_work_id IS NOT NULL) AND (target_step = ANY (ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text]))) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_work_id IS NULL) AND (target_step IS NULL))) AND ((NOT rerun_target) OR ((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_step = 'fingerprint'::text)) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_work_id IS NULL) AND (target_step IS NULL)))))) IS TRUE)))),
    CONSTRAINT operation_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'running'::text, 'failed'::text, 'succeeded'::text]))),
    CONSTRAINT operation_target_identity CHECK (((kind = ANY (ARRAY['publish_track_v1'::text, 'cleanup_publication_sources_v1'::text, 'publication_reconcile_v1'::text, 'retry_publication_sources_v1'::text, 'obsolete_publication_output_cleanup_v1'::text, 'refresh_genre_catalog_v1'::text, 'delete_library_v1'::text])) OR (kind = ANY (ARRAY['move_tools_root'::text, 'scan_source'::text, 'analyze_source'::text, 'cleanup_source_analysis_artifacts'::text, 'provider_release_lookup'::text, 'provider_recording_lookup'::text, 'provider_release_search'::text, 'provider_recording_search'::text, 'provider_artist_search'::text, 'provider_artist_release_browse'::text, 'provider_acoustid_lookup'::text, 'matching_evaluation_v1'::text, 'matching_apply_v1'::text])) OR (COALESCE((input_snapshot ->> 'target_identity'::text), ''::text) <> ''::text)))
);

CREATE TABLE public.operation_source_work_hold (
    operation_id uuid NOT NULL,
    work_id uuid NOT NULL
);

CREATE TABLE public.operation_tool_read_hold (
    operation_id uuid NOT NULL,
    installation_id uuid NOT NULL
);

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

CREATE TABLE public.position_source_authority (
    position_id uuid NOT NULL,
    mode text NOT NULL,
    revision uuid NOT NULL,
    publication_epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    source_location_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT position_source_authority_mode_check CHECK ((mode = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

CREATE TABLE public.provider (
    id uuid NOT NULL,
    code text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_code_check CHECK ((length(btrim(code)) > 0))
);

CREATE TABLE public.provider_artist (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_key text NOT NULL,
    name text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_artist_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT provider_artist_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

CREATE TABLE public.provider_artist_discography (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    artist_mbid uuid NOT NULL,
    release_count integer NOT NULL,
    completed_at timestamp with time zone NOT NULL,
    CONSTRAINT provider_artist_discography_configuration_identity_check CHECK ((length(btrim(configuration_identity)) > 0)),
    CONSTRAINT provider_artist_discography_release_count_check CHECK ((release_count >= 0))
);

CREATE SEQUENCE public.provider_fetch_order_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE public.provider_local_artist (
    provider_id uuid NOT NULL,
    provider_artist_id uuid NOT NULL,
    local_artist_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_artist_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_artist_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

CREATE TABLE public.provider_local_recording (
    provider_id uuid NOT NULL,
    provider_recording_id uuid NOT NULL,
    local_recording_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_recording_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_recording_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

CREATE TABLE public.provider_local_release (
    provider_id uuid NOT NULL,
    provider_release_id uuid NOT NULL,
    local_release_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_release_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_release_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

CREATE TABLE public.provider_local_track (
    provider_id uuid NOT NULL,
    provider_track_id uuid NOT NULL,
    local_track_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_track_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_track_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

CREATE TABLE public.provider_recording (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_key text NOT NULL,
    title text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_recording_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT provider_recording_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

CREATE TABLE public.provider_release (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_key text NOT NULL,
    title text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_release_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT provider_release_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

CREATE TABLE public.provider_release_artist_credit (
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint DEFAULT 0 NOT NULL,
    fetch_order bigint DEFAULT 0 NOT NULL,
    credit_name text DEFAULT ''::text NOT NULL,
    CONSTRAINT provider_release_artist_credit_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_release_artist_credit_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_release_artist_credit_position_check CHECK (("position" >= 0))
);

CREATE TABLE public.provider_release_track (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    recording_id uuid NOT NULL,
    medium integer,
    "position" integer,
    displayed_number text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint DEFAULT 0 NOT NULL,
    fetch_order bigint DEFAULT 0 NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT provider_release_track_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_release_track_check CHECK ((present = ((medium IS NOT NULL) AND ("position" IS NOT NULL)))),
    CONSTRAINT provider_release_track_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_release_track_medium_check CHECK ((medium >= 0)),
    CONSTRAINT provider_release_track_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT provider_release_track_position_check CHECK (("position" >= 0))
);

CREATE TABLE public.provider_response_cache (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    cache_key text NOT NULL,
    payload jsonb,
    fetched_at timestamp with time zone,
    revision text,
    generation bigint DEFAULT 0 NOT NULL,
    fetch_order bigint DEFAULT 0 NOT NULL,
    CONSTRAINT provider_response_cache_check CHECK (((payload IS NULL) = (fetched_at IS NULL))),
    CONSTRAINT provider_response_cache_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_response_cache_generation_check CHECK ((generation >= 0)),
    CONSTRAINT provider_response_cache_payload_check CHECK (((payload IS NULL) OR (jsonb_typeof(payload) = ANY (ARRAY['object'::text, 'array'::text]))))
);

CREATE TABLE public.provider_source (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    namespace text NOT NULL,
    endpoint text NOT NULL,
    configuration_identity text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_source_namespace_check CHECK ((length(btrim(namespace)) > 0))
);

CREATE TABLE public.provider_source_entity (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    entity_kind text NOT NULL,
    provider_key text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    field_evidence jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    authority smallint NOT NULL,
    complete boolean DEFAULT false NOT NULL,
    fetch_order bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_source_entity_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_source_entity_entity_kind_check CHECK ((entity_kind = ANY (ARRAY['artist'::text, 'release'::text, 'recording'::text]))),
    CONSTRAINT provider_source_entity_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_source_entity_field_evidence_check CHECK ((jsonb_typeof(field_evidence) = 'object'::text)),
    CONSTRAINT provider_source_entity_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text)),
    CONSTRAINT provider_source_entity_provider_key_check CHECK ((length(btrim(provider_key)) > 0)),
    CONSTRAINT provider_source_entity_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

CREATE TABLE public.provider_source_release_artist_credit (
    provider_source_id uuid CONSTRAINT provider_source_release_artist_cred_provider_source_id_not_null NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint NOT NULL,
    fetch_order bigint NOT NULL,
    CONSTRAINT provider_source_release_artist_credit_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_source_release_artist_credit_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_source_release_artist_credit_position_check CHECK (("position" >= 0))
);

CREATE TABLE public.provider_source_release_track (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    track_id uuid NOT NULL,
    release_id uuid NOT NULL,
    recording_id uuid NOT NULL,
    medium integer NOT NULL,
    "position" integer NOT NULL,
    displayed_number text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint NOT NULL,
    fetch_order bigint NOT NULL,
    CONSTRAINT provider_source_release_track_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_source_release_track_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_source_release_track_medium_check CHECK ((medium >= 0)),
    CONSTRAINT provider_source_release_track_position_check CHECK (("position" >= 0))
);

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
    CONSTRAINT publication_attempt_cover_input_mode_check CHECK ((authority_mode = ANY (ARRAY['automatic'::text, 'manual'::text]))),
    CONSTRAINT publication_attempt_cover_input_revision_check CHECK ((authority_revision > 0)),
    CONSTRAINT publication_attempt_cover_input_state_check CHECK ((state = ANY (ARRAY['absent'::text, 'value'::text]))),
    CONSTRAINT publication_attempt_cover_input_tuple_check CHECK (((((state = 'absent'::text) AND (generation IS NULL) AND (image_bytes IS NULL) AND (image_mime IS NULL) AND (image_sha256 IS NULL) AND (image_size IS NULL)) OR ((state = 'value'::text) AND (generation IS NOT NULL) AND (generation <> '00000000-0000-0000-0000-000000000000'::uuid) AND (image_bytes IS NOT NULL) AND (octet_length(image_bytes) > 0) AND (image_mime = ANY (ARRAY['image/png'::text, 'image/jpeg'::text, 'image/gif'::text, 'image/tiff'::text, 'image/webp'::text, 'image/bmp'::text])) AND (image_sha256 IS NOT NULL) AND (octet_length(image_sha256) = 32) AND (image_sha256 = sha256(image_bytes)) AND (image_size = octet_length(image_bytes)) AND (image_size > 0))) IS TRUE))
);

CREATE TABLE public.publication_automatic_album (
    apply_operation_id uuid NOT NULL,
    output_epoch uuid NOT NULL,
    release_id uuid NOT NULL,
    scope jsonb NOT NULL,
    local_layout jsonb,
    receipt_operation_id uuid GENERATED ALWAYS AS (
CASE
    WHEN (local_layout IS NULL) THEN apply_operation_id
    ELSE NULL::uuid
END) STORED,
    local_operation_id uuid GENERATED ALWAYS AS (
CASE
    WHEN (local_layout IS NOT NULL) THEN apply_operation_id
    ELSE NULL::uuid
END) STORED,
    CONSTRAINT publication_automatic_album_local_layout_check CHECK (((local_layout IS NULL) OR (((jsonb_typeof(local_layout) = 'object'::text) AND (jsonb_typeof((local_layout -> 'layout_target'::text)) = 'object'::text) AND ((local_layout -> 'layout_target'::text) ?& ARRAY['schema_version'::text, 'target'::text, 'captures'::text, 'grouping_revision'::text, 'pair_threshold'::text, 'policy_version'::text]) AND (jsonb_typeof((local_layout -> 'catalog_digest'::text)) = 'string'::text) AND ((local_layout ->> 'catalog_digest'::text) ~ '^[0-9a-f]{64}$'::text) AND
CASE
    WHEN (jsonb_typeof((local_layout -> 'sources'::text)) = 'array'::text) THEN ((jsonb_array_length((local_layout -> 'sources'::text)) > 0) AND (NOT jsonb_path_exists(local_layout, '$."sources"[*]?(@.type() != "object")'::jsonpath)))
    ELSE false
END) IS TRUE))),
    CONSTRAINT publication_automatic_album_scope_check CHECK ((((jsonb_typeof(scope) = 'array'::text) AND (jsonb_array_length(scope) > 0)) IS TRUE))
);

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
    CONSTRAINT publication_execution_claim_delivery_token_check CHECK ((delivery_token <> ''::text)),
    CONSTRAINT publication_execution_claim_river_attempt_check CHECK ((river_attempt > 0)),
    CONSTRAINT publication_execution_claim_state_check CHECK ((state = ANY (ARRAY['executing'::text, 'recovery_required'::text, 'quiescent'::text])))
);

CREATE TABLE public.publication_manual_lyrics_receipt (
    position_id uuid NOT NULL,
    epoch uuid NOT NULL,
    desired_revision bigint NOT NULL,
    confirmed_generation uuid NOT NULL,
    published_generation uuid NOT NULL,
    desired_state text NOT NULL,
    desired_sha256 text,
    warning_code text,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT publication_manual_lyrics_receipt_revision_check CHECK ((desired_revision >= 0)),
    CONSTRAINT publication_manual_lyrics_receipt_state_check CHECK ((((desired_state = 'value'::text) AND (desired_sha256 IS NOT NULL) AND (desired_sha256 ~ '^[0-9a-f]{64}$'::text)) OR ((desired_state = 'explicit_empty'::text) AND (desired_sha256 IS NULL)))),
    CONSTRAINT publication_manual_lyrics_receipt_warning_check CHECK (((warning_code IS NULL) OR (warning_code = 'readback_mismatch'::text)))
);

CREATE TABLE public.publication_namespace_claim (
    id uuid NOT NULL,
    epoch uuid NOT NULL,
    key_digest bytea NOT NULL,
    canonical_key text NOT NULL,
    relative_path text NOT NULL,
    node_kind text NOT NULL,
    owner_position_id uuid,
    directory_scope_key text,
    CONSTRAINT publication_namespace_claim_canonical_key_check CHECK ((canonical_key <> ''::text)),
    CONSTRAINT publication_namespace_claim_digest_size_check CHECK ((octet_length(key_digest) = 32)),
    CONSTRAINT publication_namespace_claim_node_kind_check CHECK ((node_kind = ANY (ARRAY['file'::text, 'dir'::text]))),
    CONSTRAINT publication_namespace_claim_owner_scope_check CHECK ((((owner_position_id IS NOT NULL) AND (directory_scope_key IS NULL)) OR ((owner_position_id IS NULL) AND (directory_scope_key IS NOT NULL) AND (directory_scope_key <> ''::text)))),
    CONSTRAINT publication_namespace_claim_relative_path_check CHECK ((relative_path <> ''::text))
);

CREATE TABLE public.publication_namespace_reservation (
    namespace_claim_id uuid NOT NULL,
    position_id uuid NOT NULL,
    generation uuid NOT NULL
);

CREATE TABLE public.publication_namespace_state (
    singleton boolean NOT NULL,
    output_epoch uuid NOT NULL,
    CONSTRAINT publication_namespace_state_singleton_check CHECK (singleton)
);

CREATE TABLE public.publication_reconciliation_dirty_scope (
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    scope text NOT NULL,
    generation bigint NOT NULL,
    CONSTRAINT publication_reconciliation_dirty_scope_generation_check CHECK ((generation > 0)),
    CONSTRAINT publication_reconciliation_dirty_scope_scope_check CHECK ((scope <> ''::text))
);

CREATE TABLE public.publication_reconciliation_event (
    id uuid NOT NULL,
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    observed_generation bigint NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    registered_job_id bigint,
    delivery_epoch bigint DEFAULT 0 NOT NULL,
    highest_delivery_attempt integer DEFAULT 0 CONSTRAINT publication_reconciliation_ev_highest_delivery_attempt_not_null NOT NULL,
    delivery_claimed boolean DEFAULT false NOT NULL,
    claim_token uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    CONSTRAINT publication_reconciliation_delivery_shape CHECK ((((((state = 'pending'::text) AND (registered_job_id IS NULL) AND (NOT delivery_claimed) AND (highest_delivery_attempt = 0) AND (delivery_epoch = 0)) OR ((state = 'dispatched'::text) AND (registered_job_id > 0) AND (delivery_epoch > 0) AND ((NOT delivery_claimed) OR (highest_delivery_attempt > 0)) AND (delivery_claimed = (claim_token IS NOT NULL))) OR ((state = ANY (ARRAY['completed'::text, 'cancelled'::text])) AND (NOT delivery_claimed) AND ((registered_job_id IS NULL) OR (registered_job_id > 0)))) IS TRUE) AND (delivery_claimed = (claim_token IS NOT NULL)))),
    CONSTRAINT publication_reconciliation_event_delivery_epoch_check CHECK ((delivery_epoch >= 0)),
    CONSTRAINT publication_reconciliation_event_highest_delivery_attempt_check CHECK ((highest_delivery_attempt >= 0)),
    CONSTRAINT publication_reconciliation_event_observed_generation_check CHECK ((observed_generation > 0)),
    CONSTRAINT publication_reconciliation_event_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'dispatched'::text, 'completed'::text, 'cancelled'::text])))
);

CREATE TABLE public.publication_reconciliation_request (
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    requested_generation bigint DEFAULT 0 CONSTRAINT publication_reconciliation_reques_requested_generation_not_null NOT NULL,
    reconciled_generation bigint DEFAULT 0 CONSTRAINT publication_reconciliation_reque_reconciled_generation_not_null NOT NULL,
    evaluated_generation bigint,
    parked_reason text,
    retry_not_before timestamp with time zone,
    CONSTRAINT publication_reconciliation_parking_shape CHECK ((((evaluated_generation IS NULL) AND (parked_reason IS NULL) AND (retry_not_before IS NULL)) OR ((evaluated_generation IS NOT NULL) AND (evaluated_generation >= 0) AND ((parked_reason IS NULL) OR (parked_reason <> ''::text))))),
    CONSTRAINT publication_reconciliation_request_check CHECK (((reconciled_generation >= 0) AND (reconciled_generation <= requested_generation))),
    CONSTRAINT publication_reconciliation_request_requested_generation_check CHECK ((requested_generation >= 0))
);

CREATE TABLE public.publication_root_abandonment (
    operation_id uuid NOT NULL,
    root_id uuid NOT NULL,
    epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    candidate_generation uuid NOT NULL,
    namespace_claim_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT publication_root_abandonment_evidence_object CHECK ((jsonb_typeof(evidence) = 'object'::text))
);

CREATE TABLE public.publication_source_cleanup_request (
    originating_operation_id uuid CONSTRAINT publication_source_cleanup_re_originating_operation_id_not_null NOT NULL,
    position_id uuid NOT NULL,
    output_epoch uuid NOT NULL,
    publication_generation uuid CONSTRAINT publication_source_cleanup_requ_publication_generation_not_null NOT NULL,
    requested_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    last_attempted_at timestamp with time zone,
    CONSTRAINT publication_source_cleanup_request_nonzero_witness CHECK (((output_epoch <> '00000000-0000-0000-0000-000000000000'::uuid) AND (publication_generation <> '00000000-0000-0000-0000-000000000000'::uuid)))
);

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
    receipt_track_source_id uuid,
    receipt_track_id uuid,
    receipt_compatible boolean GENERATED ALWAYS AS (
CASE
    WHEN (receipt_track_source_id IS NULL) THEN NULL::boolean
    ELSE true
END) STORED,
    CONSTRAINT publication_source_hold_attempt_check CHECK ((application_attempt > 0)),
    CONSTRAINT publication_source_hold_configured_path_check CHECK ((configured_path <> ''::text)),
    CONSTRAINT publication_source_hold_inventory_path_check CHECK ((inventory_path <> ''::text)),
    CONSTRAINT publication_source_hold_job_check CHECK ((river_job_id > 0)),
    CONSTRAINT publication_source_hold_receipt_pair_check CHECK (((receipt_track_source_id IS NULL) = (receipt_track_id IS NULL))),
    CONSTRAINT publication_source_hold_relative_path_check CHECK ((relative_path <> ''::text)),
    CONSTRAINT publication_source_hold_size_check CHECK ((size_bytes >= 0))
);

CREATE TABLE public.recording_artist (
    recording_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    role text DEFAULT ''::text NOT NULL,
    CONSTRAINT recording_artist_position_check CHECK (("position" >= 0))
);

CREATE TABLE public.release_artist (
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    role text DEFAULT ''::text NOT NULL,
    CONSTRAINT release_artist_position_check CHECK (("position" >= 0))
);

CREATE TABLE public.release_medium (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    release_id uuid NOT NULL,
    medium integer NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    format text DEFAULT ''::text NOT NULL,
    CONSTRAINT release_medium_medium_check CHECK ((medium >= 0))
);

CREATE TABLE public.release_publication_cover (
    release_id uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    mode text DEFAULT 'automatic'::text NOT NULL,
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
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT release_publication_cover_acquisition_mode_check CHECK ((((acquisition_owner IS NULL) OR (mode = 'automatic'::text)) IS TRUE)),
    CONSTRAINT release_publication_cover_acquisition_owner_check CHECK ((((acquisition_owner IS NULL) OR (acquisition_owner <> '00000000-0000-0000-0000-000000000000'::uuid)) IS TRUE)),
    CONSTRAINT release_publication_cover_manual_pending_check CHECK ((((pending_generation IS NULL) OR (mode = 'manual'::text)) IS TRUE)),
    CONSTRAINT release_publication_cover_mode_check CHECK ((mode = ANY (ARRAY['automatic'::text, 'manual'::text]))),
    CONSTRAINT release_publication_cover_pending_tuple_check CHECK (((((pending_generation IS NULL) AND (pending_bytes IS NULL) AND (pending_mime IS NULL) AND (pending_sha256 IS NULL) AND (pending_size IS NULL)) OR ((pending_generation IS NOT NULL) AND (pending_generation <> '00000000-0000-0000-0000-000000000000'::uuid) AND (pending_bytes IS NOT NULL) AND (octet_length(pending_bytes) > 0) AND (pending_mime = ANY (ARRAY['image/png'::text, 'image/jpeg'::text, 'image/gif'::text, 'image/tiff'::text, 'image/webp'::text, 'image/bmp'::text])) AND (pending_sha256 IS NOT NULL) AND (octet_length(pending_sha256) = 32) AND (pending_sha256 = sha256(pending_bytes)) AND (pending_size = octet_length(pending_bytes)) AND (pending_size > 0))) IS TRUE)),
    CONSTRAINT release_publication_cover_revision_check CHECK ((revision > 0)),
    CONSTRAINT release_publication_cover_selected_tuple_check CHECK (((((selected_generation IS NULL) AND (selected_bytes IS NULL) AND (selected_mime IS NULL) AND (selected_sha256 IS NULL) AND (selected_size IS NULL)) OR ((selected_generation IS NOT NULL) AND (selected_generation <> '00000000-0000-0000-0000-000000000000'::uuid) AND (selected_bytes IS NOT NULL) AND (octet_length(selected_bytes) > 0) AND (selected_mime = ANY (ARRAY['image/png'::text, 'image/jpeg'::text, 'image/gif'::text, 'image/tiff'::text, 'image/webp'::text, 'image/bmp'::text])) AND (selected_sha256 IS NOT NULL) AND (octet_length(selected_sha256) = 32) AND (selected_sha256 = sha256(selected_bytes)) AND (selected_size = octet_length(selected_bytes)) AND (selected_size > 0))) IS TRUE)),
    CONSTRAINT release_publication_cover_warning_check CHECK (((warning IS NULL) OR (length(warning) > 0)))
);

CREATE TABLE public.release_track (
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    track_id uuid NOT NULL,
    recording_id uuid,
    medium integer DEFAULT 0 NOT NULL,
    displayed_number text DEFAULT ''::text NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    metadata_revision bigint DEFAULT 1 NOT NULL,
    primary_artist_id uuid,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    duration_ms bigint,
    CONSTRAINT release_track_duration_ms_check CHECK (((duration_ms IS NULL) OR (duration_ms >= 0))),
    CONSTRAINT release_track_medium_check CHECK ((medium >= 0)),
    CONSTRAINT release_track_metadata_object_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT release_track_metadata_revision_check CHECK ((metadata_revision >= 1)),
    CONSTRAINT release_track_position_check CHECK (("position" >= 0))
);

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

CREATE TABLE public.source_analysis_artifact_cleanup_item (
    artifact_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    operation_attempt integer CONSTRAINT source_analysis_artifact_cleanup_ite_operation_attempt_not_null NOT NULL,
    job_id bigint NOT NULL,
    relative_output_path text CONSTRAINT source_analysis_artifact_cleanup__relative_output_path_not_null NOT NULL,
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
    invalidated_at timestamp with time zone,
    CONSTRAINT source_analysis_work_current_location_origin_check CHECK (((current_location_id IS NULL) OR (current_location_id = location_id))),
    CONSTRAINT source_analysis_work_invalidated_detached CHECK (((invalidated_at IS NULL) OR (current_location_id IS NULL))),
    CONSTRAINT source_analysis_work_relative_path_check CHECK ((relative_path <> ''::text)),
    CONSTRAINT source_analysis_work_size_bytes_check CHECK ((size_bytes >= 0))
);

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

CREATE TABLE public.source_cleanup_completion (
    work_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    kind text NOT NULL,
    handled_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT source_cleanup_completion_kind_check CHECK ((kind = ANY (ARRAY['native_removed'::text, 'absent_after_explicit_retry'::text]))),
    CONSTRAINT source_cleanup_completion_size_bytes_check CHECK ((size_bytes >= 0))
);

COMMENT ON TABLE public.source_cleanup_completion IS 'Current root-owned namespace exclusion, retained until an accepted observation of its relative path; snapshot identities do not assert live work.';

CREATE TABLE public.source_cleanup_explicit_retry (
    id uuid NOT NULL,
    recovery_operation_id uuid NOT NULL,
    original_operation_id uuid NOT NULL,
    original_application_attempt integer CONSTRAINT source_cleanup_explicit_ret_original_application_attem_not_null NOT NULL,
    original_river_job_id bigint NOT NULL,
    original_claim_uuid uuid NOT NULL,
    position_id uuid NOT NULL,
    epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    binding jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    consumed_application_attempt integer,
    consumed_river_job_id bigint,
    consumed_claim_uuid uuid,
    frontier_revision uuid,
    CONSTRAINT source_cleanup_explicit_retry_binding_contract CHECK ((public.valid_source_cleanup_explicit_retry_binding(binding, original_operation_id, original_application_attempt, original_river_job_id, original_claim_uuid, position_id, epoch, publication_generation) IS TRUE)),
    CONSTRAINT source_cleanup_explicit_retry_consumption_complete CHECK ((((consumed_application_attempt IS NULL) AND (consumed_river_job_id IS NULL) AND (consumed_claim_uuid IS NULL)) OR ((consumed_application_attempt IS NOT NULL) AND (consumed_river_job_id IS NOT NULL) AND (consumed_claim_uuid IS NOT NULL) AND (consumed_application_attempt > 0) AND (consumed_river_job_id > 0))))
);

CREATE TABLE public.source_cleanup_explicit_retry_item (
    retry_id uuid NOT NULL,
    original_operation_id uuid CONSTRAINT source_cleanup_explicit_retry_it_original_operation_id_not_null NOT NULL,
    item_id uuid NOT NULL,
    owner_uuid uuid NOT NULL
);

CREATE TABLE public.source_cleanup_explicit_retry_resolution (
    retry_id uuid NOT NULL,
    item_id uuid NOT NULL,
    recovery_operation_id uuid CONSTRAINT source_cleanup_explicit_retry_re_recovery_operation_id_not_null NOT NULL,
    application_attempt integer CONSTRAINT source_cleanup_explicit_retry_reso_application_attempt_not_null NOT NULL,
    river_job_id bigint NOT NULL,
    claim_uuid uuid NOT NULL,
    started_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone,
    resolution_kind text,
    CONSTRAINT source_cleanup_explicit_retry_resolut_application_attempt_check CHECK ((application_attempt > 0)),
    CONSTRAINT source_cleanup_explicit_retry_resolution_claim_uuid_check CHECK ((claim_uuid <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT source_cleanup_explicit_retry_resolution_resolution_kind_check CHECK ((resolution_kind = ANY (ARRAY['native_removed'::text, 'absent_after_explicit_retry'::text, 'original_native_completed'::text]))),
    CONSTRAINT source_cleanup_explicit_retry_resolution_river_job_id_check CHECK ((river_job_id > 0)),
    CONSTRAINT source_cleanup_retry_resolution_completion_shape CHECK (((((completed_at IS NULL) AND (resolution_kind IS NULL)) OR ((completed_at IS NOT NULL) AND (completed_at >= started_at) AND (resolution_kind IS NOT NULL))) IS TRUE))
);

CREATE TABLE public.source_cleanup_frontier_transfer_proof (
    transaction_id bigint NOT NULL,
    original_operation_id uuid CONSTRAINT source_cleanup_frontier_transfer_original_operation_id_not_null NOT NULL,
    recovery_operation_id uuid CONSTRAINT source_cleanup_frontier_transfer_recovery_operation_id_not_null NOT NULL,
    proof jsonb NOT NULL,
    CONSTRAINT source_cleanup_frontier_transfer_proof_proof_check CHECK (((jsonb_typeof(proof) = 'object'::text) AND (proof <> '{}'::jsonb)))
);

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
    mutation_started_at timestamp with time zone,
    mutation_completed_at timestamp with time zone,
    safe_error text,
    CONSTRAINT source_cleanup_item_live_borrower_check CHECK (((state <> ALL (ARRAY['queued'::text, 'claimed'::text, 'recovery_required'::text, 'retry_pending'::text])) OR ((source_snapshot IS NOT NULL) AND (jsonb_typeof(source_snapshot) = 'object'::text) AND (borrower_revision IS NOT NULL)))),
    CONSTRAINT source_cleanup_item_mutation_completion_check CHECK (((mutation_completed_at IS NULL) OR ((mutation_started_at IS NOT NULL) AND (mutation_completed_at >= mutation_started_at)))),
    CONSTRAINT source_cleanup_item_mutation_frontier_check CHECK ((((mutation_claim_uuid IS NULL) AND (mutation_started_at IS NULL)) OR ((mutation_claim_uuid IS NOT NULL) AND (mutation_started_at IS NOT NULL)))),
    CONSTRAINT source_cleanup_item_snapshot_object_check CHECK (((source_snapshot IS NULL) OR (jsonb_typeof(source_snapshot) = 'object'::text))),
    CONSTRAINT source_cleanup_item_state_check CHECK ((state = ANY (ARRAY['queued'::text, 'claimed'::text, 'recovery_required'::text, 'retry_pending'::text, 'cancelled'::text, 'stale'::text])))
);

CREATE TABLE public.source_cleanup_owner (
    item_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    application_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    owner_uuid uuid NOT NULL,
    source_root_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    work_id uuid NOT NULL,
    CONSTRAINT source_cleanup_owner_application_attempt_check CHECK ((application_attempt > 0)),
    CONSTRAINT source_cleanup_owner_river_job_id_check CHECK ((river_job_id > 0))
);

CREATE TABLE public.source_cleanup_recovery_item (
    original_operation_id uuid NOT NULL,
    item_id uuid NOT NULL,
    kind text NOT NULL,
    started_at timestamp with time zone,
    completed_at timestamp with time zone,
    CONSTRAINT source_cleanup_recovery_item_kind_check CHECK ((kind = ANY (ARRAY['never_started'::text, 'uncertain'::text, 'native_removed'::text, 'absent_after_explicit_retry'::text, 'original_native_completed'::text]))),
    CONSTRAINT source_cleanup_recovery_item_shape CHECK (((((kind = 'never_started'::text) AND (started_at IS NULL) AND (completed_at IS NULL)) OR ((kind = 'uncertain'::text) AND (started_at IS NOT NULL) AND (completed_at IS NULL)) OR ((kind = ANY (ARRAY['native_removed'::text, 'absent_after_explicit_retry'::text, 'original_native_completed'::text])) AND (started_at IS NOT NULL) AND (completed_at IS NOT NULL) AND (completed_at >= started_at))) IS TRUE))
);

CREATE TABLE public.source_cleanup_recovery_state (
    original_operation_id uuid NOT NULL,
    revision uuid NOT NULL,
    returned_recovery_operation_id uuid CONSTRAINT source_cleanup_recovery_sta_returned_recovery_operatio_not_null NOT NULL,
    returned_application_attempt integer CONSTRAINT source_cleanup_recovery_sta_returned_application_attem_not_null NOT NULL,
    returned_river_job_id bigint NOT NULL,
    returned_river_attempt integer NOT NULL,
    returned_claim_uuid uuid NOT NULL,
    returned_boot_uuid uuid NOT NULL,
    returned_delivery_token text NOT NULL,
    proof jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT source_cleanup_recovery_stat_returned_application_attempt_check CHECK ((returned_application_attempt > 0)),
    CONSTRAINT source_cleanup_recovery_state_proof_check CHECK (((jsonb_typeof(proof) = 'object'::text) AND (proof <> '{}'::jsonb))),
    CONSTRAINT source_cleanup_recovery_state_returned_boot_uuid_check CHECK ((returned_boot_uuid <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT source_cleanup_recovery_state_returned_claim_uuid_check CHECK ((returned_claim_uuid <> '00000000-0000-0000-0000-000000000000'::uuid)),
    CONSTRAINT source_cleanup_recovery_state_returned_delivery_token_check CHECK ((returned_delivery_token <> ''::text)),
    CONSTRAINT source_cleanup_recovery_state_returned_river_attempt_check CHECK ((returned_river_attempt > 0)),
    CONSTRAINT source_cleanup_recovery_state_returned_river_job_id_check CHECK ((returned_river_job_id > 0))
);

CREATE TABLE public.source_cleanup_root_abandonment (
    original_operation_id uuid NOT NULL,
    root_id uuid NOT NULL,
    transaction_id xid8 DEFAULT pg_current_xact_id() NOT NULL,
    confirmed_path text NOT NULL,
    confirmed_location_count bigint CONSTRAINT source_cleanup_root_abandonme_confirmed_location_count_not_null NOT NULL,
    evidence jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT transaction_timestamp() NOT NULL,
    CONSTRAINT source_cleanup_root_abandonment_confirmed_location_count_check CHECK ((confirmed_location_count >= 0)),
    CONSTRAINT source_cleanup_root_abandonment_confirmed_path_check CHECK ((btrim(confirmed_path) <> ''::text)),
    CONSTRAINT source_cleanup_root_abandonment_evidence_check CHECK ((jsonb_typeof(evidence) = 'object'::text))
);

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

CREATE TABLE public.source_work_variant_identity (
    work_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.tool_installation (
    id uuid NOT NULL,
    package_kind text NOT NULL,
    platform_goos text NOT NULL,
    platform_goarch text NOT NULL,
    source_name text NOT NULL,
    release_identity text NOT NULL,
    relative_path text NOT NULL,
    state text NOT NULL,
    executable_verification_output jsonb DEFAULT '{}'::jsonb CONSTRAINT tool_installation_executable_versions_not_null NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    verified_at timestamp with time zone,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    artifact_identities jsonb DEFAULT '{}'::jsonb NOT NULL,
    version text,
    CONSTRAINT tool_installation_package_kind_check CHECK ((package_kind = ANY (ARRAY['ffmpeg'::text, 'fpcalc'::text]))),
    CONSTRAINT tool_installation_ready_is_verified CHECK (((state <> 'ready'::text) OR (verified_at IS NOT NULL))),
    CONSTRAINT tool_installation_ready_version_check CHECK (((state <> 'ready'::text) OR ((version IS NOT NULL) AND (version <> ''::text)))),
    CONSTRAINT tool_installation_relative_path_matches_identity CHECK (((package_kind = ANY (ARRAY['ffmpeg'::text, 'fpcalc'::text])) AND (release_identity <> ''::text) AND (release_identity <> ALL (ARRAY['.'::text, '..'::text])) AND (release_identity !~ '[/\\:]'::text) AND (relative_path = ((package_kind ||
CASE
    WHEN (platform_goos = 'windows'::text) THEN chr(92)
    ELSE '/'::text
END) || release_identity)))),
    CONSTRAINT tool_installation_relative_path_not_empty CHECK ((relative_path <> ''::text)),
    CONSTRAINT tool_installation_release_identity_not_empty CHECK ((release_identity <> ''::text)),
    CONSTRAINT tool_installation_state_check CHECK ((state = ANY (ARRAY['preparing'::text, 'ready'::text, 'failed'::text])))
);

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

CREATE TABLE public.track_artist (
    track_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    role text DEFAULT ''::text NOT NULL,
    CONSTRAINT track_artist_position_check CHECK (("position" >= 0))
);

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
    initial_lyrics jsonb,
    cover_embedding jsonb,
    matching_confidence double precision,
    CONSTRAINT track_publication_actual_assets_object_check CHECK ((jsonb_typeof(actual_assets) = 'object'::text)),
    CONSTRAINT track_publication_actual_audio_object_check CHECK ((jsonb_typeof(actual_audio) = 'object'::text)),
    CONSTRAINT track_publication_actual_metadata_object_check CHECK ((jsonb_typeof(actual_metadata) = 'object'::text)),
    CONSTRAINT track_publication_cover_embedding_projection_check CHECK (((cover_embedding IS NULL) OR (((jsonb_typeof(cover_embedding) = 'object'::text) IS TRUE) AND ((cover_embedding ?& ARRAY['version'::text, 'generation'::text, 'receipt_operation_id'::text, 'receipt_release_id'::text, 'receipt_revision'::text, 'receipt_mode'::text, 'requested_generation'::text, 'requested_sha256'::text, 'outcome'::text, 'warning_code'::text]) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'version'::text)) = 'number'::text) IS TRUE) AND (((cover_embedding ->> 'version'::text) = ANY (ARRAY['1'::text, '2'::text])) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'generation'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'generation'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'receipt_operation_id'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'receipt_operation_id'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'receipt_release_id'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'receipt_release_id'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'receipt_revision'::text)) = 'number'::text) IS TRUE) AND (((cover_embedding ->> 'receipt_revision'::text) ~ '^[1-9][0-9]*$'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'receipt_mode'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'receipt_mode'::text) = ANY (ARRAY['automatic'::text, 'manual'::text])) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'requested_generation'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'requested_generation'::text) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'requested_sha256'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'requested_sha256'::text) ~ '^[0-9a-f]{64}$'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'outcome'::text)) = 'string'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'warning_code'::text)) = 'string'::text) IS TRUE) AND (((((cover_embedding ->> 'version'::text) = '1'::text) AND (NOT (cover_embedding ?| ARRAY['embedded_sha256'::text, 'embedded_mime'::text, 'embedded_size'::text])) AND ((cover_embedding ->> 'outcome'::text) = ANY (ARRAY['verified'::text, 'write_failed'::text, 'read_failed'::text, 'mismatch'::text]))) OR (((cover_embedding ->> 'version'::text) = '2'::text) AND ((((cover_embedding ->> 'outcome'::text) = 'verified'::text) AND ((cover_embedding ?& ARRAY['embedded_sha256'::text, 'embedded_mime'::text, 'embedded_size'::text]) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'embedded_sha256'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'embedded_sha256'::text) ~ '^[0-9a-f]{64}$'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'embedded_mime'::text)) = 'string'::text) IS TRUE) AND (((cover_embedding ->> 'embedded_mime'::text) = 'image/png'::text) IS TRUE) AND ((jsonb_typeof((cover_embedding -> 'embedded_size'::text)) = 'number'::text) IS TRUE) AND (((cover_embedding ->> 'embedded_size'::text) ~ '^[1-9][0-9]*$'::text) IS TRUE)) OR (((cover_embedding ->> 'outcome'::text) <> 'verified'::text) AND (NOT (cover_embedding ?| ARRAY['embedded_sha256'::text, 'embedded_mime'::text, 'embedded_size'::text])) AND ((cover_embedding ->> 'outcome'::text) = ANY (ARRAY['write_failed'::text, 'read_failed'::text, 'mismatch'::text, 'preparation_failed'::text, 'absent_after_interruption'::text])))))) IS TRUE) AND (((((cover_embedding ->> 'outcome'::text) = 'verified'::text) AND ((cover_embedding ->> 'warning_code'::text) = ''::text)) OR (((cover_embedding ->> 'outcome'::text) = 'write_failed'::text) AND ((cover_embedding ->> 'warning_code'::text) = 'cover_write_failed'::text)) OR (((cover_embedding ->> 'outcome'::text) = 'read_failed'::text) AND ((cover_embedding ->> 'warning_code'::text) = 'cover_read_failed'::text)) OR (((cover_embedding ->> 'outcome'::text) = 'mismatch'::text) AND ((cover_embedding ->> 'warning_code'::text) = 'cover_mismatch'::text)) OR (((cover_embedding ->> 'version'::text) = '2'::text) AND ((cover_embedding ->> 'outcome'::text) = 'preparation_failed'::text) AND ((cover_embedding ->> 'warning_code'::text) = ANY (ARRAY['unsupported_source'::text, 'decode_failed'::text, 'derivative_failed'::text, 'source_unavailable'::text]))) OR (((cover_embedding ->> 'version'::text) = '2'::text) AND ((cover_embedding ->> 'outcome'::text) = 'absent_after_interruption'::text) AND ((cover_embedding ->> 'warning_code'::text) = 'cover_absent_after_interruption'::text))) IS TRUE)))),
    CONSTRAINT track_publication_initial_lyrics_object CHECK (((initial_lyrics IS NULL) OR (jsonb_typeof(initial_lyrics) = 'object'::text))),
    CONSTRAINT track_publication_matching_confidence_check CHECK (((matching_confidence IS NULL) OR ((matching_confidence >= (0)::double precision) AND (matching_confidence <= (1)::double precision)))),
    CONSTRAINT track_publication_ownership_object_check CHECK ((jsonb_typeof(ownership) = 'object'::text)),
    CONSTRAINT track_publication_quality_policy_version_check CHECK ((quality_policy_version > 0)),
    CONSTRAINT track_publication_relative_path_check CHECK ((relative_path <> ''::text))
);

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
    resume_mode text DEFAULT 'prepare'::text NOT NULL,
    matching_confidence double precision,
    CONSTRAINT track_publication_attempt_frozen_inputs_phase_check CHECK (((frozen_inputs IS NOT NULL) AND (jsonb_typeof(frozen_inputs) = 'object'::text))),
    CONSTRAINT track_publication_attempt_intent_kind_check CHECK ((intent_kind = ANY (ARRAY['audio'::text, 'rewrite'::text, 'delete'::text, 'reorganize'::text]))),
    CONSTRAINT track_publication_attempt_matching_confidence_check CHECK (((matching_confidence IS NULL) OR ((matching_confidence >= (0)::double precision) AND (matching_confidence <= (1)::double precision)))),
    CONSTRAINT track_publication_attempt_namespace_manifest_object_check CHECK ((jsonb_typeof(namespace_manifest) = 'object'::text)),
    CONSTRAINT track_publication_attempt_phase_check CHECK ((phase = ANY (ARRAY['queued'::text, 'preparing'::text, 'staged'::text, 'validated'::text, 'exposing'::text]))),
    CONSTRAINT track_publication_attempt_resume_mode_check CHECK ((resume_mode = ANY (ARRAY['prepare'::text, 'inspect_source_republish_target'::text])))
);

CREATE TABLE public.track_source (
    id uuid NOT NULL,
    track_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    selected boolean DEFAULT false NOT NULL,
    compatible boolean DEFAULT true NOT NULL,
    decision_method text NOT NULL,
    policy_version text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    source_metadata_result_id uuid,
    CONSTRAINT track_source_check CHECK ((((decision_method = 'manual'::text) AND (policy_version IS NULL)) OR ((decision_method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT track_source_decision_method_check CHECK ((decision_method = ANY (ARRAY['manual'::text, 'automatic'::text]))),
    CONSTRAINT track_source_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text))
);

-- Defaults and table constraints
ALTER TABLE ONLY public.matching_delivery ALTER COLUMN id SET DEFAULT nextval('public.matching_delivery_id_seq'::regclass);

ALTER TABLE ONLY public.app_setting
    ADD CONSTRAINT app_setting_pkey PRIMARY KEY (setting_name);

ALTER TABLE ONLY public.collection_artist
    ADD CONSTRAINT collection_artist_pkey PRIMARY KEY (collection_id, "position");

ALTER TABLE ONLY public.collection
    ADD CONSTRAINT collection_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.collection_release
    ADD CONSTRAINT collection_release_pkey PRIMARY KEY (collection_id, "position");

ALTER TABLE ONLY public.cover_art_archive_image
    ADD CONSTRAINT cover_art_archive_image_pkey PRIMARY KEY (release_mbid, image_id);

ALTER TABLE ONLY public.cover_art_archive_release
    ADD CONSTRAINT cover_art_archive_release_pkey PRIMARY KEY (release_mbid);

ALTER TABLE ONLY public.genre_catalog
    ADD CONSTRAINT genre_catalog_pkey PRIMARY KEY (singleton);

ALTER TABLE ONLY public.incoming_group_matching_authority
    ADD CONSTRAINT incoming_group_matching_authority_pkey PRIMARY KEY (group_id);

ALTER TABLE ONLY public.incoming_group_member_location
    ADD CONSTRAINT incoming_group_member_location_pkey PRIMARY KEY (group_id, variant_id, source_root_id, relative_path);

ALTER TABLE ONLY public.incoming_group_member
    ADD CONSTRAINT incoming_group_member_pkey PRIMARY KEY (group_id, variant_id);

ALTER TABLE ONLY public.incoming_group_member
    ADD CONSTRAINT incoming_group_member_variant_id_key UNIQUE (variant_id);

ALTER TABLE ONLY public.incoming_group
    ADD CONSTRAINT incoming_group_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.incoming_group_return_automatic_receipt
    ADD CONSTRAINT incoming_group_return_automatic_receipt_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.incoming_group_unlink_receipt
    ADD CONSTRAINT incoming_group_unlink_receipt_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.incoming_grouping_state
    ADD CONSTRAINT incoming_grouping_state_pkey PRIMARY KEY (singleton);

ALTER TABLE ONLY public.incoming_match_evaluation_location
    ADD CONSTRAINT incoming_match_evaluation_location_pkey PRIMARY KEY (group_id, file_member_id, source_root_id, relative_path);

ALTER TABLE ONLY public.incoming_match_evaluation_member
    ADD CONSTRAINT incoming_match_evaluation_member_pkey PRIMARY KEY (group_id, file_member_id);

ALTER TABLE ONLY public.incoming_match_evaluation
    ADD CONSTRAINT incoming_match_evaluation_pkey PRIMARY KEY (group_id);

ALTER TABLE ONLY public.incoming_matching_automatic_admission
    ADD CONSTRAINT incoming_matching_automatic_admission_pkey PRIMARY KEY (fingerprint);

ALTER TABLE ONLY public.incoming_matching_generation
    ADD CONSTRAINT incoming_matching_generation_pkey PRIMARY KEY (group_id);

ALTER TABLE ONLY public.incoming_matching_reconcile_event
    ADD CONSTRAINT incoming_matching_reconcile_event_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.library_artist_photo
    ADD CONSTRAINT library_artist_photo_pkey PRIMARY KEY (config_identity, artist_mbid);

ALTER TABLE ONLY public.local_artist
    ADD CONSTRAINT local_artist_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.local_matching_receipt
    ADD CONSTRAINT local_matching_receipt_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.local_recording
    ADD CONSTRAINT local_recording_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.local_release
    ADD CONSTRAINT local_release_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.local_track
    ADD CONSTRAINT local_track_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_group_id_request_id_key UNIQUE (group_id, request_id);

ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_lookup_operation_id_key UNIQUE (lookup_operation_id);

ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_run_id_key UNIQUE (run_id);

ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_lookup_operation_id_key UNIQUE (lookup_operation_id);

ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.manual_matching_receipt
    ADD CONSTRAINT manual_matching_receipt_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_operation_id_key UNIQUE (operation_id);

ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.manual_picker_search_result
    ADD CONSTRAINT manual_picker_search_result_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.matching_acquisition_phase
    ADD CONSTRAINT matching_acquisition_phase_pkey PRIMARY KEY (run_id, revision);

ALTER TABLE ONLY public.matching_acquisition
    ADD CONSTRAINT matching_acquisition_pkey PRIMARY KEY (run_id);

ALTER TABLE ONLY public.matching_acquisition_seal
    ADD CONSTRAINT matching_acquisition_seal_pkey PRIMARY KEY (run_id);

ALTER TABLE ONLY public.matching_acquisition_seal
    ADD CONSTRAINT matching_acquisition_seal_river_job_id_key UNIQUE (river_job_id);

ALTER TABLE ONLY public.matching_apply_receipt
    ADD CONSTRAINT matching_apply_receipt_pkey PRIMARY KEY (apply_operation_id);

ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_apply_operation_id_key UNIQUE (apply_operation_id);

ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_group_id_evaluation_revision_key UNIQUE (group_id, evaluation_revision);

ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_operation_input_unique UNIQUE (apply_operation_id, input_sha256);

ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_river_job_id_key UNIQUE (river_job_id);

ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_run_id_river_job_id_key UNIQUE (run_id, river_job_id);

ALTER TABLE ONLY public.matching_fallback_authority_seal
    ADD CONSTRAINT matching_fallback_authority_seal_pkey PRIMARY KEY (run_id, wait_revision);

ALTER TABLE ONLY public.matching_fallback_authority_seal
    ADD CONSTRAINT matching_fallback_authority_seal_run_wait_digest_key UNIQUE (run_id, wait_revision, authority_digest);

ALTER TABLE public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_layout_decision_check CHECK ((((schema_version = 1) IS TRUE) AND ((jsonb_typeof(layout_decision) = 'object'::text) IS TRUE) AND (((layout_decision ->> 'schema_version'::text) = '1'::text) IS TRUE) AND (((layout_decision ->> 'policy_version'::text) = 'matching-layout-v1'::text) IS TRUE) AND ((jsonb_typeof((layout_decision -> 'pair_threshold'::text)) = 'string'::text) IS TRUE) AND (((layout_decision ->> 'pair_threshold'::text) ~ '^(0|1|0\.[0-9]*[1-9])$'::text) IS TRUE) AND (((layout_decision ->> 'status'::text) = ANY (ARRAY['selected'::text, 'none'::text, 'indeterminate'::text])) IS TRUE) AND ((octet_length(decision_digest) = 32) IS TRUE) AND (((((((layout_decision ->> 'status'::text) = 'selected'::text) IS TRUE) AND ((jsonb_typeof((layout_decision -> 'provider_release_id'::text)) = 'string'::text) IS TRUE) AND (((layout_decision ->> 'provider_release_id'::text) <> ''::text) IS TRUE) AND ((jsonb_typeof((layout_decision -> 'assignments'::text)) = 'array'::text) IS TRUE) AND ((
CASE
    WHEN (jsonb_typeof((layout_decision -> 'assignments'::text)) = 'array'::text) THEN jsonb_array_length((layout_decision -> 'assignments'::text))
    ELSE 0
END > 0) IS TRUE)) IS TRUE) OR (((((layout_decision ->> 'status'::text) = ANY (ARRAY['none'::text, 'indeterminate'::text])) IS TRUE) AND (((layout_decision -> 'provider_release_id'::text) IS NULL) IS TRUE) AND (((layout_decision -> 'assignments'::text) IS NULL) IS TRUE)) IS TRUE)) IS TRUE)));

ALTER TABLE ONLY public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_pkey PRIMARY KEY (run_id, acquisition_revision);

ALTER TABLE ONLY public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_run_id_stage_index_key UNIQUE (run_id, stage_index);

ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_pkey PRIMARY KEY (run_id, wait_revision);

ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_river_job_id_key UNIQUE (river_job_id);

ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_pkey PRIMARY KEY (run_id, request_id);

ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_request_id_key UNIQUE (request_id);

ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_pkey PRIMARY KEY (run_id, stage_index, role, request_id);

ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_request_id_key UNIQUE (request_id);

ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_pkey PRIMARY KEY (run_id, request_id);

ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_request_id_key UNIQUE (request_id);

ALTER TABLE ONLY public.matching_local_recheck
    ADD CONSTRAINT matching_local_recheck_local_apply_operation_key UNIQUE (local_apply_operation_id);

ALTER TABLE ONLY public.matching_local_recheck
    ADD CONSTRAINT matching_local_recheck_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.matching_local_recheck_publication
    ADD CONSTRAINT matching_local_recheck_publication_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.matching_local_recheck_source
    ADD CONSTRAINT matching_local_recheck_source_pkey PRIMARY KEY (recheck_id, work_id);

ALTER TABLE ONLY public.matching_local_recheck_source
    ADD CONSTRAINT matching_local_recheck_source_source_location_id_key UNIQUE (source_location_id);

ALTER TABLE ONLY public.matching_local_recheck_source
    ADD CONSTRAINT matching_local_recheck_source_work_id_key UNIQUE (work_id);

ALTER TABLE ONLY public.matching_provider_dependency
    ADD CONSTRAINT matching_provider_dependency_pkey PRIMARY KEY (parent_run_id, request_id);

ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_operation_id_key UNIQUE (operation_id);

ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_plan_admission_key_key UNIQUE (plan_admission_key);

ALTER TABLE ONLY public.matching_run_retirement_authorization
    ADD CONSTRAINT matching_run_retirement_authorization_pkey PRIMARY KEY (run_id);

ALTER TABLE ONLY public.matching_run_target
    ADD CONSTRAINT matching_run_target_group_id_target_generation_key UNIQUE (group_id, target_generation);

ALTER TABLE ONLY public.matching_run_target
    ADD CONSTRAINT matching_run_target_pkey PRIMARY KEY (run_id, group_id);

ALTER TABLE ONLY public.media_fingerprint_result
    ADD CONSTRAINT media_fingerprint_result_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.media_fingerprint_result
    ADD CONSTRAINT media_fingerprint_result_source_sha256_unique UNIQUE (source_sha256);

ALTER TABLE ONLY public.media_metadata_result
    ADD CONSTRAINT media_metadata_result_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.media_probe_cache
    ADD CONSTRAINT media_probe_cache_pkey PRIMARY KEY (source_sha256, ffprobe_version_sha256, analysis_policy_version);

ALTER TABLE ONLY public.media_variant
    ADD CONSTRAINT media_variant_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.media_variant
    ADD CONSTRAINT media_variant_source_sha256_unique UNIQUE (source_sha256);

ALTER TABLE ONLY public.obsolete_publication_output_cleanup
    ADD CONSTRAINT obsolete_publication_output_cleanup_claim_unique UNIQUE (namespace_claim_id);

ALTER TABLE ONLY public.obsolete_publication_output_cleanup
    ADD CONSTRAINT obsolete_publication_output_cleanup_pkey PRIMARY KEY (operation_id);

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

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_id_attempt_job_unique UNIQUE (id, attempt, river_job_id);

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_river_job_id_key UNIQUE (river_job_id);

ALTER TABLE ONLY public.operation_source_work_hold
    ADD CONSTRAINT operation_source_work_hold_pkey PRIMARY KEY (operation_id, work_id);

ALTER TABLE ONLY public.operation_tool_read_hold
    ADD CONSTRAINT operation_tool_read_hold_pkey PRIMARY KEY (operation_id, installation_id);

ALTER TABLE ONLY public.output_reset_journal
    ADD CONSTRAINT output_reset_journal_pkey PRIMARY KEY (token);

ALTER TABLE ONLY public.position_source_authority
    ADD CONSTRAINT position_source_authority_pkey PRIMARY KEY (position_id);

ALTER TABLE ONLY public.provider_artist_discography
    ADD CONSTRAINT provider_artist_discography_pkey PRIMARY KEY (provider_source_id, configuration_identity, artist_mbid);

ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_provider_id_id_key UNIQUE (provider_id, id);

ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_provider_id_provider_key_key UNIQUE (provider_id, provider_key);

ALTER TABLE ONLY public.provider
    ADD CONSTRAINT provider_code_key UNIQUE (code);

ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_pkey PRIMARY KEY (provider_id, provider_artist_id);

ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_provider_id_local_artist_id_key UNIQUE (provider_id, local_artist_id);

ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_pkey PRIMARY KEY (provider_id, provider_recording_id);

ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_provider_id_local_recording_id_key UNIQUE (provider_id, local_recording_id);

ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_pkey PRIMARY KEY (provider_id, provider_release_id);

ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_provider_id_local_release_id_key UNIQUE (provider_id, local_release_id);

ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_pkey PRIMARY KEY (provider_id, provider_track_id);

ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_provider_id_local_track_id_key UNIQUE (provider_id, local_track_id);

ALTER TABLE ONLY public.provider
    ADD CONSTRAINT provider_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_provider_id_id_key UNIQUE (provider_id, id);

ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_provider_id_provider_key_key UNIQUE (provider_id, provider_key);

ALTER TABLE ONLY public.provider_release_artist_credit
    ADD CONSTRAINT provider_release_artist_credit_pkey PRIMARY KEY (provider_id, release_id, "position");

ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_provider_id_id_key UNIQUE (provider_id, id);

ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_provider_id_provider_key_key UNIQUE (provider_id, provider_key);

ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_match_target UNIQUE (provider_id, release_id, id);

ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_provider_id_id_key UNIQUE (provider_id, id);

ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_provider_id_release_id_medium_positi_key UNIQUE (provider_id, release_id, medium, "position") DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.provider_response_cache
    ADD CONSTRAINT provider_response_cache_pkey PRIMARY KEY (provider_source_id, cache_key);

ALTER TABLE ONLY public.provider_source_entity
    ADD CONSTRAINT provider_source_entity_pkey PRIMARY KEY (provider_source_id, entity_kind, provider_key);

ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_provider_id_id_key UNIQUE (provider_id, id);

ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_provider_id_namespace_key UNIQUE (provider_id, namespace);

ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artist_credit_pkey PRIMARY KEY (provider_source_id, release_id, "position");

ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_pkey PRIMARY KEY (provider_source_id, track_id);

ALTER TABLE ONLY public.publication_attempt_cover_input
    ADD CONSTRAINT publication_attempt_cover_input_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.publication_automatic_album
    ADD CONSTRAINT publication_automatic_album_pkey PRIMARY KEY (apply_operation_id);

ALTER TABLE ONLY public.publication_execution_claim
    ADD CONSTRAINT publication_execution_claim_claim_uuid_key UNIQUE (claim_uuid);

ALTER TABLE ONLY public.publication_execution_claim
    ADD CONSTRAINT publication_execution_claim_delivery_identity_key UNIQUE (operation_id, application_attempt, river_job_id, claim_uuid);

ALTER TABLE ONLY public.publication_execution_claim
    ADD CONSTRAINT publication_execution_claim_operation_claim_key UNIQUE (operation_id, claim_uuid);

ALTER TABLE ONLY public.publication_execution_claim
    ADD CONSTRAINT publication_execution_claim_pkey PRIMARY KEY (operation_id, application_attempt);

ALTER TABLE ONLY public.publication_manual_lyrics_receipt
    ADD CONSTRAINT publication_manual_lyrics_receipt_pkey PRIMARY KEY (position_id);

ALTER TABLE ONLY public.publication_namespace_claim
    ADD CONSTRAINT publication_namespace_claim_cleanup_identity_unique UNIQUE (id, epoch, owner_position_id, relative_path, node_kind);

ALTER TABLE ONLY public.publication_namespace_claim
    ADD CONSTRAINT publication_namespace_claim_key_unique UNIQUE (epoch, key_digest);

ALTER TABLE ONLY public.publication_namespace_claim
    ADD CONSTRAINT publication_namespace_claim_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.publication_namespace_reservation
    ADD CONSTRAINT publication_namespace_reservation_pkey PRIMARY KEY (namespace_claim_id, position_id, generation);

ALTER TABLE ONLY public.publication_namespace_state
    ADD CONSTRAINT publication_namespace_state_output_epoch_key UNIQUE (output_epoch);

ALTER TABLE ONLY public.publication_namespace_state
    ADD CONSTRAINT publication_namespace_state_pkey PRIMARY KEY (singleton);

ALTER TABLE ONLY public.publication_reconciliation_dirty_scope
    ADD CONSTRAINT publication_reconciliation_dirty_scope_pkey PRIMARY KEY (output_epoch, position_id, scope);

ALTER TABLE ONLY public.publication_reconciliation_event
    ADD CONSTRAINT publication_reconciliation_event_operation_id_key UNIQUE (operation_id);

ALTER TABLE ONLY public.publication_reconciliation_event
    ADD CONSTRAINT publication_reconciliation_event_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.publication_reconciliation_request
    ADD CONSTRAINT publication_reconciliation_request_pkey PRIMARY KEY (output_epoch, position_id);

ALTER TABLE ONLY public.publication_root_abandonment
    ADD CONSTRAINT publication_root_abandonment_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.publication_source_cleanup_request
    ADD CONSTRAINT publication_source_cleanup_request_pkey PRIMARY KEY (originating_operation_id);

ALTER TABLE ONLY public.publication_source_cleanup_request
    ADD CONSTRAINT publication_source_cleanup_request_position_epoch_key UNIQUE (position_id, output_epoch);

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_uuid_key UNIQUE (hold_uuid);

ALTER TABLE ONLY public.recording_artist
    ADD CONSTRAINT recording_artist_pkey PRIMARY KEY (recording_id, "position");

ALTER TABLE ONLY public.release_artist
    ADD CONSTRAINT release_artist_pkey PRIMARY KEY (release_id, "position");

ALTER TABLE ONLY public.release_medium
    ADD CONSTRAINT release_medium_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.release_medium
    ADD CONSTRAINT release_medium_release_id_medium_key UNIQUE (release_id, medium);

ALTER TABLE ONLY public.release_publication_cover
    ADD CONSTRAINT release_publication_cover_pkey PRIMARY KEY (release_id);

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_id_key UNIQUE (id);

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_id_release_key UNIQUE (id, release_id);

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_pkey PRIMARY KEY (release_id, medium, "position");

ALTER TABLE ONLY public.source_analysis_artifact_cleanup_item
    ADD CONSTRAINT source_analysis_artifact_cleanup_item_pkey PRIMARY KEY (artifact_id, operation_id, operation_attempt, job_id);

ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_relative_output_path_key UNIQUE (relative_output_path);

ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_work_id_id_key UNIQUE (work_id, id);

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_pkey PRIMARY KEY (work_id, step);

ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_artifact_id_key UNIQUE (artifact_id);

ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_pkey PRIMARY KEY (work_id);

ALTER TABLE ONLY public.source_analysis_work_execution
    ADD CONSTRAINT source_analysis_work_execution_pkey PRIMARY KEY (work_id, operation_id, operation_attempt, job_id);

ALTER TABLE ONLY public.source_analysis_work
    ADD CONSTRAINT source_analysis_work_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.source_cleanup_completion
    ADD CONSTRAINT source_cleanup_completion_pkey PRIMARY KEY (work_id);

ALTER TABLE ONLY public.source_cleanup_explicit_retry_item
    ADD CONSTRAINT source_cleanup_explicit_retry_item_pkey PRIMARY KEY (retry_id, item_id);

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_one_outstanding UNIQUE (original_operation_id, original_claim_uuid);

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_recovery_operation_id_key UNIQUE (recovery_operation_id);

ALTER TABLE ONLY public.source_cleanup_explicit_retry_resolution
    ADD CONSTRAINT source_cleanup_explicit_retry_resolution_pkey PRIMARY KEY (retry_id, item_id);

ALTER TABLE ONLY public.source_cleanup_frontier_transfer_proof
    ADD CONSTRAINT source_cleanup_frontier_transfer_proof_pkey PRIMARY KEY (transaction_id, original_operation_id, recovery_operation_id);

ALTER TABLE ONLY public.source_cleanup_item
    ADD CONSTRAINT source_cleanup_item_id_operation_key UNIQUE (id, operation_id);

ALTER TABLE ONLY public.source_cleanup_item
    ADD CONSTRAINT source_cleanup_item_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.source_cleanup_owner
    ADD CONSTRAINT source_cleanup_owner_item_owner_key UNIQUE (item_id, owner_uuid);

ALTER TABLE ONLY public.source_cleanup_owner
    ADD CONSTRAINT source_cleanup_owner_owner_uuid_key UNIQUE (owner_uuid);

ALTER TABLE ONLY public.source_cleanup_owner
    ADD CONSTRAINT source_cleanup_owner_pkey PRIMARY KEY (item_id);

ALTER TABLE ONLY public.source_cleanup_owner
    ADD CONSTRAINT source_cleanup_owner_source_location_id_key UNIQUE (source_location_id);

ALTER TABLE ONLY public.source_cleanup_owner
    ADD CONSTRAINT source_cleanup_owner_work_id_key UNIQUE (work_id);

ALTER TABLE ONLY public.source_cleanup_recovery_item
    ADD CONSTRAINT source_cleanup_recovery_item_pkey PRIMARY KEY (original_operation_id, item_id);

ALTER TABLE ONLY public.source_cleanup_recovery_state
    ADD CONSTRAINT source_cleanup_recovery_state_original_operation_id_revisio_key UNIQUE (original_operation_id, revision);

ALTER TABLE ONLY public.source_cleanup_recovery_state
    ADD CONSTRAINT source_cleanup_recovery_state_pkey PRIMARY KEY (original_operation_id);

ALTER TABLE ONLY public.source_cleanup_root_abandonment
    ADD CONSTRAINT source_cleanup_root_abandonment_pkey PRIMARY KEY (original_operation_id);

ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_id_root_unique UNIQUE (id, source_root_id);

ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_root_path_unique UNIQUE (source_root_id, relative_path);

ALTER TABLE ONLY public.source_root
    ADD CONSTRAINT source_root_configured_path_unique UNIQUE (configured_path);

ALTER TABLE ONLY public.source_root
    ADD CONSTRAINT source_root_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.source_scan_candidate
    ADD CONSTRAINT source_scan_candidate_operation_path_unique UNIQUE (operation_id, relative_path);

ALTER TABLE ONLY public.source_scan_candidate
    ADD CONSTRAINT source_scan_candidate_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.source_work_variant_identity
    ADD CONSTRAINT source_work_variant_identity_pkey PRIMARY KEY (work_id);

ALTER TABLE ONLY public.tool_installation
    ADD CONSTRAINT tool_installation_identity_unique UNIQUE (package_kind, source_name, release_identity, platform_goos, platform_goarch);

ALTER TABLE ONLY public.tool_installation
    ADD CONSTRAINT tool_installation_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.tools_execution_claim
    ADD CONSTRAINT tools_execution_claim_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.track_artist
    ADD CONSTRAINT track_artist_pkey PRIMARY KEY (track_id, "position");

ALTER TABLE ONLY public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_operation_epoch_unique UNIQUE (operation_id, epoch);

ALTER TABLE ONLY public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_operation_position_key UNIQUE (operation_id, position_id);

ALTER TABLE ONLY public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_position_key UNIQUE (position_id);

ALTER TABLE ONLY public.track_publication
    ADD CONSTRAINT track_publication_pkey PRIMARY KEY (position_id);

ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_receipt_pin_key UNIQUE (id, track_id, media_variant_id, compatible);

ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_track_id_media_variant_id_key UNIQUE (track_id, media_variant_id);

-- Indexes
CREATE INDEX incoming_group_member_location_variant_idx ON public.incoming_group_member_location USING btree (variant_id);

CREATE INDEX incoming_group_return_automatic_receipt_group_idx ON public.incoming_group_return_automatic_receipt USING btree (group_id, created_at);

CREATE UNIQUE INDEX incoming_matching_reconcile_current_pending_idx ON public.incoming_matching_reconcile_event USING btree (reason) WHERE ((reason = 'current'::text) AND (state = 'pending'::text));

CREATE INDEX incoming_matching_reconcile_pending_idx ON public.incoming_matching_reconcile_event USING btree (created_at, id) WHERE (state = 'pending'::text);

CREATE UNIQUE INDEX incoming_matching_reconcile_source_unique ON public.incoming_matching_reconcile_event USING btree (source_operation_id, source_attempt, reason) WHERE (reason = ANY (ARRAY['analysis_success'::text, 'scan_success'::text]));

CREATE UNIQUE INDEX incoming_matching_reconcile_startup_open_unique ON public.incoming_matching_reconcile_event USING btree (reason) WHERE ((reason = 'startup'::text) AND (state = ANY (ARRAY['pending'::text, 'dispatched'::text])));

CREATE INDEX local_matching_receipt_group_idx ON public.local_matching_receipt USING btree (group_id);

CREATE INDEX manual_matching_admission_group_created_idx ON public.manual_matching_admission USING btree (group_id, created_at DESC);

CREATE INDEX manual_matching_lookup_seal_operation_idx ON public.manual_matching_lookup_seal USING btree (lookup_operation_id);

CREATE INDEX manual_picker_search_group_idx ON public.manual_picker_search USING btree (group_id, operation_id);

CREATE UNIQUE INDEX matching_fallback_request_registration_lookup_key ON public.matching_fallback_request_registration USING btree (run_id, stage_index, cache_key) WHERE (role = 'lookup'::text);

CREATE UNIQUE INDEX matching_fallback_request_registration_one_search ON public.matching_fallback_request_registration USING btree (run_id, stage_index) WHERE (role = 'search'::text);

CREATE INDEX matching_local_recheck_due_idx ON public.matching_local_recheck USING btree (state, next_recheck_at);

CREATE INDEX matching_local_recheck_publication_destination_position_idx ON public.matching_local_recheck_publication USING btree (position_id, output_epoch);

CREATE UNIQUE INDEX matching_local_recheck_publication_live_predecessor_claim_key ON public.matching_local_recheck_publication USING btree (predecessor_claim_id) WHERE ((committed_at IS NULL) OR (NOT same_path));

CREATE INDEX matching_local_recheck_publication_live_predecessor_position_id ON public.matching_local_recheck_publication USING btree (predecessor_position_id, output_epoch) WHERE ((committed_at IS NULL) OR (NOT same_path));

CREATE INDEX matching_local_recheck_publication_recheck_idx ON public.matching_local_recheck_publication USING btree (recheck_id);

CREATE INDEX matching_local_recheck_source_root_idx ON public.matching_local_recheck_source USING btree (source_root_id);

CREATE INDEX matching_local_recheck_source_unlink_target_idx ON public.matching_local_recheck_source USING hash (unlink_target);

CREATE INDEX matching_provider_dependency_pending_idx ON public.matching_provider_dependency USING btree (request_id) WHERE (terminal_outcome IS NULL);

CREATE INDEX matching_run_active_idx ON public.matching_run USING btree (state) WHERE (state = ANY (ARRAY['queued'::text, 'running'::text, 'waiting_provider'::text, 'waiting_policy'::text]));

CREATE INDEX matching_run_operation_id_idx ON public.matching_run USING btree (operation_id);

CREATE UNIQUE INDEX media_metadata_result_current_sha256_idx ON public.media_metadata_result USING btree (source_sha256) WHERE (source_sha256 IS NOT NULL);

CREATE INDEX obsolete_publication_output_cleanup_state_idx ON public.obsolete_publication_output_cleanup USING btree (state);

CREATE INDEX operation_active_source_analysis_work_idx ON public.operation USING btree (target_work_id) WHERE ((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_work_id IS NOT NULL));

CREATE INDEX operation_active_source_root_idx ON public.operation USING btree (target_source_root_id) WHERE ((kind = 'scan_source'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

CREATE INDEX operation_active_updated_at_idx ON public.operation USING btree (updated_at) WHERE (state = ANY (ARRAY['queued'::text, 'running'::text, 'failed'::text]));

CREATE UNIQUE INDEX operation_one_active_installation ON public.operation USING btree (target_installation_id) WHERE ((kind <> 'move_tools_root'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

CREATE UNIQUE INDEX operation_one_active_source_root_scan ON public.operation USING btree (target_source_root_id) WHERE ((kind = 'scan_source'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

CREATE UNIQUE INDEX operation_one_active_tools_root_move ON public.operation USING btree (kind) WHERE ((kind = 'move_tools_root'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

CREATE INDEX operation_scan_source_updated_at_idx ON public.operation USING btree (updated_at) WHERE ((kind = 'scan_source'::text) AND (state = ANY (ARRAY['queued'::text, 'running'::text])));

CREATE UNIQUE INDEX operation_source_work_hold_one_active_work ON public.operation_source_work_hold USING btree (work_id);

CREATE INDEX operation_target_installation_active_idx ON public.operation USING btree (target_installation_id) WHERE (state = ANY (ARRAY['queued'::text, 'running'::text]));

CREATE INDEX operation_target_source_root_idx ON public.operation USING btree (target_source_root_id) WHERE (target_source_root_id IS NOT NULL);

CREATE UNIQUE INDEX output_reset_one_unresolved_idx ON public.output_reset_journal USING btree ((true)) WHERE (state <> 'finished'::text);

CREATE INDEX provider_source_entity_provider_key_idx ON public.provider_source_entity USING btree (provider_id, entity_kind, provider_key);

CREATE UNIQUE INDEX provider_source_release_track_position_idx ON public.provider_source_release_track USING btree (provider_source_id, release_id, medium, "position") WHERE present;

CREATE INDEX publication_automatic_album_local_operation_idx ON public.publication_automatic_album USING btree (local_operation_id) WHERE (local_operation_id IS NOT NULL);

CREATE INDEX publication_automatic_album_receipt_operation_idx ON public.publication_automatic_album USING btree (receipt_operation_id) WHERE (receipt_operation_id IS NOT NULL);

CREATE INDEX publication_execution_claim_state_idx ON public.publication_execution_claim USING btree (state);

CREATE INDEX publication_namespace_claim_owner_idx ON public.publication_namespace_claim USING btree (owner_position_id);

CREATE INDEX publication_namespace_reservation_position_idx ON public.publication_namespace_reservation USING btree (position_id);

CREATE UNIQUE INDEX publication_reconciliation_one_dispatched ON public.publication_reconciliation_event USING btree (output_epoch, position_id) WHERE (state = 'dispatched'::text);

CREATE UNIQUE INDEX publication_reconciliation_one_pending ON public.publication_reconciliation_event USING btree (output_epoch, position_id) WHERE (state = 'pending'::text);

CREATE INDEX publication_source_cleanup_request_position_idx ON public.publication_source_cleanup_request USING btree (position_id);

CREATE INDEX publication_source_hold_receipt_track_source_idx ON public.publication_source_hold USING btree (receipt_track_source_id, operation_id) WHERE (receipt_track_source_id IS NOT NULL);

CREATE INDEX publication_source_hold_source_idx ON public.publication_source_hold USING btree (source_root_id, source_location_id, work_id, media_variant_id);

CREATE INDEX release_publication_cover_acquisition_idx ON public.release_publication_cover USING btree (release_id) WHERE (acquisition_owner IS NOT NULL);

CREATE INDEX release_track_recording_id_idx ON public.release_track USING btree (recording_id);

CREATE INDEX source_analysis_artifact_cleanup_idx ON public.source_analysis_artifact USING btree (state) WHERE (state = ANY (ARRAY['cleanup_eligible'::text, 'cleanup_failed'::text]));

CREATE UNIQUE INDEX source_analysis_artifact_cleanup_one_claim_idx ON public.source_analysis_artifact_cleanup_item USING btree (artifact_id) WHERE (state = 'claimed'::text);

CREATE INDEX source_analysis_artifact_cleanup_operation_idx ON public.source_analysis_artifact_cleanup_item USING btree (operation_id, operation_attempt, job_id);

CREATE INDEX source_analysis_artifact_work_idx ON public.source_analysis_artifact USING btree (work_id);

CREATE INDEX source_analysis_step_pending_idx ON public.source_analysis_step USING btree (work_id) WHERE (state = 'pending'::text);

CREATE UNIQUE INDEX source_analysis_work_current_location_id_key ON public.source_analysis_work USING btree (current_location_id) WHERE (current_location_id IS NOT NULL);

CREATE INDEX source_cleanup_item_operation_idx ON public.source_cleanup_item USING btree (operation_id);

CREATE INDEX source_cleanup_item_state_idx ON public.source_cleanup_item USING btree (state);

CREATE INDEX source_location_media_variant_idx ON public.source_location USING btree (media_variant_id);

CREATE INDEX source_location_root_id_idx ON public.source_location USING btree (source_root_id);

CREATE INDEX source_scan_candidate_operation_idx ON public.source_scan_candidate USING btree (operation_id);

CREATE INDEX source_work_variant_identity_media_variant_id_idx ON public.source_work_variant_identity USING btree (media_variant_id);

CREATE INDEX tool_installation_package_platform_idx ON public.tool_installation USING btree (package_kind, platform_goos, platform_goarch);

CREATE INDEX track_publication_attempt_phase_idx ON public.track_publication_attempt USING btree (phase);

CREATE INDEX track_publication_epoch_idx ON public.track_publication USING btree (epoch);

CREATE UNIQUE INDEX track_source_one_selected_idx ON public.track_source USING btree (track_id) WHERE selected;

CREATE INDEX track_source_source_metadata_result_idx ON public.track_source USING btree (source_metadata_result_id) WHERE (source_metadata_result_id IS NOT NULL);

-- Triggers
CREATE TRIGGER frozen_matching_apply_request_write_once BEFORE DELETE OR UPDATE ON public.matching_apply_request FOR EACH ROW EXECUTE FUNCTION public.reject_frozen_matching_apply_request_mutation();

CREATE TRIGGER library_deletion_operation_update_guard BEFORE UPDATE ON public.operation FOR EACH ROW EXECUTE FUNCTION public.guard_library_deletion_operation_update();

CREATE TRIGGER local_matching_receipt_immutable BEFORE DELETE OR UPDATE ON public.local_matching_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_local_matching_receipt_mutation();

CREATE CONSTRAINT TRIGGER local_recording_requires_release_track AFTER INSERT OR UPDATE OF id ON public.local_recording DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_local_recording_release_track_parent();

CREATE CONSTRAINT TRIGGER local_release_requires_artist AFTER INSERT OR UPDATE OF id ON public.local_release DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_local_release_artist_parent();

CREATE TRIGGER manual_matching_receipt_delete_write_once BEFORE DELETE ON public.manual_matching_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_manual_matching_receipt_mutation();

CREATE TRIGGER manual_matching_receipt_retirement_guard BEFORE INSERT OR UPDATE ON public.manual_matching_receipt FOR EACH ROW EXECUTE FUNCTION public.guard_manual_matching_receipt_retirement();

CREATE CONSTRAINT TRIGGER manual_matching_receipt_retirement_settled AFTER UPDATE ON public.manual_matching_receipt DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_manual_matching_receipt_retirement();

CREATE TRIGGER manual_picker_search_result_immutable BEFORE UPDATE ON public.manual_picker_search_result FOR EACH ROW EXECUTE FUNCTION public.reject_manual_picker_search_result_mutation();

CREATE TRIGGER matching_acquisition_phase_append_only BEFORE DELETE OR UPDATE ON public.matching_acquisition_phase FOR EACH ROW EXECUTE FUNCTION public.reject_matching_acquisition_phase_mutation();

CREATE TRIGGER matching_acquisition_seal_write_once BEFORE DELETE OR UPDATE ON public.matching_acquisition_seal FOR EACH ROW EXECUTE FUNCTION public.reject_matching_acquisition_seal_mutation();

CREATE TRIGGER matching_apply_receipt_input_binding BEFORE INSERT ON public.matching_apply_receipt FOR EACH ROW EXECUTE FUNCTION public.validate_matching_apply_receipt_input_binding();

CREATE TRIGGER matching_apply_receipt_write_once BEFORE DELETE OR UPDATE ON public.matching_apply_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_apply_receipt_mutation();

CREATE TRIGGER matching_artist_response_guard BEFORE UPDATE ON public.matching_provider_dependency FOR EACH ROW EXECUTE FUNCTION public.guard_matching_artist_response();

CREATE TRIGGER matching_fallback_authority_seal_write_once BEFORE DELETE OR UPDATE ON public.matching_fallback_authority_seal FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_authority_seal_mutation();

CREATE TRIGGER matching_fallback_calculation_receipt_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_calculation_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_calculation_receipt_mutation();

CREATE TRIGGER matching_fallback_evaluation_handoff_write_once BEFORE DELETE OR UPDATE ON public.matching_fallback_evaluation_handoff FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_evaluation_handoff_mutation();

CREATE TRIGGER matching_fallback_lookup_receipt_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_lookup_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_lookup_receipt_mutation();

CREATE TRIGGER matching_fallback_request_registration_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_request_registration FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_request_registration_mutation();

CREATE TRIGGER matching_fallback_search_receipt_immutable BEFORE DELETE OR UPDATE ON public.matching_fallback_search_receipt FOR EACH ROW EXECUTE FUNCTION public.reject_matching_fallback_search_receipt_mutation();

CREATE TRIGGER matching_local_recheck_incumbent_guard BEFORE UPDATE ON public.matching_local_recheck FOR EACH ROW EXECUTE FUNCTION public.guard_matching_local_recheck_incumbent();

CREATE TRIGGER matching_local_recheck_publication_immutable BEFORE UPDATE ON public.matching_local_recheck_publication FOR EACH ROW EXECUTE FUNCTION public.guard_matching_local_recheck_publication();

CREATE TRIGGER matching_run_quiescence_timestamp_immutable BEFORE INSERT OR UPDATE OF quiesced_at ON public.matching_run FOR EACH ROW EXECUTE FUNCTION public.protect_matching_run_quiescence_timestamp();

CREATE TRIGGER matching_run_retirement_authorization_immutable BEFORE DELETE OR UPDATE ON public.matching_run_retirement_authorization FOR EACH ROW EXECUTE FUNCTION public.reject_matching_run_retirement_authorization_mutation();

CREATE TRIGGER matching_run_retirement_authorization_validate BEFORE INSERT ON public.matching_run_retirement_authorization FOR EACH ROW EXECUTE FUNCTION public.validate_matching_run_retirement_authorization();

CREATE TRIGGER media_fingerprint_result_identity_immutable BEFORE UPDATE ON public.media_fingerprint_result FOR EACH ROW EXECUTE FUNCTION public.source_fingerprint_result_identity_immutable();

CREATE TRIGGER media_probe_cache_association_valid BEFORE INSERT OR UPDATE ON public.media_probe_cache FOR EACH ROW EXECUTE FUNCTION public.validate_media_probe_cache_association();

CREATE CONSTRAINT TRIGGER operation_source_analysis_holds_guard AFTER INSERT OR UPDATE ON public.operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_operation_holds();

CREATE CONSTRAINT TRIGGER operation_source_work_hold_guard AFTER INSERT OR DELETE OR UPDATE ON public.operation_source_work_hold DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_operation_holds();

CREATE CONSTRAINT TRIGGER operation_tool_read_hold_guard AFTER INSERT OR DELETE OR UPDATE ON public.operation_tool_read_hold DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_operation_holds();

CREATE TRIGGER publication_attempt_cover_input_immutable BEFORE DELETE OR UPDATE ON public.publication_attempt_cover_input FOR EACH ROW EXECUTE FUNCTION public.reject_publication_attempt_cover_input_mutation();

CREATE CONSTRAINT TRIGGER publication_operation_owner_attempt AFTER INSERT OR DELETE OR UPDATE ON public.track_publication_attempt DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_publication_operation_owner_trigger();

CREATE CONSTRAINT TRIGGER publication_operation_owner_cleanup_item AFTER INSERT OR DELETE OR UPDATE ON public.source_cleanup_item DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_publication_operation_owner_trigger();

CREATE CONSTRAINT TRIGGER publication_operation_owner_operation AFTER INSERT OR DELETE OR UPDATE ON public.operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_publication_operation_owner_trigger();

CREATE CONSTRAINT TRIGGER publication_operation_owner_retry_grant AFTER INSERT OR DELETE OR UPDATE ON public.source_cleanup_explicit_retry DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();

CREATE CONSTRAINT TRIGGER publication_operation_owner_retry_item AFTER INSERT OR DELETE OR UPDATE ON public.source_cleanup_explicit_retry_item DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();

CREATE CONSTRAINT TRIGGER publication_operation_owner_retry_operation AFTER INSERT OR DELETE OR UPDATE ON public.operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();

CREATE TRIGGER publication_reconciliation_event_update BEFORE UPDATE ON public.publication_reconciliation_event FOR EACH ROW EXECUTE FUNCTION public.guard_publication_reconciliation_event_update();

CREATE CONSTRAINT TRIGGER publication_reconciliation_owner_event AFTER INSERT OR DELETE OR UPDATE ON public.publication_reconciliation_event DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_publication_reconciliation_owner_trigger();

CREATE CONSTRAINT TRIGGER publication_reconciliation_owner_operation AFTER INSERT OR DELETE OR UPDATE ON public.operation DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_publication_reconciliation_owner_trigger();

CREATE TRIGGER publication_root_abandonment_immutable BEFORE DELETE OR UPDATE ON public.publication_root_abandonment FOR EACH ROW EXECUTE FUNCTION public.reject_publication_root_abandonment_mutation();

CREATE CONSTRAINT TRIGGER release_artist_preserves_old_parent AFTER DELETE OR UPDATE OF release_id ON public.release_artist DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_release_artist_old_parent();

CREATE TRIGGER release_artist_prevent_orphaning_truncate BEFORE TRUNCATE ON public.release_artist FOR EACH STATEMENT EXECUTE FUNCTION public.prevent_release_artist_truncate_orphans();

CREATE TRIGGER release_track_ensure_medium BEFORE INSERT OR UPDATE OF release_id, medium ON public.release_track FOR EACH ROW EXECUTE FUNCTION public.ensure_release_medium_for_track();

CREATE CONSTRAINT TRIGGER release_track_preserves_old_parent AFTER DELETE OR UPDATE OF release_id, recording_id ON public.release_track DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_release_track_old_parent();

CREATE TRIGGER release_track_prevent_orphaning_truncate BEFORE TRUNCATE ON public.release_track FOR EACH STATEMENT EXECUTE FUNCTION public.prevent_release_track_truncate_orphans();

CREATE CONSTRAINT TRIGGER source_analysis_step_membership_guard AFTER INSERT OR DELETE OR UPDATE ON public.source_analysis_step DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_analysis_step_membership();

CREATE TRIGGER source_analysis_work_attachment_guard BEFORE INSERT OR UPDATE OF current_location_id, invalidated_at ON public.source_analysis_work FOR EACH ROW EXECUTE FUNCTION public.source_analysis_work_attachment_guard();

CREATE TRIGGER source_analysis_work_execution_immutable BEFORE UPDATE ON public.source_analysis_work_execution FOR EACH ROW EXECUTE FUNCTION public.prevent_source_analysis_work_execution_mutation();

CREATE TRIGGER source_analysis_work_execution_requires_live_identity BEFORE INSERT ON public.source_analysis_work_execution FOR EACH ROW EXECUTE FUNCTION public.source_analysis_work_execution_requires_live_identity();

CREATE TRIGGER source_analysis_work_hold_requires_current_attachment BEFORE INSERT OR UPDATE ON public.operation_source_work_hold FOR EACH ROW EXECUTE FUNCTION public.source_analysis_work_hold_requires_current_attachment();

CREATE TRIGGER source_cleanup_explicit_retry_binding_immutable BEFORE UPDATE ON public.source_cleanup_explicit_retry FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_explicit_retry_binding();

CREATE TRIGGER source_cleanup_explicit_retry_item_immutable BEFORE DELETE OR UPDATE ON public.source_cleanup_explicit_retry_item FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_explicit_retry_item();

CREATE TRIGGER source_cleanup_frontier_transfer_proof_canonical BEFORE INSERT OR UPDATE ON public.source_cleanup_frontier_transfer_proof FOR EACH ROW EXECUTE FUNCTION public.validate_source_cleanup_frontier_transfer_proof();

CREATE CONSTRAINT TRIGGER source_cleanup_frontier_transfer_proof_consumed AFTER INSERT ON public.source_cleanup_frontier_transfer_proof DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.finish_source_cleanup_frontier_transfer_proof();

CREATE CONSTRAINT TRIGGER source_cleanup_recovery_item_coverage AFTER INSERT OR DELETE OR UPDATE ON public.source_cleanup_recovery_item DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();

CREATE TRIGGER source_cleanup_recovery_item_protected BEFORE INSERT OR DELETE OR UPDATE ON public.source_cleanup_recovery_item FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_recovery_item();

CREATE CONSTRAINT TRIGGER source_cleanup_recovery_state_coverage AFTER INSERT OR DELETE OR UPDATE ON public.source_cleanup_recovery_state DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();

CREATE TRIGGER source_cleanup_recovery_state_protected BEFORE INSERT OR DELETE OR UPDATE ON public.source_cleanup_recovery_state FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_recovery_state();

CREATE TRIGGER source_cleanup_retry_frontier_immutable BEFORE UPDATE ON public.source_cleanup_explicit_retry FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_retry_frontier();

CREATE CONSTRAINT TRIGGER source_cleanup_retry_grant_frontier_coverage AFTER INSERT OR DELETE OR UPDATE ON public.source_cleanup_explicit_retry DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_recovery_coverage();

CREATE TRIGGER source_cleanup_retry_resolution_immutable BEFORE DELETE OR UPDATE ON public.source_cleanup_explicit_retry_resolution FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_retry_resolution();

CREATE TRIGGER source_cleanup_root_abandonment_capture BEFORE INSERT ON public.source_cleanup_root_abandonment FOR EACH ROW EXECUTE FUNCTION public.capture_source_cleanup_root_abandonment();

CREATE TRIGGER source_cleanup_root_abandonment_immutable BEFORE DELETE OR UPDATE ON public.source_cleanup_root_abandonment FOR EACH ROW EXECUTE FUNCTION public.reject_source_cleanup_root_abandonment_mutation();

CREATE CONSTRAINT TRIGGER source_cleanup_root_abandonment_retired AFTER INSERT ON public.source_cleanup_root_abandonment DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.check_source_cleanup_root_abandonment_retired();

CREATE TRIGGER source_work_variant_identity_no_rekey BEFORE UPDATE ON public.source_work_variant_identity FOR EACH ROW EXECUTE FUNCTION public.prevent_source_work_variant_identity_rekey();

CREATE TRIGGER source_work_variant_identity_requires_live_identity BEFORE INSERT ON public.source_work_variant_identity FOR EACH ROW EXECUTE FUNCTION public.source_work_variant_identity_requires_live_identity();

-- Foreign keys
ALTER TABLE ONLY public.collection_artist
    ADD CONSTRAINT collection_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.collection_artist
    ADD CONSTRAINT collection_artist_collection_id_fkey FOREIGN KEY (collection_id) REFERENCES public.collection(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.collection_release
    ADD CONSTRAINT collection_release_collection_id_fkey FOREIGN KEY (collection_id) REFERENCES public.collection(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.collection_release
    ADD CONSTRAINT collection_release_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.cover_art_archive_image
    ADD CONSTRAINT cover_art_archive_image_release_fkey FOREIGN KEY (release_mbid) REFERENCES public.cover_art_archive_release(release_mbid) ON DELETE CASCADE;

ALTER TABLE ONLY public.incoming_group_member
    ADD CONSTRAINT incoming_group_member_group_id_fkey FOREIGN KEY (group_id) REFERENCES public.incoming_group(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incoming_group_member_location
    ADD CONSTRAINT incoming_group_member_location_group_id_variant_id_fkey FOREIGN KEY (group_id, variant_id) REFERENCES public.incoming_group_member(group_id, variant_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incoming_match_evaluation
    ADD CONSTRAINT incoming_match_evaluation_frozen_seal_fk FOREIGN KEY (frozen_run_id, frozen_wait_revision, frozen_seal_digest) REFERENCES public.matching_fallback_authority_seal(run_id, wait_revision, authority_digest) ON DELETE RESTRICT;

ALTER TABLE ONLY public.incoming_match_evaluation_location
    ADD CONSTRAINT incoming_match_evaluation_location_group_id_file_member_id_fkey FOREIGN KEY (group_id, file_member_id) REFERENCES public.incoming_match_evaluation_member(group_id, file_member_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incoming_match_evaluation_member
    ADD CONSTRAINT incoming_match_evaluation_member_group_id_fkey FOREIGN KEY (group_id) REFERENCES public.incoming_match_evaluation(group_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.incoming_match_evaluation
    ADD CONSTRAINT incoming_match_evaluation_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.incoming_matching_generation
    ADD CONSTRAINT incoming_matching_generation_current_run_fk FOREIGN KEY (current_run_id) REFERENCES public.matching_run(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.local_release
    ADD CONSTRAINT local_release_primary_artist_id_fkey FOREIGN KEY (primary_artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_lookup_operation_id_fkey FOREIGN KEY (lookup_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_lookup_operation_id_fkey FOREIGN KEY (lookup_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.manual_matching_lookup_seal
    ADD CONSTRAINT manual_matching_lookup_seal_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.manual_matching_admission(request_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.manual_matching_receipt
    ADD CONSTRAINT manual_matching_receipt_local_release_id_fkey FOREIGN KEY (local_release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.manual_matching_receipt
    ADD CONSTRAINT manual_matching_receipt_retired_operation_fkey FOREIGN KEY (retired_by_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.manual_picker_search
    ADD CONSTRAINT manual_picker_search_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id);

ALTER TABLE ONLY public.manual_picker_search_result
    ADD CONSTRAINT manual_picker_search_result_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.manual_picker_search(operation_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.manual_picker_search_result
    ADD CONSTRAINT manual_picker_search_result_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id);

ALTER TABLE ONLY public.matching_acquisition_phase
    ADD CONSTRAINT matching_acquisition_phase_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_acquisition(run_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_acquisition
    ADD CONSTRAINT matching_acquisition_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_acquisition_seal
    ADD CONSTRAINT matching_acquisition_seal_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_acquisition(run_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_apply_receipt
    ADD CONSTRAINT matching_apply_receipt_apply_operation_id_fkey FOREIGN KEY (apply_operation_id) REFERENCES public.matching_apply_request(apply_operation_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_apply_receipt
    ADD CONSTRAINT matching_apply_receipt_request_input_fk FOREIGN KEY (apply_operation_id, input_sha256) REFERENCES public.matching_apply_request(apply_operation_id, input_sha256) DEFERRABLE;

ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_apply_operation_id_fkey FOREIGN KEY (apply_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_frozen_seal_fk FOREIGN KEY (parent_run_id, seal_wait_revision, seal_digest) REFERENCES public.matching_fallback_authority_seal(run_id, wait_revision, authority_digest) DEFERRABLE;

ALTER TABLE ONLY public.matching_apply_request
    ADD CONSTRAINT matching_apply_request_parent_run_id_fkey FOREIGN KEY (parent_run_id) REFERENCES public.matching_run(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_delivery
    ADD CONSTRAINT matching_delivery_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_fallback_authority_seal
    ADD CONSTRAINT matching_fallback_authority_seal_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation__run_id_acquisition_revision_fkey FOREIGN KEY (run_id, acquisition_revision) REFERENCES public.matching_acquisition_phase(run_id, revision) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_evaluation_handoff
    ADD CONSTRAINT matching_fallback_evaluation_handoff_run_id_wait_revision_fkey FOREIGN KEY (run_id, wait_revision) REFERENCES public.matching_fallback_authority_seal(run_id, wait_revision) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_fallback_lookup_receipt
    ADD CONSTRAINT matching_fallback_lookup_receipt_run_id_request_id_fkey FOREIGN KEY (run_id, request_id) REFERENCES public.matching_provider_dependency(parent_run_id, request_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_fallback_request_registration
    ADD CONSTRAINT matching_fallback_request_registration_run_id_request_id_fkey FOREIGN KEY (run_id, request_id) REFERENCES public.matching_provider_dependency(parent_run_id, request_id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_fallback_search_receipt
    ADD CONSTRAINT matching_fallback_search_receipt_run_id_request_id_fkey FOREIGN KEY (run_id, request_id) REFERENCES public.matching_provider_dependency(parent_run_id, request_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_local_recheck
    ADD CONSTRAINT matching_local_recheck_local_apply_operation_fk FOREIGN KEY (local_apply_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_local_recheck_publication
    ADD CONSTRAINT matching_local_recheck_publication_operation_fk FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_local_recheck_publication
    ADD CONSTRAINT matching_local_recheck_publication_position_fk FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_local_recheck_publication
    ADD CONSTRAINT matching_local_recheck_publication_recheck_fk FOREIGN KEY (recheck_id) REFERENCES public.matching_local_recheck(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_local_recheck_source
    ADD CONSTRAINT matching_local_recheck_source_recheck_fk FOREIGN KEY (recheck_id) REFERENCES public.matching_local_recheck(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_local_recheck_source
    ADD CONSTRAINT matching_local_recheck_source_root_fk FOREIGN KEY (source_root_id) REFERENCES public.source_root(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_provider_dependency
    ADD CONSTRAINT matching_provider_dependency_parent_run_id_fkey FOREIGN KEY (parent_run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_provider_dependency
    ADD CONSTRAINT matching_provider_dependency_request_id_fkey FOREIGN KEY (request_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_run
    ADD CONSTRAINT matching_run_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.matching_run_retirement_authorization
    ADD CONSTRAINT matching_run_retirement_authorization_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.matching_run_target
    ADD CONSTRAINT matching_run_target_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.matching_run(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.media_fingerprint_result
    ADD CONSTRAINT media_fingerprint_result_source_sha256_fkey FOREIGN KEY (source_sha256) REFERENCES public.media_variant(source_sha256) ON DELETE RESTRICT;

ALTER TABLE ONLY public.media_probe_cache
    ADD CONSTRAINT media_probe_cache_result_id_fkey FOREIGN KEY (result_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.media_probe_cache
    ADD CONSTRAINT media_probe_cache_source_sha256_fkey FOREIGN KEY (source_sha256) REFERENCES public.media_variant(source_sha256) ON DELETE RESTRICT;

ALTER TABLE ONLY public.obsolete_publication_output_cleanup
    ADD CONSTRAINT obsolete_publication_output_cleanup_claim_fkey FOREIGN KEY (namespace_claim_id, epoch, position_id, relative_path, claim_node_kind) REFERENCES public.publication_namespace_claim(id, epoch, owner_position_id, relative_path, node_kind) ON DELETE RESTRICT;

ALTER TABLE ONLY public.obsolete_publication_output_cleanup
    ADD CONSTRAINT obsolete_publication_output_cleanup_epoch_fkey FOREIGN KEY (epoch) REFERENCES public.publication_namespace_state(output_epoch) ON DELETE RESTRICT;

ALTER TABLE ONLY public.obsolete_publication_output_cleanup
    ADD CONSTRAINT obsolete_publication_output_cleanup_operation_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.obsolete_publication_output_cleanup
    ADD CONSTRAINT obsolete_publication_output_cleanup_position_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.obsolete_publication_output_cleanup
    ADD CONSTRAINT obsolete_publication_output_cleanup_reservation_fkey FOREIGN KEY (namespace_claim_id, position_id, generation) REFERENCES public.publication_namespace_reservation(namespace_claim_id, position_id, generation) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operation_source_work_hold
    ADD CONSTRAINT operation_source_work_hold_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operation_source_work_hold
    ADD CONSTRAINT operation_source_work_hold_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_installation_id_fkey FOREIGN KEY (target_installation_id) REFERENCES public.tool_installation(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_source_location_id_fkey FOREIGN KEY (target_source_location_id) REFERENCES public.source_location(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_source_root_id_fkey FOREIGN KEY (target_source_root_id) REFERENCES public.source_root(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_target_work_id_fkey FOREIGN KEY (target_work_id) REFERENCES public.source_analysis_work(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.operation_tool_read_hold
    ADD CONSTRAINT operation_tool_read_hold_installation_id_fkey FOREIGN KEY (installation_id) REFERENCES public.tool_installation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.operation_tool_read_hold
    ADD CONSTRAINT operation_tool_read_hold_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.position_source_authority
    ADD CONSTRAINT position_source_authority_position_id_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_artist_discography
    ADD CONSTRAINT provider_artist_discography_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_local_artist_id_fkey FOREIGN KEY (local_artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_provider_id_provider_artist_id_fkey FOREIGN KEY (provider_id, provider_artist_id) REFERENCES public.provider_artist(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_local_recording_id_fkey FOREIGN KEY (local_recording_id) REFERENCES public.local_recording(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_provider_id_provider_recording_id_fkey FOREIGN KEY (provider_id, provider_recording_id) REFERENCES public.provider_recording(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_local_release_id_fkey FOREIGN KEY (local_release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_provider_id_provider_release_id_fkey FOREIGN KEY (provider_id, provider_release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_local_track_id_fkey FOREIGN KEY (local_track_id) REFERENCES public.local_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_provider_id_provider_track_id_fkey FOREIGN KEY (provider_id, provider_track_id) REFERENCES public.provider_release_track(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_release_artist_credit
    ADD CONSTRAINT provider_release_artist_credit_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.provider_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_release_artist_credit
    ADD CONSTRAINT provider_release_artist_credit_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_recording_id_fkey FOREIGN KEY (recording_id) REFERENCES public.provider_recording(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_response_cache
    ADD CONSTRAINT provider_response_cache_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_source_entity
    ADD CONSTRAINT provider_source_entity_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artis_provider_id_provider_source__fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artist_cred_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artist_credi_provider_id_artist_id_fkey FOREIGN KEY (provider_id, artist_id) REFERENCES public.provider_artist(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_provider_source__fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_recording_id_fkey FOREIGN KEY (provider_id, recording_id) REFERENCES public.provider_recording(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_track_id_fkey FOREIGN KEY (provider_id, track_id) REFERENCES public.provider_release_track(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_attempt_cover_input
    ADD CONSTRAINT publication_attempt_cover_input_attempt_fkey FOREIGN KEY (operation_id, position_id) REFERENCES public.track_publication_attempt(operation_id, position_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.publication_attempt_cover_input
    ADD CONSTRAINT publication_attempt_cover_input_release_position_fkey FOREIGN KEY (position_id, release_id) REFERENCES public.release_track(id, release_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_automatic_album
    ADD CONSTRAINT publication_automatic_album_local_operation_id_fkey FOREIGN KEY (local_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_automatic_album
    ADD CONSTRAINT publication_automatic_album_receipt_operation_id_fkey FOREIGN KEY (receipt_operation_id) REFERENCES public.matching_apply_receipt(apply_operation_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_execution_claim
    ADD CONSTRAINT publication_execution_claim_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id) REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_manual_lyrics_receipt
    ADD CONSTRAINT publication_manual_lyrics_receipt_position_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_namespace_claim
    ADD CONSTRAINT publication_namespace_claim_owner_fkey FOREIGN KEY (owner_position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_namespace_reservation
    ADD CONSTRAINT publication_namespace_reservation_claim_fkey FOREIGN KEY (namespace_claim_id) REFERENCES public.publication_namespace_claim(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_namespace_reservation
    ADD CONSTRAINT publication_namespace_reservation_position_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_reconciliation_dirty_scope
    ADD CONSTRAINT publication_reconciliation_dirty__output_epoch_position_id_fkey FOREIGN KEY (output_epoch, position_id) REFERENCES public.publication_reconciliation_request(output_epoch, position_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.publication_reconciliation_event
    ADD CONSTRAINT publication_reconciliation_event_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id);

ALTER TABLE ONLY public.publication_reconciliation_event
    ADD CONSTRAINT publication_reconciliation_event_output_epoch_position_id_fkey FOREIGN KEY (output_epoch, position_id) REFERENCES public.publication_reconciliation_request(output_epoch, position_id);

ALTER TABLE ONLY public.publication_reconciliation_request
    ADD CONSTRAINT publication_reconciliation_request_position_id_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id);

ALTER TABLE ONLY public.publication_source_cleanup_request
    ADD CONSTRAINT publication_source_cleanup_reques_originating_operation_id_fkey FOREIGN KEY (originating_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_source_cleanup_request
    ADD CONSTRAINT publication_source_cleanup_request_position_id_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_attempt_fkey FOREIGN KEY (operation_id, epoch) REFERENCES public.track_publication_attempt(operation_id, epoch) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_location_fkey FOREIGN KEY (source_location_id) REFERENCES public.source_location(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id) REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_receipt_pin_fkey FOREIGN KEY (receipt_track_source_id, receipt_track_id, media_variant_id, receipt_compatible) REFERENCES public.track_source(id, track_id, media_variant_id, compatible) ON UPDATE RESTRICT ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_root_fkey FOREIGN KEY (source_root_id) REFERENCES public.source_root(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_variant_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.publication_source_hold
    ADD CONSTRAINT publication_source_hold_work_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.recording_artist
    ADD CONSTRAINT recording_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.recording_artist
    ADD CONSTRAINT recording_artist_recording_id_fkey FOREIGN KEY (recording_id) REFERENCES public.local_recording(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.release_artist
    ADD CONSTRAINT release_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_artist
    ADD CONSTRAINT release_artist_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_medium
    ADD CONSTRAINT release_medium_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_publication_cover
    ADD CONSTRAINT release_publication_cover_release_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_medium_fk FOREIGN KEY (release_id, medium) REFERENCES public.release_medium(release_id, medium);

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_primary_artist_id_fkey FOREIGN KEY (primary_artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_recording_id_fkey FOREIGN KEY (recording_id) REFERENCES public.local_recording(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.local_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_execution_fkey FOREIGN KEY (work_id, owner_operation_id, owner_operation_attempt, owner_job_id) REFERENCES public.source_analysis_work_execution(work_id, operation_id, operation_attempt, job_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_artifact
    ADD CONSTRAINT source_analysis_artifact_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_execution_operation_id_execution_oper_fkey FOREIGN KEY (execution_operation_id, execution_operation_attempt, execution_job_id) REFERENCES public.operation(id, attempt, river_job_id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_fingerprint_result_id_fkey FOREIGN KEY (success_fingerprint_result_id) REFERENCES public.media_fingerprint_result(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_metadata_result_id_fkey FOREIGN KEY (success_metadata_result_id) REFERENCES public.media_metadata_result(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_probe_variant_id_fkey FOREIGN KEY (success_probe_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_success_sha_variant_id_fkey FOREIGN KEY (success_sha_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_work_hold_fkey FOREIGN KEY (execution_operation_id, work_id) REFERENCES public.operation_source_work_hold(operation_id, work_id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.source_analysis_step
    ADD CONSTRAINT source_analysis_step_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_artifact_fk FOREIGN KEY (work_id, artifact_id) REFERENCES public.source_analysis_artifact(work_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_execution_fk FOREIGN KEY (work_id, borrower_operation_id, borrower_operation_attempt, borrower_job_id) REFERENCES public.source_analysis_work_execution(work_id, operation_id, operation_attempt, job_id) ON DELETE SET NULL (borrower_operation_id, borrower_operation_attempt, borrower_job_id);

ALTER TABLE ONLY public.source_analysis_work_artifact_binding
    ADD CONSTRAINT source_analysis_work_artifact_binding_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_analysis_work
    ADD CONSTRAINT source_analysis_work_current_location_fk FOREIGN KEY (current_location_id, source_root_id) REFERENCES public.source_location(id, source_root_id) ON DELETE SET NULL (current_location_id);

ALTER TABLE ONLY public.source_analysis_work_execution
    ADD CONSTRAINT source_analysis_work_execution_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_analysis_work_execution
    ADD CONSTRAINT source_analysis_work_execution_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_cleanup_completion
    ADD CONSTRAINT source_cleanup_completion_media_variant_id_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_cleanup_completion
    ADD CONSTRAINT source_cleanup_completion_source_root_id_fkey FOREIGN KEY (source_root_id) REFERENCES public.source_root(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_consumed_claim_fkey FOREIGN KEY (recovery_operation_id, consumed_application_attempt, consumed_river_job_id, consumed_claim_uuid) REFERENCES public.publication_execution_claim(operation_id, application_attempt, river_job_id, claim_uuid) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry_item
    ADD CONSTRAINT source_cleanup_explicit_retry_item_id_original_operation_i_fkey FOREIGN KEY (item_id, original_operation_id) REFERENCES public.source_cleanup_item(id, operation_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry_item
    ADD CONSTRAINT source_cleanup_explicit_retry_item_item_id_owner_uuid_fkey FOREIGN KEY (item_id, owner_uuid) REFERENCES public.source_cleanup_owner(item_id, owner_uuid) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry_item
    ADD CONSTRAINT source_cleanup_explicit_retry_item_retry_id_fkey FOREIGN KEY (retry_id) REFERENCES public.source_cleanup_explicit_retry(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_original_claim_fkey FOREIGN KEY (original_operation_id, original_claim_uuid) REFERENCES public.publication_execution_claim(operation_id, claim_uuid) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_original_operation_fkey FOREIGN KEY (original_operation_id, original_application_attempt, original_river_job_id) REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_position_id_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_recovery_operation_id_fkey FOREIGN KEY (recovery_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry_resolution
    ADD CONSTRAINT source_cleanup_explicit_retry_resolu_recovery_operation_id_fkey FOREIGN KEY (recovery_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry_resolution
    ADD CONSTRAINT source_cleanup_explicit_retry_resolution_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.source_cleanup_item(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry_resolution
    ADD CONSTRAINT source_cleanup_explicit_retry_resolution_retry_id_fkey FOREIGN KEY (retry_id) REFERENCES public.source_cleanup_explicit_retry(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_item
    ADD CONSTRAINT source_cleanup_item_mutation_claim_fkey FOREIGN KEY (operation_id, mutation_claim_uuid) REFERENCES public.publication_execution_claim(operation_id, claim_uuid) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_item
    ADD CONSTRAINT source_cleanup_item_operation_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_item
    ADD CONSTRAINT source_cleanup_item_position_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_owner
    ADD CONSTRAINT source_cleanup_owner_item_fkey FOREIGN KEY (item_id, operation_id) REFERENCES public.source_cleanup_item(id, operation_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_owner
    ADD CONSTRAINT source_cleanup_owner_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id) REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_recovery_item
    ADD CONSTRAINT source_cleanup_recovery_item_item_id_original_operation_id_fkey FOREIGN KEY (item_id, original_operation_id) REFERENCES public.source_cleanup_item(id, operation_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_recovery_item
    ADD CONSTRAINT source_cleanup_recovery_item_original_operation_id_fkey FOREIGN KEY (original_operation_id) REFERENCES public.source_cleanup_recovery_state(original_operation_id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_recovery_state
    ADD CONSTRAINT source_cleanup_recovery_state_original_operation_id_fkey FOREIGN KEY (original_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_retry_frontier_revision_fkey FOREIGN KEY (original_operation_id, frontier_revision) REFERENCES public.source_cleanup_recovery_state(original_operation_id, revision) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_media_variant_id_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_location
    ADD CONSTRAINT source_location_source_root_id_fkey FOREIGN KEY (source_root_id) REFERENCES public.source_root(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_scan_candidate
    ADD CONSTRAINT source_scan_candidate_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.source_work_variant_identity
    ADD CONSTRAINT source_work_variant_identity_media_variant_id_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.source_work_variant_identity
    ADD CONSTRAINT source_work_variant_identity_work_id_fkey FOREIGN KEY (work_id) REFERENCES public.source_analysis_work(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.tools_execution_claim
    ADD CONSTRAINT tools_execution_claim_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.track_artist
    ADD CONSTRAINT track_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.track_artist
    ADD CONSTRAINT track_artist_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.local_track(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_operation_fkey FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_position_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.track_publication
    ADD CONSTRAINT track_publication_position_fkey FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_media_variant_id_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_source_metadata_result_fk FOREIGN KEY (source_metadata_result_id) REFERENCES public.media_metadata_result(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.local_track(id) ON DELETE CASCADE;

-- Required seed rows (not historical application data).

-- The genre catalog is a single atomically replaced JSONB snapshot.
INSERT INTO public.genre_catalog (singleton) VALUES (true);

-- Grouping revision state is a required singleton.
INSERT INTO public.incoming_grouping_state (singleton, revision, needs_refresh) VALUES (true, 0, true);

-- Publication namespaces start with a fresh output epoch.
INSERT INTO public.publication_namespace_state (singleton, output_epoch) VALUES (true, gen_random_uuid());

-- Stable, server-owned provider namespace for operator-managed catalog records.
INSERT INTO public.provider (id, code) VALUES ('00000000-0000-4000-8000-000000000001', 'local');
