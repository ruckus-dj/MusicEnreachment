CREATE TABLE position_source_authority (
    position_id uuid PRIMARY KEY REFERENCES release_track(id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('manual', 'automatic')),
    revision uuid NOT NULL,
    publication_epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    source_location_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
