-- Squashed additive rollback, preserving original reverse dependency order.

-- A baseline rollback is destructive: reject retained provider state before DDL.
-- The provider baseline rollback guard runs only after this migration's schema is
-- dropped, so refuse the same non-pristine catalog here to keep the group atomic.
DO $guard$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.genre_catalog
        WHERE items IS DISTINCT FROM '[]'::jsonb
           OR updated_at IS NOT NULL
           OR last_attempt_at IS NOT NULL
           OR last_error IS NOT NULL
           OR configuration_identity <> ''
           OR refresh_request IS DISTINCT FROM '{"status":"idle"}'::jsonb
           OR refresh_due_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back incoming matching baseline: genre catalog contains retained state';
    END IF;
    IF EXISTS (SELECT 1 FROM public.publication_root_abandonment) THEN
        RAISE EXCEPTION 'cannot roll back incoming matching baseline: publication root abandonment evidence exists';
    END IF;
    IF EXISTS (SELECT 1 FROM public.source_cleanup_root_abandonment) THEN
        RAISE EXCEPTION 'cannot roll back incoming matching baseline: source cleanup root abandonment evidence exists';
    END IF;
END;
$guard$;

DROP TRIGGER source_cleanup_root_abandonment_retired ON public.source_cleanup_root_abandonment;
DROP FUNCTION public.check_source_cleanup_root_abandonment_retired();
DROP FUNCTION public.assert_source_cleanup_root_abandonment_retired(uuid);
DROP TRIGGER source_cleanup_root_abandonment_capture ON public.source_cleanup_root_abandonment;
DROP TRIGGER source_cleanup_root_abandonment_immutable ON public.source_cleanup_root_abandonment;
DROP FUNCTION public.capture_source_cleanup_root_abandonment();
DROP FUNCTION public.reject_source_cleanup_root_abandonment_mutation();
DROP TABLE public.source_cleanup_root_abandonment;

DROP TRIGGER publication_root_abandonment_immutable ON public.publication_root_abandonment;
DROP FUNCTION public.reject_publication_root_abandonment_mutation();
DROP TABLE public.publication_root_abandonment;

-- Former migration: 20261101000001_release_media_and_manual_credits.tx.down.sql
DO $guard$
BEGIN
    IF EXISTS (
        SELECT 1 FROM release_artist WHERE role <> ''
        UNION ALL SELECT 1 FROM track_artist WHERE role <> ''
        UNION ALL SELECT 1 FROM recording_artist WHERE role <> ''
        UNION ALL SELECT 1 FROM release_track WHERE duration_ms IS NOT NULL
        UNION ALL SELECT 1 FROM release_medium WHERE title <> '' OR format <> ''
        UNION ALL SELECT 1 FROM release_medium medium
            WHERE NOT EXISTS (
                SELECT 1 FROM release_track track
                WHERE track.release_id = medium.release_id AND track.medium = medium.medium
            )
    ) THEN
        RAISE EXCEPTION 'cannot roll back release media and manual credits: populated additive fields would be lost';
    END IF;
END;
$guard$;

DROP TRIGGER release_track_ensure_medium ON release_track;
DROP FUNCTION ensure_release_medium_for_track();
ALTER TABLE release_track DROP CONSTRAINT release_track_medium_fk;
ALTER TABLE release_track DROP COLUMN duration_ms;
ALTER TABLE recording_artist DROP COLUMN role;
ALTER TABLE track_artist DROP COLUMN role;
ALTER TABLE release_artist DROP COLUMN role;
DROP TABLE release_medium;

-- Former migration: 20261031000001_canonical_tool_versions.tx.down.sql
DO $migration$
DECLARE
    attempt record;
    installation record;
    selection jsonb;
    restored_tools jsonb;
    executable_name text;
    raw_output text;
    token text;
    ffmpeg_token text;
    ffprobe_token text;
    logical_executable text;
    banner_fields text[];
