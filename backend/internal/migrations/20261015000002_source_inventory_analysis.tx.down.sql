-- A baseline rollback is destructive: reject populated application tables before DDL.
DO $baseline_rollback$
DECLARE
    relation_name text;
    populated boolean;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY['media_fingerprint_result', 'media_metadata_result', 'media_probe_cache', 'media_variant', 'operation_source_work_hold', 'operation_tool_read_hold', 'source_analysis_artifact', 'source_analysis_artifact_cleanup_item', 'source_analysis_step', 'source_analysis_work', 'source_analysis_work_artifact_binding', 'source_analysis_work_execution', 'source_location', 'source_root', 'source_scan_candidate', 'source_work_variant_identity']
    LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I)', relation_name) INTO populated;
        IF populated THEN
            RAISE EXCEPTION 'cannot roll back populated application baseline: % contains data', relation_name;
        END IF;
    END LOOP;
END;
$baseline_rollback$;

DROP TRIGGER source_work_variant_identity_no_rekey ON public.source_work_variant_identity;
DROP TRIGGER source_analysis_work_execution_immutable ON public.source_analysis_work_execution;
DROP TRIGGER source_analysis_work_attachment_guard ON public.source_analysis_work;
DROP TRIGGER source_analysis_step_membership_guard ON public.source_analysis_step;
DROP TRIGGER operation_tool_read_hold_guard ON public.operation_tool_read_hold;
DROP TRIGGER source_analysis_work_hold_requires_current_attachment ON public.operation_source_work_hold;
DROP TRIGGER operation_source_work_hold_guard ON public.operation_source_work_hold;
DROP TRIGGER operation_source_analysis_holds_guard ON public.operation;
DROP TRIGGER media_probe_cache_association_valid ON public.media_probe_cache;
DROP TRIGGER media_fingerprint_result_identity_immutable ON public.media_fingerprint_result;
ALTER TABLE public.source_work_variant_identity DROP CONSTRAINT source_work_variant_identity_work_id_fkey;
ALTER TABLE public.source_work_variant_identity DROP CONSTRAINT source_work_variant_identity_media_variant_id_fkey;
ALTER TABLE public.source_scan_candidate DROP CONSTRAINT source_scan_candidate_operation_id_fkey;
ALTER TABLE public.source_location DROP CONSTRAINT source_location_source_root_id_fkey;
ALTER TABLE public.source_location DROP CONSTRAINT source_location_media_variant_id_fkey;
ALTER TABLE public.source_analysis_work_execution DROP CONSTRAINT source_analysis_work_execution_work_id_fkey;
ALTER TABLE public.source_analysis_work_execution DROP CONSTRAINT source_analysis_work_execution_operation_id_fkey;
ALTER TABLE public.source_analysis_work_artifact_binding DROP CONSTRAINT source_analysis_work_artifact_binding_work_id_fkey;
ALTER TABLE public.source_analysis_work_artifact_binding DROP CONSTRAINT source_analysis_work_artifact_binding_execution_fk;
ALTER TABLE public.source_analysis_work_artifact_binding DROP CONSTRAINT source_analysis_work_artifact_binding_artifact_fk;
ALTER TABLE public.source_analysis_work DROP CONSTRAINT source_analysis_work_current_location_fk;
ALTER TABLE public.source_analysis_step DROP CONSTRAINT source_analysis_step_work_id_fkey;
ALTER TABLE public.source_analysis_step DROP CONSTRAINT source_analysis_step_work_hold_fkey;
ALTER TABLE public.source_analysis_step DROP CONSTRAINT source_analysis_step_success_sha_variant_id_fkey;
ALTER TABLE public.source_analysis_step DROP CONSTRAINT source_analysis_step_success_probe_variant_id_fkey;
ALTER TABLE public.source_analysis_step DROP CONSTRAINT source_analysis_step_success_metadata_result_id_fkey;
ALTER TABLE public.source_analysis_step DROP CONSTRAINT source_analysis_step_success_fingerprint_result_id_fkey;
ALTER TABLE public.source_analysis_step DROP CONSTRAINT source_analysis_step_execution_operation_id_execution_oper_fkey;
ALTER TABLE public.source_analysis_artifact DROP CONSTRAINT source_analysis_artifact_work_id_fkey;
ALTER TABLE public.source_analysis_artifact DROP CONSTRAINT source_analysis_artifact_execution_fkey;
ALTER TABLE public.operation_tool_read_hold DROP CONSTRAINT operation_tool_read_hold_operation_id_fkey;
ALTER TABLE public.operation_tool_read_hold DROP CONSTRAINT operation_tool_read_hold_installation_id_fkey;
ALTER TABLE public.operation_source_work_hold DROP CONSTRAINT operation_source_work_hold_work_id_fkey;
ALTER TABLE public.operation_source_work_hold DROP CONSTRAINT operation_source_work_hold_operation_id_fkey;
ALTER TABLE public.operation DROP CONSTRAINT operation_target_work_id_fkey;
ALTER TABLE public.operation DROP CONSTRAINT operation_target_source_root_id_fkey;
ALTER TABLE public.operation DROP CONSTRAINT operation_target_source_location_id_fkey;
ALTER TABLE public.media_probe_cache DROP CONSTRAINT media_probe_cache_source_sha256_fkey;
ALTER TABLE public.media_probe_cache DROP CONSTRAINT media_probe_cache_result_id_fkey;
ALTER TABLE public.media_fingerprint_result DROP CONSTRAINT media_fingerprint_result_source_sha256_fkey;

DROP TABLE public.source_work_variant_identity;
DROP TABLE public.source_scan_candidate;
DROP TABLE public.source_root;
DROP TABLE public.source_location;
DROP TABLE public.source_analysis_work_execution;
DROP TABLE public.source_analysis_work_artifact_binding;
DROP TABLE public.source_analysis_work;
DROP TABLE public.source_analysis_step;
DROP TABLE public.source_analysis_artifact_cleanup_item;
DROP TABLE public.source_analysis_artifact;
DROP TABLE public.operation_tool_read_hold;
DROP TABLE public.operation_source_work_hold;
DROP TABLE public.media_variant;
DROP TABLE public.media_probe_cache;
DROP TABLE public.media_metadata_result;
DROP TABLE public.media_fingerprint_result;
DROP FUNCTION public.validate_media_probe_cache_association();
DROP FUNCTION public.source_fingerprint_result_identity_immutable();
DROP FUNCTION public.source_analysis_work_hold_requires_current_attachment();
DROP FUNCTION public.source_analysis_work_attachment_guard();
DROP FUNCTION public.source_analysis_immutable_result();
DROP FUNCTION public.reject_manual_picker_search_result_mutation();
DROP FUNCTION public.prevent_source_work_variant_identity_rekey();
DROP FUNCTION public.prevent_source_analysis_work_execution_mutation();
DROP FUNCTION public.check_source_analysis_step_membership();
DROP FUNCTION public.check_source_analysis_operation_holds();
