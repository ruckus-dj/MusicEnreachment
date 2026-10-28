DO $migration$
DECLARE
    installation record;
    attempt record;
    selection jsonb;
    migrated_tools jsonb;
    output jsonb;
    canonical_version text;
    executable_output text;
    executable_name text;
    ffmpeg_token text;
    ffprobe_token text;
    selected_token text;
    selected_output text;
    logical_executable text;
    banner_fields text[];
    evidence_fields text[];
BEGIN
    ALTER TABLE public.tool_installation RENAME COLUMN executable_versions TO executable_verification_output;
    ALTER TABLE public.tool_installation ADD COLUMN version text;

    FOR installation IN SELECT id, package_kind, platform_goos, state, executable_verification_output FROM public.tool_installation LOOP
        IF installation.state <> 'ready' THEN CONTINUE; END IF;
        output := installation.executable_verification_output;
        IF jsonb_typeof(output) IS DISTINCT FROM 'object' THEN
            RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: malformed verification output', installation.id;
        END IF;
        logical_executable := CASE WHEN installation.package_kind = 'ffmpeg' THEN 'ffmpeg' ELSE 'fpcalc' END;
        executable_name := logical_executable;
        IF installation.platform_goos = 'windows' THEN executable_name := executable_name || '.exe'; END IF;
        IF jsonb_typeof(output -> executable_name) IS DISTINCT FROM 'string' THEN
            RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: missing verification output for %', installation.id, executable_name;
        END IF;
        executable_output := output ->> executable_name;
        banner_fields := regexp_split_to_array(regexp_replace(split_part(executable_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
        IF coalesce(array_length(banner_fields, 1), 0) < 3 OR lower(banner_fields[1]) <> logical_executable OR lower(banner_fields[2]) <> 'version' OR banner_fields[3] = '' THEN
            RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: malformed verification output', installation.id;
        END IF;
        selected_token := banner_fields[3];
        IF installation.package_kind = 'ffmpeg' THEN
            executable_name := 'ffprobe' || CASE WHEN installation.platform_goos = 'windows' THEN '.exe' ELSE '' END;
            IF jsonb_typeof(output -> executable_name) IS DISTINCT FROM 'string' THEN
                RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: missing verification output for %', installation.id, executable_name;
            END IF;
            ffmpeg_token := selected_token;
            selected_output := output ->> executable_name;
            banner_fields := regexp_split_to_array(regexp_replace(split_part(selected_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(banner_fields, 1), 0) < 3 OR lower(banner_fields[1]) <> 'ffprobe' OR lower(banner_fields[2]) <> 'version' OR banner_fields[3] = '' THEN
                RAISE EXCEPTION 'cannot backfill canonical version for ready tool installation %: malformed verification output for %', installation.id, executable_name;
            END IF;
            ffprobe_token := banner_fields[3];
            canonical_version := 'ffmpeg ' || ffmpeg_token || ' + ffprobe ' || ffprobe_token;
        ELSE
            canonical_version := selected_token;
        END IF;
        UPDATE public.tool_installation SET version = canonical_version WHERE id = installation.id;
    END LOOP;

    -- Publication inputs are retained execution authority. Rebind only when the
    -- exact installation, relative path, executable and captured raw banner agree.
    FOR attempt IN SELECT operation_id, frozen_inputs FROM public.track_publication_attempt
        WHERE frozen_inputs IS NOT NULL LOOP
        IF jsonb_typeof(attempt.frozen_inputs -> 'tools') IS DISTINCT FROM 'array' THEN
            RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: tools array is missing', attempt.operation_id;
        END IF;
        migrated_tools := '[]'::jsonb;
        FOR selection IN SELECT value FROM jsonb_array_elements(attempt.frozen_inputs -> 'tools') LOOP
            IF jsonb_typeof(selection) IS DISTINCT FROM 'object' OR
               jsonb_typeof(selection -> 'package_kind') IS DISTINCT FROM 'string' OR selection ->> 'package_kind' = '' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: selection package kind is missing', attempt.operation_id;
            END IF;
            IF selection ->> 'package_kind' <> 'ffmpeg' THEN
                migrated_tools := migrated_tools || jsonb_build_array(selection);
                CONTINUE;
            END IF;
            IF jsonb_typeof(selection -> 'installation_id') IS DISTINCT FROM 'string' OR selection ->> 'installation_id' = '' OR
               jsonb_typeof(selection -> 'relative_path') IS DISTINCT FROM 'string' OR selection ->> 'relative_path' = '' OR
               jsonb_typeof(selection -> 'version') IS DISTINCT FROM 'string' OR selection ->> 'version' = '' OR
               jsonb_typeof(selection -> 'version_banner') IS DISTINCT FROM 'string' OR selection ->> 'version_banner' = '' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: frozen selection identity and evidence must be nonempty strings', attempt.operation_id;
            END IF;
            SELECT i.version, i.relative_path, i.executable_verification_output, i.platform_goos
              INTO installation
              FROM public.tool_installation i
             WHERE i.id::text = selection ->> 'installation_id' AND i.package_kind = 'ffmpeg';
            IF NOT FOUND OR installation.version IS NULL OR installation.relative_path IS DISTINCT FROM selection ->> 'relative_path' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: installation identity or path differs', attempt.operation_id;
            END IF;
            executable_name := selection ->> 'executable';
            IF executable_name NOT IN ('ffmpeg', 'ffprobe') THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: unexpected executable', attempt.operation_id;
            END IF;
            IF installation.platform_goos = 'windows' THEN executable_name := executable_name || '.exe'; END IF;
            IF jsonb_typeof(installation.executable_verification_output -> executable_name) IS DISTINCT FROM 'string' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: raw executable evidence is missing', attempt.operation_id;
            END IF;
            selected_output := installation.executable_verification_output ->> executable_name;
            logical_executable := selection ->> 'executable';
            banner_fields := regexp_split_to_array(regexp_replace(split_part(selected_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(banner_fields, 1), 0) < 3 OR lower(banner_fields[1]) <> logical_executable OR lower(banner_fields[2]) <> 'version' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: malformed raw executable banner', attempt.operation_id;
            END IF;
            selected_token := banner_fields[3];
            ffmpeg_token := (regexp_match(installation.version, '^ffmpeg ([^ ]+) \+ ffprobe ([^ ]+)$'))[1];
            ffprobe_token := (regexp_match(installation.version, '^ffmpeg ([^ ]+) \+ ffprobe ([^ ]+)$'))[2];
            executable_output := installation.executable_verification_output ->> ('ffmpeg' || CASE WHEN installation.platform_goos = 'windows' THEN '.exe' ELSE '' END);
            evidence_fields := regexp_split_to_array(regexp_replace(split_part(executable_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(evidence_fields, 1), 0) < 3 OR lower(evidence_fields[1]) <> 'ffmpeg' OR lower(evidence_fields[2]) <> 'version' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: malformed ffmpeg evidence', attempt.operation_id;
            END IF;
            IF evidence_fields[3] IS DISTINCT FROM ffmpeg_token THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: ffmpeg evidence differs', attempt.operation_id;
            END IF;
            executable_output := installation.executable_verification_output ->> ('ffprobe' || CASE WHEN installation.platform_goos = 'windows' THEN '.exe' ELSE '' END);
            evidence_fields := regexp_split_to_array(regexp_replace(split_part(executable_output, E'\n', 1), $ws$^\s+|\s+$$ws$, '', 'g'), $ws$\s+$ws$);
            IF coalesce(array_length(evidence_fields, 1), 0) < 3 OR lower(evidence_fields[1]) <> 'ffprobe' OR lower(evidence_fields[2]) <> 'version' THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: malformed ffprobe evidence', attempt.operation_id;
            END IF;
            IF selected_token IS NULL OR selected_token IS DISTINCT FROM selection ->> 'version' OR
               regexp_replace(selected_output, $trim$^\s+|\s+$$trim$, '', 'g') IS DISTINCT FROM selection ->> 'version_banner' OR
               ffmpeg_token IS NULL OR ffprobe_token IS NULL OR evidence_fields[3] IS DISTINCT FROM ffprobe_token THEN
                RAISE EXCEPTION 'cannot migrate retained publication tool authority for operation %: frozen selection is not proven by installation evidence', attempt.operation_id;
            END IF;
            migrated_tools := migrated_tools || jsonb_build_array(jsonb_set(selection, '{version}', to_jsonb(installation.version), false));
        END LOOP;
        UPDATE public.track_publication_attempt
           SET frozen_inputs = jsonb_set(frozen_inputs, '{tools}', migrated_tools, false)
         WHERE operation_id = attempt.operation_id;
    END LOOP;

    ALTER TABLE public.tool_installation ADD CONSTRAINT tool_installation_ready_version_check
        CHECK (state <> 'ready' OR (version IS NOT NULL AND version <> ''));
END $migration$;