BEGIN
    -- Restore the former token-valued selection only from the retained raw
    -- verification output and its exact frozen installation/path identity.
    FOR attempt IN SELECT operation_id, frozen_inputs FROM public.track_publication_attempt
        WHERE frozen_inputs IS NOT NULL LOOP
        IF jsonb_typeof(attempt.frozen_inputs -> 'tools') IS DISTINCT FROM 'array' THEN
            RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: tools array is missing', attempt.operation_id;
        END IF;
        restored_tools := '[]'::jsonb;
        FOR selection IN SELECT value FROM jsonb_array_elements(attempt.frozen_inputs -> 'tools') LOOP
            IF jsonb_typeof(selection) IS DISTINCT FROM 'object' OR
               jsonb_typeof(selection -> 'package_kind') IS DISTINCT FROM 'string' OR selection ->> 'package_kind' = '' THEN
                RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: selection package kind is missing', attempt.operation_id;
            END IF;
            IF selection ->> 'package_kind' <> 'ffmpeg' THEN
                restored_tools := restored_tools || jsonb_build_array(selection);
                CONTINUE;
            END IF;
            IF jsonb_typeof(selection -> 'installation_id') IS DISTINCT FROM 'string' OR selection ->> 'installation_id' = '' OR
               jsonb_typeof(selection -> 'relative_path') IS DISTINCT FROM 'string' OR selection ->> 'relative_path' = '' OR
               jsonb_typeof(selection -> 'version') IS DISTINCT FROM 'string' OR selection ->> 'version' = '' OR
               jsonb_typeof(selection -> 'version_banner') IS DISTINCT FROM 'string' OR selection ->> 'version_banner' = '' THEN
                RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: frozen selection identity and evidence must be nonempty strings', attempt.operation_id;
            END IF;
            SELECT i.version, i.relative_path, i.executable_verification_output, i.platform_goos
              INTO installation
              FROM public.tool_installation i
             WHERE i.id::text = selection ->> 'installation_id' AND i.package_kind = 'ffmpeg';
            IF NOT FOUND OR installation.relative_path IS DISTINCT FROM selection ->> 'relative_path' THEN
                RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: installation identity or path differs', attempt.operation_id;
            END IF;
            executable_name := selection ->> 'executable';
            IF executable_name NOT IN ('ffmpeg', 'ffprobe') THEN
                RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: unexpected executable', attempt.operation_id;
            END IF;
            IF installation.platform_goos = 'windows' THEN executable_name := executable_name || '.exe'; END IF;
            IF jsonb_typeof(installation.executable_verification_output -> executable_name) IS DISTINCT FROM 'string' THEN
                RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: raw executable evidence is missing', attempt.operation_id;
            END IF;
            raw_output := installation.executable_verification_output ->> executable_name;
            logical_executable := selection ->> 'executable';
            banner_fields := regexp_split_to_array(regexp_replace(split_part(raw_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(banner_fields, 1), 0) < 3 OR lower(banner_fields[1]) <> logical_executable OR lower(banner_fields[2]) <> 'version' THEN
                RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: malformed raw executable banner', attempt.operation_id;
            END IF;
            token := banner_fields[3];
            ffmpeg_token := (regexp_match(installation.version, '^ffmpeg ([^ ]+) \+ ffprobe ([^ ]+)$'))[1];
            ffprobe_token := (regexp_match(installation.version, '^ffmpeg ([^ ]+) \+ ffprobe ([^ ]+)$'))[2];
            IF token IS NULL OR regexp_replace(raw_output, $trim$^\s+|\s+$$trim$, '', 'g') IS DISTINCT FROM selection ->> 'version_banner' OR
               selection ->> 'version' IS DISTINCT FROM installation.version OR
               (selection ->> 'executable' = 'ffmpeg' AND token IS DISTINCT FROM ffmpeg_token) OR
               (selection ->> 'executable' = 'ffprobe' AND token IS DISTINCT FROM ffprobe_token) OR
               installation.version IS DISTINCT FROM ('ffmpeg ' || ffmpeg_token || ' + ffprobe ' || ffprobe_token) THEN
                RAISE EXCEPTION 'cannot restore retained publication tool authority for operation %: canonical selection is not proven by raw installation evidence', attempt.operation_id;
            END IF;
            restored_tools := restored_tools || jsonb_build_array(jsonb_set(selection, '{version}', to_jsonb(token), false));
        END LOOP;
        UPDATE public.track_publication_attempt
           SET frozen_inputs = jsonb_set(frozen_inputs, '{tools}', restored_tools, false)
         WHERE operation_id = attempt.operation_id;
    END LOOP;
END $migration$;

ALTER TABLE public.tool_installation DROP CONSTRAINT tool_installation_ready_version_check;
ALTER TABLE public.tool_installation DROP COLUMN version;
ALTER TABLE public.tool_installation RENAME COLUMN executable_verification_output TO executable_versions;

-- Former migration: 20261030000007_retire_publication_mutation_journal.tx.down.sql
LOCK TABLE public.track_publication_attempt IN ACCESS EXCLUSIVE MODE;

ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_frozen_inputs_phase_check,
    DROP CONSTRAINT track_publication_attempt_phase_check,
    ADD COLUMN mutation_journal jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE public.track_publication_attempt
    ALTER COLUMN mutation_journal DROP DEFAULT,
    ADD CONSTRAINT track_publication_attempt_phase_check CHECK (phase = ANY (ARRAY['queued', 'preparing', 'staged', 'validated', 'exposing', 'committed', 'cleaning', 'cleanup_pending', 'recovery_required'])),
    ADD CONSTRAINT track_publication_attempt_frozen_inputs_phase_check CHECK (
        (phase IN ('queued', 'preparing', 'staged', 'validated', 'exposing', 'recovery_required')
            AND frozen_inputs IS NOT NULL AND jsonb_typeof(frozen_inputs) = 'object')
        OR (phase IN ('committed', 'cleaning', 'cleanup_pending') AND frozen_inputs IS NULL)
    ),
    ADD CONSTRAINT track_publication_attempt_mutation_journal_array_check CHECK (jsonb_typeof(mutation_journal) = 'array');

-- Former migration: 20261030000006_publication_reconciliation_tool_reads.tx.down.sql
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

-- Former migration: 20261030000005_obsolete_publication_output_cleanup.tx.down.sql
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
ALTER TABLE public.operation DROP CONSTRAINT IF EXISTS operation_publication_snapshot_check;
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

-- Former migration: 20261030000004_preserve_original_cover_facts.tx.down.sql
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.release_publication_cover
        WHERE selected_mime IS NOT NULL AND selected_mime NOT IN ('image/png', 'image/jpeg')
           OR pending_mime IS NOT NULL AND pending_mime NOT IN ('image/png', 'image/jpeg')
    ) OR EXISTS (
        SELECT 1 FROM public.publication_attempt_cover_input
        WHERE image_mime IS NOT NULL AND image_mime NOT IN ('image/png', 'image/jpeg')
    ) OR EXISTS (
        SELECT 1 FROM public.track_publication WHERE cover_embedding->>'version' = '2'
    ) OR EXISTS (
        SELECT 1 FROM public.media_metadata_result WHERE cover_error IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'refusing to remove retained original cover facts or derivative binding';
    END IF;
END;
$$;

ALTER TABLE public.track_publication
    DROP CONSTRAINT track_publication_cover_embedding_projection_check;
ALTER TABLE public.media_metadata_result
    DROP CONSTRAINT media_metadata_result_cover_error_check,
    DROP COLUMN cover_error;

ALTER TABLE public.release_publication_cover
    DROP CONSTRAINT release_publication_cover_selected_tuple_check,
    DROP CONSTRAINT release_publication_cover_pending_tuple_check,
    ADD CONSTRAINT release_publication_cover_selected_tuple_check CHECK ((
        (selected_generation IS NULL AND selected_bytes IS NULL AND selected_mime IS NULL AND selected_sha256 IS NULL AND selected_size IS NULL)
        OR (selected_generation IS NOT NULL AND selected_generation <> '00000000-0000-0000-0000-000000000000'
            AND selected_bytes IS NOT NULL AND octet_length(selected_bytes) > 0
            AND selected_mime IN ('image/png', 'image/jpeg') AND selected_sha256 IS NOT NULL AND octet_length(selected_sha256) = 32
            AND selected_sha256 = sha256(selected_bytes) AND selected_size = octet_length(selected_bytes) AND selected_size > 0)
    ) IS TRUE),
    ADD CONSTRAINT release_publication_cover_pending_tuple_check CHECK ((
        (pending_generation IS NULL AND pending_bytes IS NULL AND pending_mime IS NULL AND pending_sha256 IS NULL AND pending_size IS NULL)
        OR (pending_generation IS NOT NULL AND pending_generation <> '00000000-0000-0000-0000-000000000000'
            AND pending_bytes IS NOT NULL AND octet_length(pending_bytes) > 0
            AND pending_mime IN ('image/png', 'image/jpeg') AND pending_sha256 IS NOT NULL AND octet_length(pending_sha256) = 32
            AND pending_sha256 = sha256(pending_bytes) AND pending_size = octet_length(pending_bytes) AND pending_size > 0)
    ) IS TRUE);

ALTER TABLE public.publication_attempt_cover_input
    DROP CONSTRAINT publication_attempt_cover_input_tuple_check,
    ADD CONSTRAINT publication_attempt_cover_input_tuple_check CHECK ((
        (state = 'absent' AND generation IS NULL AND image_bytes IS NULL AND image_mime IS NULL AND image_sha256 IS NULL AND image_size IS NULL)
        OR (state = 'value' AND generation IS NOT NULL AND generation <> '00000000-0000-0000-0000-000000000000'
            AND image_bytes IS NOT NULL AND octet_length(image_bytes) > 0 AND image_mime IN ('image/png', 'image/jpeg')
            AND image_sha256 IS NOT NULL AND octet_length(image_sha256) = 32 AND image_sha256 = sha256(image_bytes)
            AND image_size = octet_length(image_bytes) AND image_size > 0)
    ) IS TRUE);

ALTER TABLE public.track_publication
    ADD CONSTRAINT track_publication_cover_embedding_projection_check
    CHECK (cover_embedding IS NULL OR (
        (jsonb_typeof(cover_embedding) = 'object') IS TRUE
        AND (cover_embedding ?& ARRAY['version', 'generation', 'receipt_operation_id', 'receipt_release_id', 'receipt_revision', 'receipt_mode', 'requested_generation', 'requested_sha256', 'outcome', 'warning_code']) IS TRUE
        AND (jsonb_typeof(cover_embedding->'version') = 'number') IS TRUE
        AND ((cover_embedding->>'version') = '1') IS TRUE
        AND (jsonb_typeof(cover_embedding->'generation') = 'string') IS TRUE
        AND ((cover_embedding->>'generation') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
        AND (jsonb_typeof(cover_embedding->'receipt_operation_id') = 'string') IS TRUE
        AND ((cover_embedding->>'receipt_operation_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
        AND (jsonb_typeof(cover_embedding->'receipt_release_id') = 'string') IS TRUE
        AND ((cover_embedding->>'receipt_release_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
        AND (jsonb_typeof(cover_embedding->'receipt_revision') = 'number') IS TRUE
        AND ((cover_embedding->>'receipt_revision') ~ '^[1-9][0-9]*$') IS TRUE
        AND (jsonb_typeof(cover_embedding->'receipt_mode') = 'string') IS TRUE
        AND ((cover_embedding->>'receipt_mode') IN ('automatic', 'manual')) IS TRUE
        AND (jsonb_typeof(cover_embedding->'requested_generation') = 'string') IS TRUE
        AND ((cover_embedding->>'requested_generation') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') IS TRUE
        AND (jsonb_typeof(cover_embedding->'requested_sha256') = 'string') IS TRUE
        AND ((cover_embedding->>'requested_sha256') ~ '^[0-9a-f]{64}$') IS TRUE
        AND (jsonb_typeof(cover_embedding->'outcome') = 'string') IS TRUE
        AND (jsonb_typeof(cover_embedding->'warning_code') = 'string') IS TRUE
        AND (
            ((cover_embedding->>'outcome') = 'verified' AND (cover_embedding->>'warning_code') = '')
            OR ((cover_embedding->>'outcome') = 'write_failed' AND (cover_embedding->>'warning_code') = 'cover_write_failed')
            OR ((cover_embedding->>'outcome') = 'read_failed' AND (cover_embedding->>'warning_code') = 'cover_read_failed')
            OR ((cover_embedding->>'outcome') = 'mismatch' AND (cover_embedding->>'warning_code') = 'cover_mismatch')
        ) IS TRUE
    ));

-- Former migration: 20261030000003_source_metadata_media_facts.tx.down.sql
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.media_metadata_result
        WHERE cover_observed OR cover_data IS NOT NULL OR properties IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM public.track_source WHERE source_metadata_result_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'refusing to remove populated source metadata media facts or references';
    END IF;
END;
$$;

DROP INDEX public.track_source_source_metadata_result_idx;
ALTER TABLE public.track_source
    DROP CONSTRAINT track_source_source_metadata_result_fk,
    DROP COLUMN source_metadata_result_id;
ALTER TABLE public.media_metadata_result
    DROP CONSTRAINT media_metadata_result_cover_facts_check,
    DROP COLUMN cover_observed,
    DROP COLUMN cover_data,
    DROP COLUMN cover_mime,
    DROP COLUMN cover_sha256,
    DROP COLUMN cover_size_bytes,
    DROP COLUMN properties;

-- Former migration: 20261030000002_publication_cover_embedding.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.track_publication WHERE cover_embedding IS NOT NULL) THEN
        RAISE EXCEPTION 'refusing to remove populated current publication cover embedding summaries';
    END IF;
END;
$$;

ALTER TABLE public.track_publication
    DROP CONSTRAINT track_publication_cover_embedding_projection_check,
    DROP COLUMN cover_embedding;

-- Former migration: 20261030000001_failed_publication_tool_holds.tx.down.sql
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.operation o
        JOIN public.operation_tool_read_hold h ON h.operation_id=o.id
        WHERE o.kind='publish_track_v1' AND o.state='failed'
    ) THEN
        RAISE EXCEPTION 'cannot roll back failed-publication tool-hold support while failed publications retain tool holds';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION public.check_source_analysis_operation_holds() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    operation_row operation%ROWTYPE;
    snapshot jsonb;
    affected_operation_id uuid;
    work_hold_count bigint;
    tool_hold_count bigint;
    has_execution boolean;
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
        IF work_hold_count <> 0 OR has_execution OR
           (operation_row.state NOT IN ('queued', 'running') AND tool_hold_count <> 0) THEN
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

-- Former migration: 20261029000003_publication_attempt_cover_input.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_attempt_cover_input) THEN
        RAISE EXCEPTION 'refusing to drop populated publication_attempt_cover_input';
    END IF;
END
$$;

DROP TRIGGER publication_attempt_cover_input_immutable ON public.publication_attempt_cover_input;
DROP FUNCTION public.reject_publication_attempt_cover_input_mutation();
DROP TABLE public.publication_attempt_cover_input;
ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_operation_position_key;
ALTER TABLE public.release_track
    DROP CONSTRAINT release_track_id_release_key;

-- Former migration: 20261029000002_position_source_authority.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM position_source_authority)
       OR EXISTS (SELECT 1 FROM track_publication_attempt WHERE frozen_inputs->>'source_authority_mode' IN ('manual', 'automatic')) THEN
        RAISE EXCEPTION 'refusing to discard live position source authority';
    END IF;
END $$;
DROP TABLE position_source_authority;

-- Former migration: 20261029000001_release_cover_authority.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.release_publication_cover) THEN
        RAISE EXCEPTION 'refusing to discard current release cover authority or an active acquisition';
    END IF;
END
$$;

DROP TABLE public.release_publication_cover;

-- Former migration: 20261028000001_current_source_cleanup_request.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM publication_source_cleanup_request) THEN
        RAISE EXCEPTION 'refusing to drop populated current source cleanup requests';
    END IF;
END $$;

DROP TABLE publication_source_cleanup_request;

-- Former migration: 20261027000001_source_cleanup_completion.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_completion) THEN
        RAISE EXCEPTION 'cannot roll back current source cleanup completion markers';
    END IF;
END $$;
DROP TABLE public.source_cleanup_completion;

-- Former migration: 20261026000001_publication_source_cleanup_frontier.tx.down.sql
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry WHERE frontier_revision IS NOT NULL)
     OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state)
     OR EXISTS (SELECT 1 FROM public.source_cleanup_frontier_transfer_proof) THEN
    RAISE EXCEPTION 'cannot roll back pinned source cleanup recovery frontier';
  END IF;
