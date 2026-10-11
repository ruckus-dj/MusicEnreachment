-- Keep the response accepted by each provider delivery attached to its manual
-- request. The accepted row is sealed only when the lookup dependency succeeds.
ALTER TABLE manual_matching_admission
    ALTER COLUMN evaluation_revision DROP NOT NULL;
ALTER TABLE manual_matching_admission
    DROP CONSTRAINT manual_matching_admission_evaluation_revision_check;
ALTER TABLE manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_evaluation_revision_check
    CHECK (evaluation_revision IS NULL OR length(btrim(evaluation_revision)) > 0);

CREATE TABLE manual_matching_lookup_seal (
    request_id uuid PRIMARY KEY REFERENCES manual_matching_admission(request_id) ON DELETE CASCADE,
    lookup_operation_id uuid NOT NULL UNIQUE REFERENCES operation(id) ON DELETE RESTRICT,
    provider_source_id uuid NOT NULL REFERENCES provider_source(id) ON DELETE RESTRICT,
    configuration_identity text NOT NULL CHECK (length(btrim(configuration_identity)) > 0),
    cache_key text NOT NULL CHECK (length(btrim(cache_key)) > 0),
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    cache_revision text,
    projection_sha256 bytea NOT NULL CHECK (octet_length(projection_sha256) = 32),
    sealed_at timestamptz
);

CREATE INDEX manual_matching_lookup_seal_operation_idx
    ON manual_matching_lookup_seal(lookup_operation_id);
