CREATE TABLE public.source_cleanup_completion (
    work_id uuid PRIMARY KEY,
    source_root_id uuid NOT NULL REFERENCES public.source_root(id) ON DELETE CASCADE,
    source_location_id uuid NOT NULL,
    media_variant_id uuid NOT NULL REFERENCES public.media_variant(id) ON DELETE CASCADE,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    mtime timestamptz NOT NULL,
    kind text NOT NULL CHECK (kind IN ('native_removed', 'absent_after_explicit_retry')),
    handled_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

COMMENT ON TABLE public.source_cleanup_completion IS
    'Current root-owned namespace exclusion, retained until an accepted observation of its relative path; snapshot identities do not assert live work.';