END $$;

CREATE OR REPLACE FUNCTION public.protect_source_cleanup_retry_resolution() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE retry_state text;
BEGIN
    IF TG_OP='DELETE' THEN
        SELECT operation.state INTO retry_state
        FROM public.source_cleanup_explicit_retry grant_row
        JOIN public.operation ON operation.id=grant_row.recovery_operation_id
        WHERE grant_row.id=OLD.retry_id;
        IF OLD.completed_at IS NULL OR OLD.resolution_kind IS NULL OR retry_state IS NULL OR retry_state IN ('queued','running') THEN
            RAISE EXCEPTION 'active source cleanup retry resolution start is immutable' USING ERRCODE='23514';
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

DROP TRIGGER source_cleanup_frontier_transfer_proof_consumed ON public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER source_cleanup_frontier_transfer_proof_canonical ON public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER source_cleanup_retry_grant_frontier_coverage ON public.source_cleanup_explicit_retry;
DROP TRIGGER source_cleanup_recovery_item_coverage ON public.source_cleanup_recovery_item;
DROP TRIGGER source_cleanup_recovery_state_coverage ON public.source_cleanup_recovery_state;
DROP FUNCTION public.finish_source_cleanup_frontier_transfer_proof();
DROP FUNCTION public.source_cleanup_frontier_transfer_proof(uuid,uuid);
DROP FUNCTION public.validate_source_cleanup_frontier_transfer_proof();
DROP FUNCTION public.compute_source_cleanup_frontier_transfer_proof(uuid,uuid);
DROP TABLE public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER source_cleanup_retry_frontier_immutable ON public.source_cleanup_explicit_retry;
DROP FUNCTION public.protect_source_cleanup_retry_frontier();
ALTER TABLE public.source_cleanup_explicit_retry DROP CONSTRAINT source_cleanup_retry_frontier_revision_fkey;
ALTER TABLE public.source_cleanup_explicit_retry DROP COLUMN frontier_revision;
DROP FUNCTION public.check_source_cleanup_recovery_coverage();
DROP TRIGGER source_cleanup_recovery_item_protected ON public.source_cleanup_recovery_item;
DROP FUNCTION public.protect_source_cleanup_recovery_item();
DROP TRIGGER source_cleanup_recovery_state_protected ON public.source_cleanup_recovery_state;
DROP FUNCTION public.protect_source_cleanup_recovery_state();
DROP TABLE public.source_cleanup_recovery_item;
DROP TABLE public.source_cleanup_recovery_state;

