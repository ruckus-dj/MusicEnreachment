-- Roll back the fresh-instance application baseline.
--
-- This migration owns only the public application namespace. River and any
-- unrelated tables, functions, sequences, or data in the same database are
-- deliberately left untouched: the drop list is explicit and uses RESTRICT
-- (never CASCADE), so an unexpected external dependency fails the rollback
-- instead of silently deleting another owner's object.
--
-- The unified guard is intentionally stricter than the historical per-feature
-- guards: every application table must be empty, except for the four required
-- seed rows, which must still match their pristine shape. This preserves live
-- or retained recovery authority as a whole without enumerating every
-- historical authority shape. Any modified seed row is also refused, except
-- for the intentionally rotatable publication namespace epoch described below.

DO $baseline_rollback$
DECLARE
    owned_tables text[] := ARRAY[
        'app_setting', 'cover_art_archive_image', 'cover_art_archive_release', 'genre_catalog', 'incoming_group',
        'incoming_group_matching_authority', 'incoming_group_member', 'incoming_group_member_location', 'incoming_group_return_automatic_receipt',
        'incoming_group_unlink_receipt', 'incoming_grouping_state', 'incoming_match_evaluation', 'incoming_match_evaluation_location',
        'incoming_match_evaluation_member', 'incoming_matching_automatic_admission', 'incoming_matching_generation', 'incoming_matching_reconcile_event',
        'library_artist_photo', 'local_artist', 'local_matching_receipt', 'local_recording',
        'local_release', 'local_track', 'manual_matching_admission', 'manual_matching_lookup_seal',
        'manual_matching_receipt', 'manual_picker_search', 'manual_picker_search_result', 'matching_acquisition',
        'matching_acquisition_phase', 'matching_acquisition_seal', 'matching_apply_receipt', 'matching_apply_request',
        'matching_delivery', 'matching_fallback_authority_seal', 'matching_fallback_calculation_receipt', 'matching_fallback_evaluation_handoff',
        'matching_fallback_lookup_receipt', 'matching_fallback_request_registration', 'matching_fallback_search_receipt', 'matching_local_recheck',
        'matching_local_recheck_publication', 'matching_local_recheck_source', 'matching_provider_dependency', 'matching_run',
        'matching_run_retirement_authorization', 'matching_run_target', 'media_fingerprint_result', 'media_metadata_result',
        'media_probe_cache', 'media_variant', 'obsolete_publication_output_cleanup', 'operation',
        'operation_source_work_hold', 'operation_tool_read_hold', 'output_reset_journal', 'position_source_authority',
        'provider', 'provider_artist', 'provider_artist_discography', 'provider_local_artist',
        'provider_local_recording', 'provider_local_release', 'provider_local_track', 'provider_recording',
        'provider_release', 'provider_release_artist_credit', 'provider_release_track', 'provider_response_cache',
        'provider_source', 'provider_source_entity', 'provider_source_release_artist_credit', 'provider_source_release_track',
        'publication_attempt_cover_input', 'publication_automatic_album', 'publication_execution_claim', 'publication_manual_lyrics_receipt',
        'publication_namespace_claim', 'publication_namespace_reservation', 'publication_namespace_state', 'publication_reconciliation_dirty_scope',
        'publication_reconciliation_event', 'publication_reconciliation_request', 'publication_root_abandonment', 'publication_source_cleanup_request',
        'publication_source_hold', 'recording_artist', 'release_artist', 'release_medium',
        'release_publication_cover', 'release_track', 'source_analysis_artifact', 'source_analysis_artifact_cleanup_item',
        'source_analysis_step', 'source_analysis_work', 'source_analysis_work_artifact_binding', 'source_analysis_work_execution',
        'source_cleanup_completion', 'source_cleanup_explicit_retry', 'source_cleanup_explicit_retry_item', 'source_cleanup_explicit_retry_resolution',
        'source_cleanup_frontier_transfer_proof', 'source_cleanup_item', 'source_cleanup_owner', 'source_cleanup_recovery_item',
        'source_cleanup_recovery_state', 'source_cleanup_root_abandonment', 'source_location', 'source_root',
        'source_scan_candidate', 'source_work_variant_identity', 'tool_installation', 'tools_execution_claim',
        'track_artist', 'track_publication', 'track_publication_attempt', 'track_source'
    ];
    relation_name text;
    populated boolean;
