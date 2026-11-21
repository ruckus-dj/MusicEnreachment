DO $guard$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.media_metadata_result
        WHERE tag_encoding IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot roll back source metadata tag encoding while saved corrections remain';
    END IF;
END;
$guard$;

ALTER TABLE public.media_metadata_result
    DROP CONSTRAINT media_metadata_result_tag_encoding_check,
    DROP COLUMN tag_encoding;