-- Former migration: 20261025000002_publication_source_cleanup_retry_resolution.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry_resolution) THEN
        RAISE EXCEPTION 'cannot roll back explicit source cleanup retry resolution while resolution authority exists';
    END IF;
END $$;

DROP TRIGGER source_cleanup_retry_resolution_immutable ON public.source_cleanup_explicit_retry_resolution;
DROP FUNCTION public.protect_source_cleanup_retry_resolution();
DROP TABLE public.source_cleanup_explicit_retry_resolution;

-- Former migration: 20261025000001_publication_source_cleanup_explicit_retry.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry)
       OR EXISTS (SELECT 1 FROM public.operation WHERE kind='retry_publication_sources_v1') THEN
        RAISE EXCEPTION 'cannot roll back explicit source cleanup retry while grants or operations exist';
    END IF;
END $$;

DROP TRIGGER source_cleanup_explicit_retry_item_immutable ON public.source_cleanup_explicit_retry_item;
DROP FUNCTION public.protect_source_cleanup_explicit_retry_item();
DROP TRIGGER source_cleanup_explicit_retry_binding_immutable ON public.source_cleanup_explicit_retry;
DROP FUNCTION public.protect_source_cleanup_explicit_retry_binding();
DROP TRIGGER publication_operation_owner_retry_item ON public.source_cleanup_explicit_retry_item;
DROP TRIGGER publication_operation_owner_retry_grant ON public.source_cleanup_explicit_retry;
DROP TRIGGER publication_operation_owner_retry_operation ON public.operation;
DROP FUNCTION public.check_source_cleanup_explicit_retry_owner_trigger();
ALTER TABLE public.source_cleanup_explicit_retry
    DROP CONSTRAINT source_cleanup_explicit_retry_original_claim_fkey;
