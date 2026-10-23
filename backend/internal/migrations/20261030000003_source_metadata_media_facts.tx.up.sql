ALTER TABLE public.media_metadata_result
    ADD COLUMN cover_observed boolean NOT NULL DEFAULT false,
    ADD COLUMN cover_data bytea,
    ADD COLUMN cover_mime text,
    ADD COLUMN cover_sha256 bytea,
    ADD COLUMN cover_size_bytes bigint,
    ADD COLUMN properties jsonb,
    ADD CONSTRAINT media_metadata_result_cover_facts_check CHECK (
        (
            cover_data IS NULL AND cover_mime IS NULL AND cover_sha256 IS NULL AND cover_size_bytes IS NULL
        ) OR (
            cover_data IS NOT NULL AND cover_mime IS NOT NULL AND cover_sha256 IS NOT NULL AND cover_size_bytes IS NOT NULL
            AND cover_observed IS TRUE
            AND cover_mime LIKE 'image/%'
            AND octet_length(cover_sha256) = 32
            AND cover_size_bytes = octet_length(cover_data)
        )
    );

ALTER TABLE public.track_source
    ADD COLUMN source_metadata_result_id uuid,
    ADD CONSTRAINT track_source_source_metadata_result_fk
        FOREIGN KEY (source_metadata_result_id)
        REFERENCES public.media_metadata_result(id) ON DELETE RESTRICT;

CREATE INDEX track_source_source_metadata_result_idx
    ON public.track_source(source_metadata_result_id)
    WHERE source_metadata_result_id IS NOT NULL;
