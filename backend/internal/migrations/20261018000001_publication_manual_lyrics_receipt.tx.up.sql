CREATE TABLE public.publication_manual_lyrics_receipt (
    position_id uuid NOT NULL,
    epoch uuid NOT NULL,
    desired_revision bigint NOT NULL,
    confirmed_generation uuid NOT NULL,
    published_generation uuid NOT NULL,
    desired_state text NOT NULL,
    desired_sha256 text,
    warning_code text,
    recorded_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT publication_manual_lyrics_receipt_pkey PRIMARY KEY (position_id),
    CONSTRAINT publication_manual_lyrics_receipt_position_fkey FOREIGN KEY (position_id)
        REFERENCES public.release_track(id) ON DELETE RESTRICT,
    CONSTRAINT publication_manual_lyrics_receipt_revision_check CHECK (desired_revision >= 0),
    CONSTRAINT publication_manual_lyrics_receipt_state_check CHECK (
        (desired_state = 'value' AND desired_sha256 IS NOT NULL AND desired_sha256 ~ '^[0-9a-f]{64}$') OR
        (desired_state = 'explicit_empty' AND desired_sha256 IS NULL)
    ),
    CONSTRAINT publication_manual_lyrics_receipt_warning_check CHECK (
        warning_code IS NULL OR warning_code IN ('readback_mismatch')
    )
);