DROP TABLE public.source_cleanup_explicit_retry_item;
DROP TABLE public.source_cleanup_explicit_retry;
DROP FUNCTION public.valid_source_cleanup_explicit_retry_binding(jsonb, uuid, integer, bigint, uuid, uuid, uuid, uuid);
CREATE OR REPLACE FUNCTION public.check_publication_operation_owner(p_operation_id uuid) RETURNS void
LANGUAGE plpgsql AS $$
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
END $$;
ALTER TABLE public.publication_execution_claim DROP CONSTRAINT publication_execution_claim_delivery_identity_key;
ALTER TABLE public.source_cleanup_owner DROP CONSTRAINT source_cleanup_owner_item_owner_key;

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
    (kind IN ('publish_track_v1','cleanup_publication_sources_v1','publication_reconcile_v1') AND source_analysis_mode IS NULL
        AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
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
ALTER TABLE public.operation ADD CONSTRAINT operation_publication_snapshot_check CHECK (
    kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1') OR
    (jsonb_typeof(input_snapshot) = 'object'
     AND input_snapshot - ARRAY['version','position_id','output_epoch','intent_kind'] = '{}'::jsonb
     AND input_snapshot ?& ARRAY['version','position_id','output_epoch']
     AND jsonb_typeof(input_snapshot->'version')='number' AND input_snapshot->>'version'='1'
     AND jsonb_typeof(input_snapshot->'position_id')='string'
     AND (input_snapshot->>'position_id') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND jsonb_typeof(input_snapshot->'output_epoch')='string'
     AND (input_snapshot->>'output_epoch') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     AND ((kind='publish_track_v1' AND input_snapshot ? 'intent_kind'
           AND jsonb_typeof(input_snapshot->'intent_kind')='string'
           AND input_snapshot->>'intent_kind' IN ('audio','rewrite','delete','reorganize'))
       OR (kind='cleanup_publication_sources_v1' AND NOT (input_snapshot ? 'intent_kind'))))
);

ALTER TABLE public.publication_namespace_claim DROP CONSTRAINT publication_namespace_claim_owner_scope_check;
ALTER TABLE public.publication_namespace_claim ADD CONSTRAINT publication_namespace_claim_owner_scope_check CHECK (
    (node_kind='file' AND owner_position_id IS NOT NULL AND directory_scope_key IS NULL)
    OR (node_kind='dir' AND owner_position_id IS NULL AND directory_scope_key IS NOT NULL AND directory_scope_key <> '')
);

ALTER TABLE public.source_cleanup_item
    DROP CONSTRAINT source_cleanup_item_mutation_claim_fkey;

ALTER TABLE public.publication_execution_claim
    DROP CONSTRAINT publication_execution_claim_operation_claim_key;

-- Former migration: 20261023000001_source_cleanup_owner.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.source_cleanup_owner) THEN
        RAISE EXCEPTION 'cannot roll back source cleanup owner schema while owner rows exist';
    END IF;
END $$;

DROP TABLE public.source_cleanup_owner;
ALTER TABLE public.source_cleanup_item
    DROP CONSTRAINT source_cleanup_item_id_operation_key;

-- Former migration: 20261022000001_initial_lyrics_projection.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM track_publication WHERE initial_lyrics IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back while current publication lyrics evidence exists';
    END IF;
END $$;

ALTER TABLE track_publication
    DROP CONSTRAINT track_publication_initial_lyrics_object,
    DROP COLUMN initial_lyrics;

-- Former migration: 20261021000001_publication_reconciliation_parking.tx.down.sql
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_reconciliation_request
        WHERE evaluated_generation IS NOT NULL OR parked_reason IS NOT NULL OR retry_not_before IS NOT NULL) THEN
        RAISE EXCEPTION 'publication reconciliation parking data exists; refusing rollback';
    END IF;
END $$;
ALTER TABLE public.publication_reconciliation_request
    DROP CONSTRAINT publication_reconciliation_parking_shape,
    DROP COLUMN retry_not_before,
    DROP COLUMN parked_reason,
    DROP COLUMN evaluated_generation;

-- Former migration: 20261020000001_publication_reconciliation.tx.down.sql
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_reconciliation_request)
       OR EXISTS (SELECT 1 FROM public.publication_reconciliation_event)
       OR EXISTS (SELECT 1 FROM public.operation WHERE kind='publication_reconcile_v1') THEN
        RAISE EXCEPTION 'publication reconciliation contains live data; refusing rollback';
    END IF;
END $$;
DROP TRIGGER publication_reconciliation_event_update ON public.publication_reconciliation_event;
DROP FUNCTION public.guard_publication_reconciliation_event_update();
DROP TRIGGER publication_reconciliation_owner_event ON public.publication_reconciliation_event;
DROP TRIGGER publication_reconciliation_owner_operation ON public.operation;
DROP FUNCTION public.check_publication_reconciliation_owner_trigger();
DROP FUNCTION public.check_publication_reconciliation_owner(uuid);
DROP TABLE public.publication_reconciliation_event;
DROP TABLE public.publication_reconciliation_dirty_scope;
DROP TABLE public.publication_reconciliation_request;
ALTER TABLE public.operation DROP CONSTRAINT operation_publication_reconciliation_snapshot_check;
ALTER TABLE public.operation DROP CONSTRAINT operation_source_analysis_target_shape;
ALTER TABLE public.operation ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
    (kind IN ('publish_track_v1','cleanup_publication_sources_v1') AND source_analysis_mode IS NULL
        AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND target_installation_id IS NULL AND target_source_root_id IS NULL AND target_source_location_id IS NULL
        AND provider_source_id IS NULL AND provider_configuration_identity IS NULL AND provider_cache_key IS NULL
        AND NOT provider_explicit_refresh AND (NOT tools_read_required OR kind='publish_track_v1'))
    OR (kind NOT IN ('publish_track_v1','cleanup_publication_sources_v1') AND (((source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target
        AND (NOT tools_read_required OR kind = 'scan_source'))
      OR (source_analysis_mode = 'batch' AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target)
      OR (source_analysis_mode = 'single_step' AND
          (((state IN ('queued','running') AND target_work_id IS NOT NULL AND target_step IN ('sha256','probe','fingerprint','metadata'))
            OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))
            AND (NOT rerun_target OR (state IN ('queued','running') AND target_step='fingerprint') OR (state IN ('succeeded','failed') AND target_work_id IS NULL AND target_step IS NULL))))) IS TRUE))
);
ALTER TABLE public.operation DROP CONSTRAINT operation_target_identity;
ALTER TABLE public.operation ADD CONSTRAINT operation_target_identity CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    COALESCE(input_snapshot ->> 'target_identity','') <> ''
);
ALTER TABLE public.operation DROP CONSTRAINT operation_mutation_has_target_installation;
ALTER TABLE public.operation ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
    kind IN ('publish_track_v1','cleanup_publication_sources_v1') OR
    kind IN ('move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
        'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
        'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1','matching_apply_v1') OR
    state NOT IN ('queued','running') OR target_installation_id IS NOT NULL
);
ALTER TABLE public.operation DROP CONSTRAINT operation_kind_check;
ALTER TABLE public.operation ADD CONSTRAINT operation_kind_check CHECK (kind = ANY (ARRAY[
    'install','activate','delete','move_tools_root','scan_source','analyze_source','cleanup_source_analysis_artifacts',
    'provider_release_lookup','provider_recording_lookup','provider_release_search','provider_recording_search',
    'provider_artist_search','provider_artist_release_browse','provider_acoustid_lookup','matching_evaluation_v1',
    'matching_apply_v1','publish_track_v1','cleanup_publication_sources_v1'
]));

