CREATE TABLE publication_source_cleanup_request (
    originating_operation_id uuid PRIMARY KEY REFERENCES operation(id) ON DELETE RESTRICT,
    position_id uuid NOT NULL REFERENCES release_track(id) ON DELETE CASCADE,
    output_epoch uuid NOT NULL,
    publication_generation uuid NOT NULL,
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_attempted_at timestamptz,
    CONSTRAINT publication_source_cleanup_request_position_epoch_key UNIQUE (position_id, output_epoch),
    CONSTRAINT publication_source_cleanup_request_nonzero_witness CHECK (
        output_epoch <> '00000000-0000-0000-0000-000000000000'::uuid AND
        publication_generation <> '00000000-0000-0000-0000-000000000000'::uuid
    )
);

CREATE INDEX publication_source_cleanup_request_position_idx
    ON publication_source_cleanup_request(position_id);
