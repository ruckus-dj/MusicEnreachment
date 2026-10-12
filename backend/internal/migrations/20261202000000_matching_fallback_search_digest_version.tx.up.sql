ALTER TABLE matching_fallback_search_receipt
    ADD COLUMN payload_digest_version smallint NOT NULL DEFAULT 1
        CHECK (payload_digest_version IN (1, 2));
