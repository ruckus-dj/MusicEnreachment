ALTER TABLE public.track_publication
    DROP CONSTRAINT track_publication_cover_embedding_projection_check,
    ADD CONSTRAINT track_publication_cover_embedding_projection_check CHECK (
        cover_embedding IS NULL OR (
            (jsonb_typeof(cover_embedding) = 'object') IS TRUE
            AND (cover_embedding ?& ARRAY[
                'version', 'generation', 'receipt_operation_id', 'receipt_release_id',
                'receipt_revision', 'receipt_mode', 'requested_generation',
                'requested_sha256', 'outcome', 'warning_code'
            ]) IS TRUE
            AND (jsonb_typeof(cover_embedding->'version') = 'number') IS TRUE
            AND ((cover_embedding->>'version') IN ('1', '2')) IS TRUE
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
                ((cover_embedding->>'version') = '1'
                    AND NOT (cover_embedding ?| ARRAY['embedded_sha256', 'embedded_mime', 'embedded_size'])
                    AND (cover_embedding->>'outcome') IN ('verified', 'write_failed', 'read_failed', 'mismatch'))
                OR ((cover_embedding->>'version') = '2' AND (
                    ((cover_embedding->>'outcome') = 'verified'
                        AND (cover_embedding ?& ARRAY['embedded_sha256', 'embedded_mime', 'embedded_size'])
                        AND (jsonb_typeof(cover_embedding->'embedded_sha256') = 'string')
                        AND ((cover_embedding->>'embedded_sha256') ~ '^[0-9a-f]{64}$')
                        AND (jsonb_typeof(cover_embedding->'embedded_mime') = 'string')
                        AND ((cover_embedding->>'embedded_mime') = 'image/png')
                        AND (jsonb_typeof(cover_embedding->'embedded_size') = 'number')
                        AND ((cover_embedding->>'embedded_size') ~ '^[1-9][0-9]*$'))
                    OR ((cover_embedding->>'outcome') <> 'verified'
                        AND NOT (cover_embedding ?| ARRAY['embedded_sha256', 'embedded_mime', 'embedded_size'])
                        AND (cover_embedding->>'outcome') IN ('write_failed', 'read_failed', 'mismatch', 'preparation_failed'))
                ))
            ) IS TRUE
            AND (
                ((cover_embedding->>'outcome') = 'verified' AND (cover_embedding->>'warning_code') = '')
                OR ((cover_embedding->>'outcome') = 'write_failed' AND (cover_embedding->>'warning_code') = 'cover_write_failed')
                OR ((cover_embedding->>'outcome') = 'read_failed' AND (cover_embedding->>'warning_code') = 'cover_read_failed')
                OR ((cover_embedding->>'outcome') = 'mismatch' AND (cover_embedding->>'warning_code') = 'cover_mismatch')
                OR ((cover_embedding->>'version') = '2' AND (cover_embedding->>'outcome') = 'preparation_failed'
                    AND (cover_embedding->>'warning_code') IN ('unsupported_source', 'decode_failed', 'derivative_failed', 'source_unavailable'))
            ) IS TRUE
        )
    );