BEGIN
    -- Lock every owned table before inspecting data so concurrent writers
    -- cannot insert between a guard check and the DDL that follows. Locks are
    -- acquired in the same lexical order every rollback uses.
    FOREACH relation_name IN ARRAY owned_tables
    LOOP
        EXECUTE format('LOCK TABLE public.%I IN ACCESS EXCLUSIVE MODE', relation_name);
    END LOOP;

    IF EXISTS (
        SELECT 1 FROM public.matching_local_recheck
    ) THEN
        RAISE EXCEPTION 'cannot roll back Local recheck source veto while durable recheck owners remain';
    END IF;

    IF EXISTS (
        SELECT 1 FROM public.genre_catalog
        WHERE singleton IS NOT TRUE
           OR items IS DISTINCT FROM '[]'::jsonb
           OR updated_at IS NOT NULL
           OR last_attempt_at IS NOT NULL
           OR last_error IS NOT NULL
           OR configuration_identity <> ''
           OR refresh_request IS DISTINCT FROM '{"status": "idle"}'::jsonb
           OR refresh_due_at IS NOT NULL
           OR published_configuration_identity IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: genre catalog contains retained state';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.genre_catalog WHERE singleton IS TRUE
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: genre catalog seed is missing';
    END IF;

    IF EXISTS (
        SELECT 1 FROM public.incoming_grouping_state
        WHERE singleton IS NOT TRUE OR revision <> 0 OR needs_refresh IS NOT TRUE
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: grouping state contains retained state';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.incoming_grouping_state WHERE singleton IS TRUE
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: grouping state seed is missing';
    END IF;

    -- A rotated output_epoch is a generated namespace identity, not retained
    -- authority. It cannot be distinguished from the seed without a fixed
    -- initial UUID, so only the singleton shape is checked here. Every table
    -- that consumes the epoch is covered by the empty-table guard below.
    IF EXISTS (
        SELECT 1 FROM public.publication_namespace_state
        WHERE singleton IS NOT TRUE
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: publication namespace state is invalid';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.publication_namespace_state WHERE singleton IS TRUE
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: publication namespace seed is missing';
    END IF;

    IF EXISTS (
        SELECT 1 FROM public.provider
        WHERE id <> '00000000-0000-4000-8000-000000000001'::uuid OR code <> 'local'
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: provider catalog contains retained state';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.provider
        WHERE id = '00000000-0000-4000-8000-000000000001'::uuid AND code = 'local'
    ) THEN
        RAISE EXCEPTION 'cannot roll back application baseline: local provider seed is missing';
    END IF;

    FOREACH relation_name IN ARRAY owned_tables
    LOOP
        IF relation_name IN ('genre_catalog', 'incoming_grouping_state', 'publication_namespace_state', 'provider') THEN
            CONTINUE;
        END IF;
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I)', relation_name) INTO populated;
        IF populated THEN
            RAISE EXCEPTION 'cannot roll back application baseline: % contains data', relation_name;
        END IF;
    END LOOP;
END;
$baseline_rollback$;

-- Drop triggers first so their functions no longer have dependent triggers.
DROP TRIGGER IF EXISTS local_matching_receipt_immutable ON public.local_matching_receipt;
DROP TRIGGER IF EXISTS local_recording_requires_release_track ON public.local_recording;
DROP TRIGGER IF EXISTS local_release_requires_artist ON public.local_release;
DROP TRIGGER IF EXISTS manual_matching_receipt_delete_write_once ON public.manual_matching_receipt;
DROP TRIGGER IF EXISTS manual_matching_receipt_retirement_guard ON public.manual_matching_receipt;
DROP TRIGGER IF EXISTS manual_matching_receipt_retirement_settled ON public.manual_matching_receipt;
DROP TRIGGER IF EXISTS manual_picker_search_result_immutable ON public.manual_picker_search_result;
DROP TRIGGER IF EXISTS matching_acquisition_phase_append_only ON public.matching_acquisition_phase;
DROP TRIGGER IF EXISTS matching_acquisition_seal_write_once ON public.matching_acquisition_seal;
DROP TRIGGER IF EXISTS matching_apply_receipt_input_binding ON public.matching_apply_receipt;
DROP TRIGGER IF EXISTS matching_apply_receipt_write_once ON public.matching_apply_receipt;
DROP TRIGGER IF EXISTS frozen_matching_apply_request_write_once ON public.matching_apply_request;
DROP TRIGGER IF EXISTS matching_fallback_authority_seal_write_once ON public.matching_fallback_authority_seal;
DROP TRIGGER IF EXISTS matching_fallback_calculation_receipt_immutable ON public.matching_fallback_calculation_receipt;
DROP TRIGGER IF EXISTS matching_fallback_evaluation_handoff_write_once ON public.matching_fallback_evaluation_handoff;
DROP TRIGGER IF EXISTS matching_fallback_lookup_receipt_immutable ON public.matching_fallback_lookup_receipt;
DROP TRIGGER IF EXISTS matching_fallback_request_registration_immutable ON public.matching_fallback_request_registration;
DROP TRIGGER IF EXISTS matching_fallback_search_receipt_immutable ON public.matching_fallback_search_receipt;
DROP TRIGGER IF EXISTS matching_local_recheck_incumbent_guard ON public.matching_local_recheck;
DROP TRIGGER IF EXISTS matching_local_recheck_publication_immutable ON public.matching_local_recheck_publication;
DROP TRIGGER IF EXISTS matching_artist_response_guard ON public.matching_provider_dependency;
DROP TRIGGER IF EXISTS matching_run_quiescence_timestamp_immutable ON public.matching_run;
DROP TRIGGER IF EXISTS matching_run_retirement_authorization_immutable ON public.matching_run_retirement_authorization;
DROP TRIGGER IF EXISTS matching_run_retirement_authorization_validate ON public.matching_run_retirement_authorization;
DROP TRIGGER IF EXISTS media_fingerprint_result_identity_immutable ON public.media_fingerprint_result;
DROP TRIGGER IF EXISTS media_probe_cache_association_valid ON public.media_probe_cache;
DROP TRIGGER IF EXISTS library_deletion_operation_update_guard ON public.operation;
DROP TRIGGER IF EXISTS operation_source_analysis_holds_guard ON public.operation;
DROP TRIGGER IF EXISTS publication_operation_owner_operation ON public.operation;
DROP TRIGGER IF EXISTS publication_operation_owner_retry_operation ON public.operation;
DROP TRIGGER IF EXISTS publication_reconciliation_owner_operation ON public.operation;
DROP TRIGGER IF EXISTS operation_source_work_hold_guard ON public.operation_source_work_hold;
DROP TRIGGER IF EXISTS source_analysis_work_hold_requires_current_attachment ON public.operation_source_work_hold;
DROP TRIGGER IF EXISTS operation_tool_read_hold_guard ON public.operation_tool_read_hold;
DROP TRIGGER IF EXISTS publication_attempt_cover_input_immutable ON public.publication_attempt_cover_input;
DROP TRIGGER IF EXISTS publication_reconciliation_event_update ON public.publication_reconciliation_event;
DROP TRIGGER IF EXISTS publication_reconciliation_owner_event ON public.publication_reconciliation_event;
DROP TRIGGER IF EXISTS publication_root_abandonment_immutable ON public.publication_root_abandonment;
DROP TRIGGER IF EXISTS release_artist_preserves_old_parent ON public.release_artist;
DROP TRIGGER IF EXISTS release_artist_prevent_orphaning_truncate ON public.release_artist;
DROP TRIGGER IF EXISTS release_track_ensure_medium ON public.release_track;
DROP TRIGGER IF EXISTS release_track_preserves_old_parent ON public.release_track;
DROP TRIGGER IF EXISTS release_track_prevent_orphaning_truncate ON public.release_track;
DROP TRIGGER IF EXISTS source_analysis_step_membership_guard ON public.source_analysis_step;
DROP TRIGGER IF EXISTS source_analysis_work_attachment_guard ON public.source_analysis_work;
DROP TRIGGER IF EXISTS source_analysis_work_execution_immutable ON public.source_analysis_work_execution;
DROP TRIGGER IF EXISTS source_analysis_work_execution_requires_live_identity ON public.source_analysis_work_execution;
DROP TRIGGER IF EXISTS publication_operation_owner_retry_grant ON public.source_cleanup_explicit_retry;
DROP TRIGGER IF EXISTS source_cleanup_explicit_retry_binding_immutable ON public.source_cleanup_explicit_retry;
DROP TRIGGER IF EXISTS source_cleanup_retry_frontier_immutable ON public.source_cleanup_explicit_retry;
DROP TRIGGER IF EXISTS source_cleanup_retry_grant_frontier_coverage ON public.source_cleanup_explicit_retry;
DROP TRIGGER IF EXISTS publication_operation_owner_retry_item ON public.source_cleanup_explicit_retry_item;
DROP TRIGGER IF EXISTS source_cleanup_explicit_retry_item_immutable ON public.source_cleanup_explicit_retry_item;
DROP TRIGGER IF EXISTS source_cleanup_retry_resolution_immutable ON public.source_cleanup_explicit_retry_resolution;
DROP TRIGGER IF EXISTS source_cleanup_frontier_transfer_proof_canonical ON public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER IF EXISTS source_cleanup_frontier_transfer_proof_consumed ON public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER IF EXISTS publication_operation_owner_cleanup_item ON public.source_cleanup_item;
DROP TRIGGER IF EXISTS source_cleanup_recovery_item_coverage ON public.source_cleanup_recovery_item;
DROP TRIGGER IF EXISTS source_cleanup_recovery_item_protected ON public.source_cleanup_recovery_item;
DROP TRIGGER IF EXISTS source_cleanup_recovery_state_coverage ON public.source_cleanup_recovery_state;
DROP TRIGGER IF EXISTS source_cleanup_recovery_state_protected ON public.source_cleanup_recovery_state;
DROP TRIGGER IF EXISTS source_cleanup_root_abandonment_capture ON public.source_cleanup_root_abandonment;
DROP TRIGGER IF EXISTS source_cleanup_root_abandonment_immutable ON public.source_cleanup_root_abandonment;
DROP TRIGGER IF EXISTS source_cleanup_root_abandonment_retired ON public.source_cleanup_root_abandonment;
DROP TRIGGER IF EXISTS source_work_variant_identity_no_rekey ON public.source_work_variant_identity;
DROP TRIGGER IF EXISTS source_work_variant_identity_requires_live_identity ON public.source_work_variant_identity;
DROP TRIGGER IF EXISTS publication_operation_owner_attempt ON public.track_publication_attempt;

-- Remove only the check constraints that call application functions.
ALTER TABLE public.operation DROP CONSTRAINT IF EXISTS operation_library_deletion_residual_subset;
ALTER TABLE public.operation DROP CONSTRAINT IF EXISTS operation_library_deletion_target_shape;
ALTER TABLE public.source_cleanup_explicit_retry DROP CONSTRAINT IF EXISTS source_cleanup_explicit_retry_binding_contract;

-- Functions are dropped before tables because SQL-language functions may
-- depend on application tables. The explicit list avoids CASCADE.
DROP FUNCTION IF EXISTS
    public.assert_local_recording_has_release_track(p_recording_id uuid), public.assert_local_release_has_artist(p_release_id uuid),
    public.assert_source_cleanup_root_abandonment_retired(p_original_operation_id uuid), public.capture_source_cleanup_root_abandonment(),
    public.check_local_recording_release_track_parent(), public.check_local_release_artist_parent(),
    public.check_manual_matching_receipt_retirement(), public.check_publication_operation_owner(p_operation_id uuid),
    public.check_publication_operation_owner_trigger(), public.check_publication_reconciliation_owner(p_operation_id uuid),
    public.check_publication_reconciliation_owner_trigger(), public.check_release_artist_old_parent(),
    public.check_release_track_old_parent(), public.check_source_analysis_operation_holds(),
    public.check_source_analysis_step_membership(), public.check_source_cleanup_explicit_retry_owner_trigger(),
    public.check_source_cleanup_recovery_coverage(), public.check_source_cleanup_root_abandonment_retired(),
    public.compute_source_cleanup_frontier_transfer_proof(p_original_operation_id uuid, p_recovery_operation_id uuid), public.ensure_release_medium_for_track(),
    public.finish_source_cleanup_frontier_transfer_proof(), public.guard_library_deletion_operation_update(),
    public.guard_manual_matching_receipt_retirement(), public.guard_matching_artist_response(),
    public.guard_matching_local_recheck_incumbent(), public.guard_matching_local_recheck_publication(),
    public.guard_publication_reconciliation_event_update(), public.matching_run_retirement_is_authorized(target_run uuid),
    public.prevent_release_artist_truncate_orphans(), public.prevent_release_track_truncate_orphans(),
    public.prevent_source_analysis_work_execution_mutation(), public.prevent_source_work_variant_identity_rekey(),
    public.protect_matching_run_quiescence_timestamp(), public.protect_source_cleanup_explicit_retry_binding(),
    public.protect_source_cleanup_explicit_retry_item(), public.protect_source_cleanup_recovery_item(),
    public.protect_source_cleanup_recovery_state(), public.protect_source_cleanup_retry_frontier(),
    public.protect_source_cleanup_retry_resolution(), public.reject_frozen_matching_apply_request_mutation(),
    public.reject_local_matching_receipt_mutation(), public.reject_manual_matching_receipt_mutation(),
    public.reject_manual_picker_search_result_mutation(), public.reject_matching_acquisition_phase_mutation(),
    public.reject_matching_acquisition_seal_mutation(), public.reject_matching_apply_receipt_mutation(),
    public.reject_matching_fallback_authority_seal_mutation(), public.reject_matching_fallback_calculation_receipt_mutation(),
    public.reject_matching_fallback_evaluation_handoff_mutation(), public.reject_matching_fallback_lookup_receipt_mutation(),
    public.reject_matching_fallback_request_registration_mutation(), public.reject_matching_fallback_search_receipt_mutation(),
    public.reject_matching_run_retirement_authorization_mutation(), public.reject_publication_attempt_cover_input_mutation(),
    public.reject_publication_root_abandonment_mutation(), public.reject_source_cleanup_root_abandonment_mutation(),
    public.source_analysis_immutable_result(), public.source_analysis_work_attachment_guard(),
    public.source_analysis_work_execution_requires_live_identity(), public.source_analysis_work_hold_requires_current_attachment(),
    public.source_cleanup_frontier_transfer_proof(p_original_operation_id uuid, p_recovery_operation_id uuid), public.source_fingerprint_result_identity_immutable(),
    public.source_work_variant_identity_requires_live_identity(), public.valid_library_deletion_residual(snapshot jsonb, operation_state text),
    public.valid_library_deletion_uuid_array(values_json jsonb), public.valid_source_cleanup_explicit_retry_binding(b jsonb, op_id uuid, app_attempt integer, job_id bigint, claim_id uuid, pos_id uuid, retry_epoch uuid, generation uuid),
    public.validate_matching_apply_receipt_input_binding(), public.validate_matching_run_retirement_authorization(),
    public.validate_media_probe_cache_association(), public.validate_source_cleanup_frontier_transfer_proof() RESTRICT;

-- One RESTRICT drop resolves foreign keys among the listed tables while
-- refusing to remove or cascade into anything outside this baseline.
DROP TABLE IF EXISTS
    public.app_setting, public.cover_art_archive_image, public.cover_art_archive_release, public.genre_catalog, public.incoming_group,
    public.incoming_group_matching_authority, public.incoming_group_member, public.incoming_group_member_location, public.incoming_group_return_automatic_receipt,
    public.incoming_group_unlink_receipt, public.incoming_grouping_state, public.incoming_match_evaluation, public.incoming_match_evaluation_location,
    public.incoming_match_evaluation_member, public.incoming_matching_automatic_admission, public.incoming_matching_generation, public.incoming_matching_reconcile_event,
    public.library_artist_photo, public.local_artist, public.local_matching_receipt, public.local_recording,
    public.local_release, public.local_track, public.manual_matching_admission, public.manual_matching_lookup_seal,
    public.manual_matching_receipt, public.manual_picker_search, public.manual_picker_search_result, public.matching_acquisition,
    public.matching_acquisition_phase, public.matching_acquisition_seal, public.matching_apply_receipt, public.matching_apply_request,
    public.matching_delivery, public.matching_fallback_authority_seal, public.matching_fallback_calculation_receipt, public.matching_fallback_evaluation_handoff,
    public.matching_fallback_lookup_receipt, public.matching_fallback_request_registration, public.matching_fallback_search_receipt, public.matching_local_recheck,
    public.matching_local_recheck_publication, public.matching_local_recheck_source, public.matching_provider_dependency, public.matching_run,
    public.matching_run_retirement_authorization, public.matching_run_target, public.media_fingerprint_result, public.media_metadata_result,
    public.media_probe_cache, public.media_variant, public.obsolete_publication_output_cleanup, public.operation,
    public.operation_source_work_hold, public.operation_tool_read_hold, public.output_reset_journal, public.position_source_authority,
    public.provider, public.provider_artist, public.provider_artist_discography, public.provider_local_artist,
    public.provider_local_recording, public.provider_local_release, public.provider_local_track, public.provider_recording,
    public.provider_release, public.provider_release_artist_credit, public.provider_release_track, public.provider_response_cache,
    public.provider_source, public.provider_source_entity, public.provider_source_release_artist_credit, public.provider_source_release_track,
    public.publication_attempt_cover_input, public.publication_automatic_album, public.publication_execution_claim, public.publication_manual_lyrics_receipt,
    public.publication_namespace_claim, public.publication_namespace_reservation, public.publication_namespace_state, public.publication_reconciliation_dirty_scope,
    public.publication_reconciliation_event, public.publication_reconciliation_request, public.publication_root_abandonment, public.publication_source_cleanup_request,
    public.publication_source_hold, public.recording_artist, public.release_artist, public.release_medium,
    public.release_publication_cover, public.release_track, public.source_analysis_artifact, public.source_analysis_artifact_cleanup_item,
    public.source_analysis_step, public.source_analysis_work, public.source_analysis_work_artifact_binding, public.source_analysis_work_execution,
    public.source_cleanup_completion, public.source_cleanup_explicit_retry, public.source_cleanup_explicit_retry_item, public.source_cleanup_explicit_retry_resolution,
    public.source_cleanup_frontier_transfer_proof, public.source_cleanup_item, public.source_cleanup_owner, public.source_cleanup_recovery_item,
    public.source_cleanup_recovery_state, public.source_cleanup_root_abandonment, public.source_location, public.source_root,
    public.source_scan_candidate, public.source_work_variant_identity, public.tool_installation, public.tools_execution_claim,
    public.track_artist, public.track_publication, public.track_publication_attempt, public.track_source RESTRICT;

DROP SEQUENCE IF EXISTS public.matching_delivery_id_seq;
DROP SEQUENCE IF EXISTS public.provider_fetch_order_seq;
