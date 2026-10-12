ALTER TABLE matching_fallback_search_receipt
    DROP CONSTRAINT matching_fallback_search_receipt_stage_index_check,
    ADD CONSTRAINT matching_fallback_search_receipt_stage_index_check CHECK (stage_index >= 0);

ALTER TABLE matching_fallback_calculation_receipt
    DROP CONSTRAINT matching_fallback_calculation_receipt_stage_index_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_stage_index_check CHECK (stage_index >= 0);

ALTER TABLE matching_fallback_calculation_receipt
    DROP CONSTRAINT matching_fallback_calculation_receipt_schema_version_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_schema_version_check CHECK (
        (schema_version = 2 AND calculator_version = 1 AND digest_version = 2) OR
        (schema_version = 3 AND calculator_version = 3 AND digest_version = 3)
    ),
    DROP CONSTRAINT matching_fallback_calculation_receipt_calculator_version_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_calculator_version_check CHECK (
        (schema_version = 2 AND calculator_version = 1 AND digest_version = 2) OR
        (schema_version = 3 AND calculator_version = 3 AND digest_version = 3)
    ),
    DROP CONSTRAINT matching_fallback_calculation_receipt_digest_version_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_digest_version_check CHECK (
        (schema_version = 2 AND calculator_version = 1 AND digest_version = 2) OR
        (schema_version = 3 AND calculator_version = 3 AND digest_version = 3)
    );
