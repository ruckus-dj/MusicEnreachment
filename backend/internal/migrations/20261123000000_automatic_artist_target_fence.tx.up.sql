ALTER TABLE matching_run
    ADD COLUMN fence_version integer NOT NULL DEFAULT 1 CHECK (fence_version IN (1, 2));

ALTER TABLE matching_run_target
    ADD COLUMN target_capture jsonb,
    ADD COLUMN target_capture_digest bytea,
    ADD CONSTRAINT matching_run_target_capture_pair CHECK (
        (target_capture IS NULL) = (target_capture_digest IS NULL)
    ),
    ADD CONSTRAINT matching_run_target_capture_digest_length CHECK (
        target_capture_digest IS NULL OR octet_length(target_capture_digest) = 32
    );

CREATE TABLE incoming_matching_automatic_admission (
    fingerprint bytea PRIMARY KEY CHECK (octet_length(fingerprint) = 32),
    original_run_id uuid NOT NULL,
    original_operation_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