-- Former migration: 20261018000001_publication_manual_lyrics_receipt.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_manual_lyrics_receipt) THEN
        RAISE EXCEPTION 'refusing to drop populated publication_manual_lyrics_receipt';
    END IF;
END;
$$;

DROP TABLE public.publication_manual_lyrics_receipt;

-- Former migration: 20261017000007_publication_source_hold.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.publication_source_hold) THEN
        RAISE EXCEPTION 'cannot roll back publication source holds while holds exist';
    END IF;
END;
$$;

DROP TABLE public.publication_source_hold;
ALTER TABLE public.track_publication_attempt
    DROP CONSTRAINT track_publication_attempt_operation_epoch_unique;

-- Former migration: 20261017000006_publication_operations.tx.down.sql
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

-- Former migration: 20261017000005_file_publication_state.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.track_publication)
       OR EXISTS (SELECT 1 FROM public.track_publication_attempt)
       OR EXISTS (SELECT 1 FROM public.publication_execution_claim)
       OR EXISTS (SELECT 1 FROM public.publication_namespace_claim)
       OR EXISTS (SELECT 1 FROM public.publication_namespace_reservation)
       OR EXISTS (SELECT 1 FROM public.source_cleanup_item) THEN
        RAISE EXCEPTION 'cannot roll back file publication state while it owns live state';
    END IF;
END;
$$;

DROP TABLE public.source_cleanup_item;
DROP TABLE public.publication_namespace_reservation;
DROP TABLE public.publication_namespace_claim;
DROP TABLE public.publication_execution_claim;
DROP TABLE public.track_publication_attempt;
DROP TABLE public.track_publication;
DROP TABLE public.publication_namespace_state;

-- Former migration: 20261017000004_position_metadata.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM release_track WHERE metadata <> '{}'::jsonb) THEN
        RAISE EXCEPTION 'cannot roll back position metadata while desired metadata is populated';
    END IF;
END;
$$;

ALTER TABLE release_track
    DROP CONSTRAINT release_track_metadata_object_check,
    DROP COLUMN metadata;

-- Former migration: 20261017000003_library_primary_artist.tx.down.sql
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM local_release WHERE primary_artist_id IS NOT NULL)
       OR EXISTS (SELECT 1 FROM release_track WHERE primary_artist_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back library primary artist selections while selections exist';
    END IF;
END
$$;

ALTER TABLE release_track DROP COLUMN primary_artist_id, DROP COLUMN metadata_revision;
ALTER TABLE local_recording DROP COLUMN metadata_revision;
ALTER TABLE local_track DROP COLUMN metadata_revision;
ALTER TABLE local_release DROP COLUMN primary_artist_id, DROP COLUMN metadata_revision;

-- Former migration: 20261017000002_release_track_identity.tx.down.sql
ALTER TABLE public.release_track
    DROP CONSTRAINT release_track_id_key;

ALTER TABLE public.release_track
    DROP COLUMN id;

-- Former migration: 20261017000001_drop_native_matroska_metadata.tx.down.sql
ALTER TABLE public.media_metadata_result
    ADD COLUMN native_matroska jsonb,
    ADD CONSTRAINT media_metadata_result_native_matroska_check
        CHECK (((native_matroska IS NULL) OR (jsonb_typeof(native_matroska) = 'object'::text)));

-- Original baseline rollback.
-- A baseline rollback is destructive: reject populated application tables before DDL.
DO $baseline_rollback$
DECLARE
    relation_name text;
    populated boolean;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY['app_setting', 'collection', 'collection_artist', 'collection_release', 'incoming_group', 'incoming_group_matching_authority', 'incoming_group_member', 'incoming_group_member_location', 'incoming_group_return_automatic_receipt', 'incoming_group_unlink_receipt', 'incoming_match_candidate', 'incoming_match_candidate_track', 'incoming_match_evaluation', 'incoming_match_evaluation_location', 'incoming_match_evaluation_member', 'incoming_matching_automatic_admission', 'incoming_matching_generation', 'incoming_matching_reconcile_event', 'local_artist', 'local_matching_receipt', 'local_recording', 'local_release', 'local_track', 'manual_matching_admission', 'manual_matching_lookup_seal', 'manual_matching_receipt', 'manual_picker_search', 'manual_picker_search_result', 'matching_acquisition', 'matching_acquisition_phase', 'matching_acquisition_seal', 'matching_apply_receipt', 'matching_apply_request', 'matching_delivery', 'matching_fallback_authority_seal', 'matching_fallback_calculation_receipt', 'matching_fallback_evaluation_handoff', 'matching_fallback_lookup_receipt', 'matching_fallback_request_registration', 'matching_fallback_search_receipt', 'matching_provider_dependency', 'matching_run', 'matching_run_retirement_authorization', 'matching_run_target', 'media_fingerprint_result', 'media_metadata_result', 'media_probe_cache', 'media_variant', 'operation', 'operation_source_work_hold', 'operation_tool_read_hold', 'output_reset_journal', 'provider', 'provider_artist', 'provider_local_artist', 'provider_local_recording', 'provider_local_release', 'provider_local_track', 'provider_recording', 'provider_release', 'provider_release_artist_credit', 'provider_release_track', 'provider_response_cache', 'provider_source', 'provider_source_entity', 'provider_source_release_artist_credit', 'provider_source_release_track', 'recording_artist', 'release_artist', 'release_track', 'source_analysis_artifact', 'source_analysis_artifact_cleanup_item', 'source_analysis_step', 'source_analysis_work', 'source_analysis_work_artifact_binding', 'source_analysis_work_execution', 'source_location', 'source_root', 'source_scan_candidate', 'source_work_variant_identity', 'tool_installation', 'tools_execution_claim', 'track_artist', 'track_source']
    LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I)', relation_name) INTO populated;
        IF populated THEN
            RAISE EXCEPTION 'cannot roll back populated application baseline: % contains data', relation_name;
        END IF;
    END LOOP;
    IF EXISTS (SELECT 1 FROM public.incoming_grouping_state WHERE revision <> 0 OR NOT needs_refresh) THEN
        RAISE EXCEPTION 'cannot roll back populated application baseline: grouping state was modified';
    END IF;
