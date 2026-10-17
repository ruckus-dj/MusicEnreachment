ALTER TABLE public.media_metadata_result
    ADD COLUMN native_matroska jsonb,
    ADD CONSTRAINT media_metadata_result_native_matroska_check
        CHECK (((native_matroska IS NULL) OR (jsonb_typeof(native_matroska) = 'object'::text)));
