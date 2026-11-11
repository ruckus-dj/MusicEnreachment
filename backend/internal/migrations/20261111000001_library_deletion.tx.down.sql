DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.manual_matching_receipt
        WHERE retired_by_operation_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back library deletion while retired manual-matching receipts remain';
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.operation
        WHERE kind = 'delete_library_v1'
          AND (state <> 'succeeded' OR jsonb_array_length(input_snapshot->'residual_publication_ids') <> 0)
    ) THEN
        RAISE EXCEPTION 'cannot roll back library deletion while deletion authority is unsettled';
    END IF;
    IF EXISTS (SELECT 1 FROM public.operation WHERE kind = 'delete_library_v1') THEN
        RAISE EXCEPTION 'cannot roll back library deletion while its operation proofs remain';
    END IF;
END;
$$;

DROP TRIGGER manual_matching_receipt_retirement_settled ON public.manual_matching_receipt;
DROP FUNCTION public.check_manual_matching_receipt_retirement();
DROP TRIGGER manual_matching_receipt_retirement_guard ON public.manual_matching_receipt;
DROP FUNCTION public.guard_manual_matching_receipt_retirement();
DROP TRIGGER manual_matching_receipt_delete_write_once ON public.manual_matching_receipt;
DROP TRIGGER library_deletion_operation_update_guard ON public.operation;
DROP FUNCTION public.guard_library_deletion_operation_update();

ALTER TABLE public.manual_matching_receipt
    DROP CONSTRAINT manual_matching_receipt_retirement_shape,
    DROP CONSTRAINT manual_matching_receipt_retired_operation_fkey,
    ALTER COLUMN local_release_id SET NOT NULL,
    DROP COLUMN retired_by_operation_id,
    DROP COLUMN original_local_release_id;

CREATE TRIGGER manual_matching_receipt_write_once
BEFORE DELETE OR UPDATE ON public.manual_matching_receipt
FOR EACH ROW EXECUTE FUNCTION public.reject_manual_matching_receipt_mutation();

ALTER TABLE public.release_track
    DROP CONSTRAINT release_track_release_id_fkey,
    ADD CONSTRAINT release_track_release_id_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE CASCADE;

ALTER TABLE public.release_artist
    DROP CONSTRAINT release_artist_release_id_fkey,
    ADD CONSTRAINT release_artist_release_id_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE CASCADE;

ALTER TABLE public.release_medium
    DROP CONSTRAINT release_medium_release_id_fkey,
    ADD CONSTRAINT release_medium_release_id_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE CASCADE;

ALTER TABLE public.operation
    DROP CONSTRAINT operation_library_deletion_residual_subset,
    DROP CONSTRAINT operation_library_deletion_target_shape,
    DROP CONSTRAINT operation_library_deletion_shape,
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind = ANY (ARRAY[
        'install', 'activate', 'delete', 'move_tools_root', 'scan_source',
        'analyze_source', 'cleanup_source_analysis_artifacts',
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
            'retry_publication_sources_v1','obsolete_publication_output_cleanup_v1','refresh_genre_catalog_v1') OR
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
            'retry_publication_sources_v1','obsolete_publication_output_cleanup_v1','refresh_genre_catalog_v1') OR
        kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
            'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
            'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup',
            'matching_evaluation_v1','matching_apply_v1') OR
        COALESCE(input_snapshot->>'target_identity','') <> ''
    );

DROP FUNCTION public.valid_library_deletion_residual(jsonb, text);
DROP FUNCTION public.valid_library_deletion_uuid_array(jsonb);
