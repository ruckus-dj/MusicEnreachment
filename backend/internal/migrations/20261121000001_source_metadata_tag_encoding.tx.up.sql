ALTER TABLE public.media_metadata_result
    ADD COLUMN tag_encoding jsonb,
    ADD CONSTRAINT media_metadata_result_tag_encoding_check CHECK (
        tag_encoding IS NULL OR (jsonb_typeof(tag_encoding) = 'object') IS TRUE
    );
