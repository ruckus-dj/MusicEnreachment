-- Add the operation proof shape and narrow release-deletion retirement for
-- immutable manual-matching confirmation receipts.
SET LOCAL check_function_bodies = false;

ALTER TABLE public.operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind = ANY (ARRAY[
        'install', 'activate', 'delete', 'delete_library_v1', 'move_tools_root',
        'scan_source', 'analyze_source', 'cleanup_source_analysis_artifacts',
        'provider_release_lookup', 'provider_recording_lookup', 'provider_release_search',
        'provider_recording_search', 'provider_artist_search', 'provider_artist_release_browse',
        'provider_acoustid_lookup', 'matching_evaluation_v1', 'matching_apply_v1',
        'publish_track_v1', 'cleanup_publication_sources_v1', 'publication_reconcile_v1',
        'retry_publication_sources_v1', 'obsolete_publication_output_cleanup_v1',
        'refresh_genre_catalog_v1'
    ]));

ALTER TABLE public.operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1',
            'retry_publication_sources_v1','obsolete_publication_output_cleanup_v1','refresh_genre_catalog_v1',
            'delete_library_v1') OR
        kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
            'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
            'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup',
            'matching_evaluation_v1','matching_apply_v1') OR
        state NOT IN ('queued','running') OR target_installation_id IS NOT NULL
    );

ALTER TABLE public.operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1',
            'retry_publication_sources_v1','obsolete_publication_output_cleanup_v1','refresh_genre_catalog_v1',
            'delete_library_v1') OR
        kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
            'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
            'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup',
            'matching_evaluation_v1','matching_apply_v1') OR
        COALESCE(input_snapshot->>'target_identity','') <> ''
    );

