ALTER TABLE public.media_metadata_result
    DROP CONSTRAINT media_metadata_result_native_matroska_check,
    DROP COLUMN native_matroska;
