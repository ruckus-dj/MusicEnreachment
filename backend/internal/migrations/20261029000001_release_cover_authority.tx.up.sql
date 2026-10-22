CREATE TABLE public.release_publication_cover (
    release_id uuid NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    mode text NOT NULL DEFAULT 'automatic',
    selected_generation uuid,
    selected_bytes bytea,
    selected_mime text,
    selected_sha256 bytea,
    selected_size bigint,
    pending_generation uuid,
    pending_bytes bytea,
    pending_mime text,
    pending_sha256 bytea,
    pending_size bigint,
    acquisition_owner uuid,
    warning text,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT release_publication_cover_pkey PRIMARY KEY (release_id),
    CONSTRAINT release_publication_cover_release_fkey FOREIGN KEY (release_id)
        REFERENCES public.local_release(id) ON DELETE RESTRICT,
    CONSTRAINT release_publication_cover_revision_check CHECK (revision > 0),
    CONSTRAINT release_publication_cover_mode_check CHECK (mode IN ('automatic', 'manual')),
    CONSTRAINT release_publication_cover_acquisition_owner_check CHECK (
        (acquisition_owner IS NULL OR acquisition_owner <> '00000000-0000-0000-0000-000000000000') IS TRUE
    ),
    CONSTRAINT release_publication_cover_acquisition_mode_check CHECK (
        (acquisition_owner IS NULL OR mode = 'automatic') IS TRUE
    ),
    CONSTRAINT release_publication_cover_selected_tuple_check CHECK ((
        (selected_generation IS NULL AND selected_bytes IS NULL AND selected_mime IS NULL AND selected_sha256 IS NULL AND selected_size IS NULL)
        OR (selected_generation IS NOT NULL AND selected_generation <> '00000000-0000-0000-0000-000000000000'
            AND selected_bytes IS NOT NULL AND octet_length(selected_bytes) > 0
            AND selected_mime IN ('image/png', 'image/jpeg') AND selected_sha256 IS NOT NULL AND octet_length(selected_sha256) = 32
            AND selected_sha256 = sha256(selected_bytes)
            AND selected_size = octet_length(selected_bytes) AND selected_size > 0)
    ) IS TRUE),
    CONSTRAINT release_publication_cover_pending_tuple_check CHECK ((
        (pending_generation IS NULL AND pending_bytes IS NULL AND pending_mime IS NULL AND pending_sha256 IS NULL AND pending_size IS NULL)
        OR (pending_generation IS NOT NULL AND pending_generation <> '00000000-0000-0000-0000-000000000000'
            AND pending_bytes IS NOT NULL AND octet_length(pending_bytes) > 0
            AND pending_mime IN ('image/png', 'image/jpeg') AND pending_sha256 IS NOT NULL AND octet_length(pending_sha256) = 32
            AND pending_sha256 = sha256(pending_bytes)
            AND pending_size = octet_length(pending_bytes) AND pending_size > 0)
    ) IS TRUE),
    CONSTRAINT release_publication_cover_manual_pending_check CHECK (
        (pending_generation IS NULL OR mode = 'manual') IS TRUE
    ),
    CONSTRAINT release_publication_cover_warning_check CHECK (
        warning IS NULL OR length(warning) > 0
    )
);

CREATE INDEX release_publication_cover_acquisition_idx
    ON public.release_publication_cover(release_id) WHERE acquisition_owner IS NOT NULL;