END;
$baseline_rollback$;

DROP TRIGGER matching_run_retirement_authorization_validate ON public.matching_run_retirement_authorization;
DROP TRIGGER matching_run_retirement_authorization_immutable ON public.matching_run_retirement_authorization;
DROP TRIGGER matching_run_quiescence_timestamp_immutable ON public.matching_run;
DROP TRIGGER matching_fallback_search_receipt_immutable ON public.matching_fallback_search_receipt;
DROP TRIGGER matching_fallback_request_registration_immutable ON public.matching_fallback_request_registration;
DROP TRIGGER matching_fallback_lookup_receipt_immutable ON public.matching_fallback_lookup_receipt;
DROP TRIGGER matching_fallback_evaluation_handoff_write_once ON public.matching_fallback_evaluation_handoff;
DROP TRIGGER matching_fallback_calculation_receipt_immutable ON public.matching_fallback_calculation_receipt;
DROP TRIGGER matching_fallback_authority_seal_write_once ON public.matching_fallback_authority_seal;
DROP TRIGGER frozen_matching_apply_request_write_once ON public.matching_apply_request;
DROP TRIGGER matching_apply_receipt_write_once ON public.matching_apply_receipt;
DROP TRIGGER matching_apply_receipt_input_binding ON public.matching_apply_receipt;
DROP TRIGGER matching_acquisition_seal_write_once ON public.matching_acquisition_seal;
DROP TRIGGER matching_acquisition_phase_append_only ON public.matching_acquisition_phase;
DROP TRIGGER manual_picker_search_result_immutable ON public.manual_picker_search_result;
DROP TRIGGER manual_matching_receipt_write_once ON public.manual_matching_receipt;
ALTER TABLE public.matching_run_target DROP CONSTRAINT matching_run_target_run_id_fkey;
ALTER TABLE public.matching_run_retirement_authorization DROP CONSTRAINT matching_run_retirement_authorization_run_id_fkey;
ALTER TABLE public.matching_run DROP CONSTRAINT matching_run_provider_source_id_fkey;
ALTER TABLE public.matching_run DROP CONSTRAINT matching_run_operation_id_fkey;
ALTER TABLE public.matching_provider_dependency DROP CONSTRAINT matching_provider_dependency_request_id_fkey;
ALTER TABLE public.matching_provider_dependency DROP CONSTRAINT matching_provider_dependency_parent_run_id_fkey;
ALTER TABLE public.matching_fallback_search_receipt DROP CONSTRAINT matching_fallback_search_receipt_run_id_request_id_fkey;
ALTER TABLE public.matching_fallback_search_receipt DROP CONSTRAINT matching_fallback_search_receipt_run_id_fkey;
ALTER TABLE public.matching_fallback_search_receipt DROP CONSTRAINT matching_fallback_search_receipt_request_id_fkey;
ALTER TABLE public.matching_fallback_search_receipt DROP CONSTRAINT matching_fallback_search_receipt_provider_source_id_fkey;
ALTER TABLE public.matching_fallback_request_registration DROP CONSTRAINT matching_fallback_request_registration_run_id_request_id_fkey;
ALTER TABLE public.matching_fallback_request_registration DROP CONSTRAINT matching_fallback_request_registration_run_id_fkey;
ALTER TABLE public.matching_fallback_request_registration DROP CONSTRAINT matching_fallback_request_registration_request_id_fkey;
ALTER TABLE public.matching_fallback_request_registration DROP CONSTRAINT matching_fallback_request_registration_provider_source_id_fkey;
ALTER TABLE public.matching_fallback_lookup_receipt DROP CONSTRAINT matching_fallback_lookup_receipt_run_id_request_id_fkey;
ALTER TABLE public.matching_fallback_lookup_receipt DROP CONSTRAINT matching_fallback_lookup_receipt_run_id_fkey;
ALTER TABLE public.matching_fallback_lookup_receipt DROP CONSTRAINT matching_fallback_lookup_receipt_request_id_fkey;
ALTER TABLE public.matching_fallback_lookup_receipt DROP CONSTRAINT matching_fallback_lookup_receipt_provider_source_id_fkey;
ALTER TABLE public.matching_fallback_evaluation_handoff DROP CONSTRAINT matching_fallback_evaluation_handoff_run_id_wait_revision_fkey;
ALTER TABLE public.matching_fallback_evaluation_handoff DROP CONSTRAINT matching_fallback_evaluation_handoff_run_id_fkey;
ALTER TABLE public.matching_fallback_calculation_receipt DROP CONSTRAINT matching_fallback_calculation_receipt_run_id_fkey;
ALTER TABLE public.matching_fallback_calculation_receipt DROP CONSTRAINT matching_fallback_calculation__run_id_acquisition_revision_fkey;
ALTER TABLE public.matching_fallback_authority_seal DROP CONSTRAINT matching_fallback_authority_seal_run_id_fkey;
ALTER TABLE public.matching_delivery DROP CONSTRAINT matching_delivery_run_id_fkey;
ALTER TABLE public.matching_apply_request DROP CONSTRAINT matching_apply_request_parent_run_id_fkey;
ALTER TABLE public.matching_apply_request DROP CONSTRAINT matching_apply_request_frozen_seal_fk;
ALTER TABLE public.matching_apply_request DROP CONSTRAINT matching_apply_request_apply_operation_id_fkey;
ALTER TABLE public.matching_apply_receipt DROP CONSTRAINT matching_apply_receipt_request_input_fk;
ALTER TABLE public.matching_apply_receipt DROP CONSTRAINT matching_apply_receipt_apply_operation_id_fkey;
ALTER TABLE public.matching_acquisition_seal DROP CONSTRAINT matching_acquisition_seal_run_id_fkey;
ALTER TABLE public.matching_acquisition_phase DROP CONSTRAINT matching_acquisition_phase_run_id_fkey;
ALTER TABLE public.matching_acquisition DROP CONSTRAINT matching_acquisition_run_id_fkey;
ALTER TABLE public.manual_picker_search_result DROP CONSTRAINT manual_picker_search_result_provider_source_id_fkey;
ALTER TABLE public.manual_picker_search_result DROP CONSTRAINT manual_picker_search_result_operation_id_fkey;
ALTER TABLE public.manual_picker_search DROP CONSTRAINT manual_picker_search_provider_source_id_fkey;
ALTER TABLE public.manual_picker_search DROP CONSTRAINT manual_picker_search_operation_id_fkey;
ALTER TABLE public.manual_matching_receipt DROP CONSTRAINT manual_matching_receipt_local_release_id_fkey;
ALTER TABLE public.manual_matching_lookup_seal DROP CONSTRAINT manual_matching_lookup_seal_request_id_fkey;
ALTER TABLE public.manual_matching_lookup_seal DROP CONSTRAINT manual_matching_lookup_seal_provider_source_id_fkey;
ALTER TABLE public.manual_matching_lookup_seal DROP CONSTRAINT manual_matching_lookup_seal_lookup_operation_id_fkey;
ALTER TABLE public.manual_matching_admission DROP CONSTRAINT manual_matching_admission_run_id_fkey;
ALTER TABLE public.manual_matching_admission DROP CONSTRAINT manual_matching_admission_lookup_operation_id_fkey;
ALTER TABLE public.incoming_matching_generation DROP CONSTRAINT incoming_matching_generation_current_run_fk;
ALTER TABLE public.incoming_match_evaluation_member DROP CONSTRAINT incoming_match_evaluation_member_group_id_fkey;
ALTER TABLE public.incoming_match_evaluation_location DROP CONSTRAINT incoming_match_evaluation_location_group_id_file_member_id_fkey;
ALTER TABLE public.incoming_match_evaluation DROP CONSTRAINT incoming_match_evaluation_provider_id_provider_source_id_fkey;
ALTER TABLE public.incoming_match_evaluation DROP CONSTRAINT incoming_match_evaluation_frozen_seal_fk;
ALTER TABLE public.incoming_match_candidate_track DROP CONSTRAINT incoming_match_candidate_trac_provider_id_release_id_track_fkey;
ALTER TABLE public.incoming_match_candidate_track DROP CONSTRAINT incoming_match_candidate_trac_group_id_file_member_id_prov_fkey;
ALTER TABLE public.incoming_match_candidate DROP CONSTRAINT incoming_match_candidate_provider_id_release_id_fkey;
ALTER TABLE public.incoming_match_candidate DROP CONSTRAINT incoming_match_candidate_provider_id_provider_source_id_fkey;
ALTER TABLE public.incoming_match_candidate DROP CONSTRAINT incoming_match_candidate_group_id_file_member_id_fkey;
ALTER TABLE public.incoming_group_member_location DROP CONSTRAINT incoming_group_member_location_group_id_variant_id_fkey;
ALTER TABLE public.incoming_group_member DROP CONSTRAINT incoming_group_member_group_id_fkey;

