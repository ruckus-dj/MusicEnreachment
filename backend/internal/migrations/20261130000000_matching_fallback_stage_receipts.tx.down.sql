DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_fallback_search_receipt WHERE stage_index <> 0) OR
       EXISTS (SELECT 1 FROM matching_fallback_lookup_receipt WHERE stage_index <> 0) OR
       EXISTS (SELECT 1 FROM matching_fallback_calculation_receipt WHERE stage_index <> 0) OR
       EXISTS (SELECT 1 FROM matching_fallback_calculation_receipt
               WHERE schema_version <> 2 OR calculator_version <> 1 OR digest_version <> 2) OR
       EXISTS (
           SELECT 1 FROM matching_acquisition_phase
           WHERE phase IN ('fallback_release_hydration', 'fallback_applicability')
             AND (input ? 'stage_index' AND (input->>'stage_index')::integer <> 0 OR
                  output ? 'stage_index' AND (output->>'stage_index')::integer <> 0)
       ) THEN
         RAISE EXCEPTION 'cannot roll back populated nonzero fallback stage or version-3 calculation receipts';
    END IF;
END;
$$;

ALTER TABLE matching_fallback_search_receipt
    DROP CONSTRAINT matching_fallback_search_receipt_stage_index_check,
    ADD CONSTRAINT matching_fallback_search_receipt_stage_index_check CHECK (stage_index = 0);

ALTER TABLE matching_fallback_calculation_receipt
    DROP CONSTRAINT matching_fallback_calculation_receipt_stage_index_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_stage_index_check CHECK (stage_index = 0),
    DROP CONSTRAINT matching_fallback_calculation_receipt_schema_version_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_schema_version_check CHECK (schema_version = 2),
    DROP CONSTRAINT matching_fallback_calculation_receipt_calculator_version_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_calculator_version_check CHECK (calculator_version = 1),
    DROP CONSTRAINT matching_fallback_calculation_receipt_digest_version_check,
    ADD CONSTRAINT matching_fallback_calculation_receipt_digest_version_check CHECK (digest_version = 2);
