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
