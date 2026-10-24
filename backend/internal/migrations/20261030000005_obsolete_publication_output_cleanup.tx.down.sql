DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.obsolete_publication_output_cleanup) THEN
        RAISE EXCEPTION 'cannot roll back unresolved obsolete publication output cleanup authority';
    END IF;
    IF EXISTS (SELECT 1 FROM public.operation WHERE kind='obsolete_publication_output_cleanup_v1') THEN
        RAISE EXCEPTION 'cannot roll back retained obsolete publication output cleanup operations';
    END IF;
END;
$$;

DROP TABLE public.obsolete_publication_output_cleanup;
ALTER TABLE public.operation DROP CONSTRAINT operation_obsolete_output_cleanup_shape_check;
ALTER TABLE public.operation DROP CONSTRAINT operation_target_identity;
ALTER TABLE public.operation ADD CONSTRAINT operation_target_identity CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1','retry_publication_sources_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    COALESCE(input_snapshot ->> 'target_identity','') <> ''
);
ALTER TABLE public.publication_namespace_claim DROP CONSTRAINT publication_namespace_claim_cleanup_identity_unique;
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