DROP TABLE public.matching_run_target;
DROP TABLE public.matching_run_retirement_authorization;
DROP TABLE public.matching_run;
DROP TABLE public.matching_provider_dependency;
DROP TABLE public.matching_fallback_search_receipt;
DROP TABLE public.matching_fallback_request_registration;
DROP TABLE public.matching_fallback_lookup_receipt;
DROP TABLE public.matching_fallback_evaluation_handoff;
DROP TABLE public.matching_fallback_calculation_receipt;
DROP TABLE public.matching_fallback_authority_seal;
DROP TABLE public.matching_delivery;
DROP TABLE public.matching_apply_request;
DROP TABLE public.matching_apply_receipt;
DROP TABLE public.matching_acquisition_seal;
DROP TABLE public.matching_acquisition_phase;
DROP TABLE public.matching_acquisition;
DROP TABLE public.manual_picker_search_result;
DROP TABLE public.manual_picker_search;
DROP TABLE public.manual_matching_receipt;
DROP TABLE public.manual_matching_lookup_seal;
DROP TABLE public.manual_matching_admission;
DROP TABLE public.local_matching_receipt;
DROP TABLE public.incoming_matching_reconcile_event;
DROP TABLE public.incoming_matching_generation;
DROP TABLE public.incoming_matching_automatic_admission;
DROP TABLE public.incoming_match_evaluation_member;
DROP TABLE public.incoming_match_evaluation_location;
DROP TABLE public.incoming_match_evaluation;
DROP TABLE public.incoming_match_candidate_track;
DROP TABLE public.incoming_match_candidate;
DROP TABLE public.incoming_grouping_state;
DROP TABLE public.incoming_group_unlink_receipt;
DROP TABLE public.incoming_group_return_automatic_receipt;
DROP TABLE public.incoming_group_member_location;
DROP TABLE public.incoming_group_member;
DROP TABLE public.incoming_group_matching_authority;
DROP TABLE public.incoming_group;
DROP FUNCTION public.validate_matching_run_retirement_authorization();
DROP FUNCTION public.validate_matching_apply_receipt_input_binding();
DROP FUNCTION public.reject_matching_run_retirement_authorization_mutation();
DROP FUNCTION public.reject_matching_fallback_search_receipt_mutation();
DROP FUNCTION public.reject_matching_fallback_request_registration_mutation();
DROP FUNCTION public.reject_matching_fallback_lookup_receipt_mutation();
DROP FUNCTION public.reject_matching_fallback_evaluation_handoff_mutation();
DROP FUNCTION public.reject_matching_fallback_calculation_receipt_mutation();
DROP FUNCTION public.reject_matching_fallback_authority_seal_mutation();
DROP FUNCTION public.reject_matching_apply_receipt_mutation();
DROP FUNCTION public.reject_matching_acquisition_seal_mutation();
DROP FUNCTION public.reject_matching_acquisition_phase_mutation();
DROP FUNCTION public.reject_manual_matching_receipt_mutation();
DROP FUNCTION public.reject_frozen_matching_apply_request_mutation();
DROP FUNCTION public.protect_matching_run_quiescence_timestamp();
DROP FUNCTION public.matching_run_retirement_is_authorized(uuid);
