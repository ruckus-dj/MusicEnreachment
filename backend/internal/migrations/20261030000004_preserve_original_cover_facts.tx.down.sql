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
