DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.operation o
        JOIN public.operation_tool_read_hold h ON h.operation_id=o.id
        WHERE o.kind='publication_reconcile_v1'
    ) THEN
        RAISE EXCEPTION 'cannot roll back publication reconciliation tool reads while tool holds remain';
    END IF;
END $$;

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
