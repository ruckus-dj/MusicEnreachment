DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.operation WHERE kind IN ('publish_track_v1','cleanup_publication_sources_v1'))
       OR EXISTS (SELECT 1 FROM public.track_publication)
       OR EXISTS (SELECT 1 FROM public.track_publication_attempt)
       OR EXISTS (SELECT 1 FROM public.publication_execution_claim)
       OR EXISTS (SELECT 1 FROM public.publication_namespace_claim)
       OR EXISTS (SELECT 1 FROM public.publication_namespace_reservation)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_item WHERE state IN ('queued','claimed','recovery_required','retry_pending')) THEN
        RAISE EXCEPTION 'cannot roll back publication operation contract while publication state is live';
    END IF;
END $$;

DROP TRIGGER publication_operation_owner_cleanup_item ON public.source_cleanup_item;
DROP TRIGGER publication_operation_owner_attempt ON public.track_publication_attempt;
DROP TRIGGER publication_operation_owner_operation ON public.operation;
DROP FUNCTION public.check_publication_operation_owner_trigger();
DROP FUNCTION public.check_publication_operation_owner(uuid);

ALTER TABLE public.operation DROP CONSTRAINT operation_publication_snapshot_check;
ALTER TABLE public.operation DROP CONSTRAINT operation_kind_check;
ALTER TABLE public.operation ADD CONSTRAINT operation_kind_check CHECK (kind = ANY (ARRAY['install','activate','delete','move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts','provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search','provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1']));
ALTER TABLE public.operation DROP CONSTRAINT operation_mutation_has_target_installation;
ALTER TABLE public.operation ADD CONSTRAINT operation_mutation_has_target_installation CHECK (kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts','provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search','provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR state NOT IN ('queued','running') OR target_installation_id IS NOT NULL);
ALTER TABLE public.operation DROP CONSTRAINT operation_target_identity;
ALTER TABLE public.operation ADD CONSTRAINT operation_target_identity CHECK (kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts','provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search','provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR COALESCE(input_snapshot->>'target_identity','') <> '');
ALTER TABLE public.operation DROP CONSTRAINT operation_source_analysis_target_shape;
ALTER TABLE public.operation ADD CONSTRAINT operation_source_analysis_target_shape CHECK (((((source_analysis_mode IS NULL) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (NOT rerun_target) AND ((NOT tools_read_required) OR (kind = 'scan_source'::text))) OR ((source_analysis_mode = 'batch'::text) AND (target_work_id IS NULL) AND (target_step IS NULL) AND (NOT rerun_target)) OR ((source_analysis_mode = 'single_step'::text) AND (((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_work_id IS NOT NULL) AND (target_step = ANY (ARRAY['sha256'::text, 'probe'::text, 'fingerprint'::text, 'metadata'::text]))) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_work_id IS NULL) AND (target_step IS NULL))) AND ((NOT rerun_target) OR ((state = ANY (ARRAY['queued'::text, 'running'::text])) AND (target_step = 'fingerprint'::text)) OR ((state = ANY (ARRAY['succeeded'::text, 'failed'::text])) AND (target_work_id IS NULL) AND (target_step IS NULL))))) IS TRUE));
ALTER TABLE public.publication_namespace_claim DROP CONSTRAINT publication_namespace_claim_owner_scope_check;
ALTER TABLE public.publication_namespace_claim ADD CONSTRAINT publication_namespace_claim_owner_scope_check CHECK ((owner_position_id IS NOT NULL AND directory_scope_key IS NULL) OR (owner_position_id IS NULL AND directory_scope_key IS NOT NULL AND directory_scope_key <> ''));