CREATE FUNCTION public.valid_library_deletion_uuid_array(values_json jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
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
$$;

ALTER TABLE public.operation
    ADD CONSTRAINT operation_library_deletion_shape CHECK (
        kind <> 'delete_library_v1'
        OR (
            target_installation_id IS NULL
            AND target_source_root_id IS NULL
            AND target_source_location_id IS NULL
            AND source_analysis_mode IS NULL
            AND target_work_id IS NULL
            AND target_step IS NULL
            AND provider_source_id IS NULL
            AND provider_configuration_identity IS NULL
            AND provider_cache_key IS NULL
            AND provider_explicit_refresh IS FALSE
            AND jsonb_typeof(input_snapshot) = 'object'
            AND input_snapshot ?& ARRAY['schema_version','scope','scope_digest','confirmation_digest','ownership_witnesses','residual_publication_ids','dependencies']
            AND input_snapshot - ARRAY['schema_version','scope','scope_digest','confirmation_digest','ownership_witnesses','residual_publication_ids','dependencies'] = '{}'::jsonb
            AND input_snapshot->>'schema_version' = '2'
            AND jsonb_typeof(input_snapshot->'scope') = 'object'
            AND jsonb_typeof(input_snapshot->'dependencies') = 'object'
            AND jsonb_typeof(input_snapshot->'ownership_witnesses') = 'array'
            AND jsonb_typeof(input_snapshot->'residual_publication_ids') = 'array'
            AND length(input_snapshot->>'scope_digest') = 64
            AND input_snapshot->>'scope_digest' ~ '^[0-9a-f]{64}$'
            AND length(input_snapshot->>'confirmation_digest') = 64
            AND input_snapshot->>'confirmation_digest' ~ '^[0-9a-f]{64}$'
        ) IS TRUE
    );

ALTER TABLE public.operation
    ADD CONSTRAINT operation_library_deletion_target_shape CHECK (
        kind <> 'delete_library_v1'
        OR (
            jsonb_typeof(input_snapshot->'scope') = 'object'
            AND input_snapshot->'scope' ?& ARRAY['kind','release_id','publication_ids','manual_receipt_request_ids','dependency_digest']
            AND (input_snapshot->'scope') - ARRAY['kind','release_id','publication_ids','manual_receipt_request_ids','dependency_digest'] = '{}'::jsonb
            AND input_snapshot->'scope'->>'kind' IN ('publication','release')
            AND input_snapshot->'scope'->>'release_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            AND jsonb_typeof(input_snapshot->'scope'->'publication_ids') = 'array'
            AND jsonb_typeof(input_snapshot->'scope'->'manual_receipt_request_ids') = 'array'
            AND public.valid_library_deletion_uuid_array(input_snapshot->'scope'->'publication_ids') IS TRUE
            AND public.valid_library_deletion_uuid_array(input_snapshot->'scope'->'manual_receipt_request_ids') IS TRUE
            AND length(input_snapshot->'scope'->>'dependency_digest') = 64
            AND input_snapshot->'scope'->>'dependency_digest' ~ '^[0-9a-f]{64}$'
            AND (
                (input_snapshot->'scope'->>'kind' = 'publication'
                    AND jsonb_array_length(input_snapshot->'scope'->'publication_ids') = 1
                    AND jsonb_array_length(input_snapshot->'scope'->'manual_receipt_request_ids') = 0)
                OR input_snapshot->'scope'->>'kind' = 'release'
            )
        ) IS TRUE
    );

CREATE FUNCTION public.valid_library_deletion_residual(snapshot jsonb, operation_state text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
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

ALTER TABLE public.operation
    ADD CONSTRAINT operation_library_deletion_residual_subset CHECK (
        kind <> 'delete_library_v1'
        OR public.valid_library_deletion_residual(input_snapshot, state) IS TRUE
    );

ALTER TABLE public.manual_matching_receipt
    ADD COLUMN original_local_release_id uuid NOT NULL,
    ADD COLUMN retired_by_operation_id uuid;

DROP TRIGGER manual_matching_receipt_write_once ON public.manual_matching_receipt;
CREATE TRIGGER manual_matching_receipt_delete_write_once
BEFORE DELETE ON public.manual_matching_receipt
FOR EACH ROW EXECUTE FUNCTION public.reject_manual_matching_receipt_mutation();

ALTER TABLE public.manual_matching_receipt
    ALTER COLUMN local_release_id DROP NOT NULL,
    ADD CONSTRAINT manual_matching_receipt_retired_operation_fkey
        FOREIGN KEY (retired_by_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT,
    ADD CONSTRAINT manual_matching_receipt_retirement_shape CHECK (
        (local_release_id IS NOT NULL AND local_release_id = original_local_release_id AND retired_by_operation_id IS NULL)
        OR (local_release_id IS NULL AND retired_by_operation_id IS NOT NULL)
    );

ALTER TABLE public.release_track
    DROP CONSTRAINT release_track_release_id_fkey,
    ADD CONSTRAINT release_track_release_id_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE public.release_artist
    DROP CONSTRAINT release_artist_release_id_fkey,
    ADD CONSTRAINT release_artist_release_id_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE RESTRICT;

ALTER TABLE public.release_medium
    DROP CONSTRAINT release_medium_release_id_fkey,
    ADD CONSTRAINT release_medium_release_id_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE RESTRICT;

CREATE FUNCTION public.guard_manual_matching_receipt_retirement() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    owner_operation public.operation%ROWTYPE;
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
    IF NOT FOUND
       OR owner_operation.kind <> 'delete_library_v1'
       OR owner_operation.state <> 'running'
       OR owner_operation.river_job_id IS NULL
       OR owner_operation.input_snapshot->'scope'->>'kind' <> 'release'
       OR owner_operation.input_snapshot->'scope'->>'release_id' <> OLD.original_local_release_id::text
       OR NOT (owner_operation.input_snapshot->'scope'->'manual_receipt_request_ids' @> jsonb_build_array(OLD.request_id::text))
       OR jsonb_array_length(owner_operation.input_snapshot->'residual_publication_ids') <> 0 THEN
        RAISE EXCEPTION 'manual matching receipt retirement lacks current release-deletion authority';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER manual_matching_receipt_retirement_guard
BEFORE INSERT OR UPDATE ON public.manual_matching_receipt
FOR EACH ROW EXECUTE FUNCTION public.guard_manual_matching_receipt_retirement();

CREATE FUNCTION public.check_manual_matching_receipt_retirement() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    receipt public.manual_matching_receipt%ROWTYPE;
    owner_operation public.operation%ROWTYPE;
BEGIN
    SELECT * INTO receipt FROM public.manual_matching_receipt WHERE request_id = NEW.request_id;
    IF NOT FOUND OR receipt.retired_by_operation_id IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT * INTO owner_operation FROM public.operation WHERE id = receipt.retired_by_operation_id;
    IF NOT FOUND
       OR owner_operation.kind <> 'delete_library_v1'
       OR owner_operation.state <> 'succeeded'
       OR receipt.local_release_id IS NOT NULL
       OR EXISTS (SELECT 1 FROM public.local_release WHERE id = receipt.original_local_release_id)
       OR owner_operation.input_snapshot->'scope'->>'kind' <> 'release'
       OR owner_operation.input_snapshot->'scope'->>'release_id' <> receipt.original_local_release_id::text
       OR NOT (owner_operation.input_snapshot->'scope'->'manual_receipt_request_ids' @> jsonb_build_array(receipt.request_id::text))
       OR jsonb_array_length(owner_operation.input_snapshot->'residual_publication_ids') <> 0 THEN
        RAISE EXCEPTION 'manual matching receipt retirement did not settle with its release deletion';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER manual_matching_receipt_retirement_settled
AFTER UPDATE ON public.manual_matching_receipt
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_manual_matching_receipt_retirement();

CREATE FUNCTION public.guard_library_deletion_operation_update() RETURNS trigger
LANGUAGE plpgsql AS $$
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

CREATE TRIGGER library_deletion_operation_update_guard
BEFORE UPDATE ON public.operation
FOR EACH ROW EXECUTE FUNCTION public.guard_library_deletion_operation_update();
