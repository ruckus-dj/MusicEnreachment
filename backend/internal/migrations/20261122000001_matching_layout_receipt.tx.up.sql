-- Whole-layout matching decisions replace the former edge-calculation arrays.
-- The receipt keeps its identity and input context; only the payload shape and
-- its version tuple change. The new ingress check is added NOT VALID so the
-- immutable v3 receipts already present on a development stand stay retained:
-- historical rows are never reinterpreted, and only new receipts must obey the
-- v4 contract.

ALTER TABLE public.matching_fallback_calculation_receipt
    RENAME COLUMN edge_calculations TO layout_decision;

ALTER TABLE public.matching_fallback_calculation_receipt
    ADD COLUMN decision_digest bytea;

ALTER TABLE public.matching_fallback_calculation_receipt
    DROP CONSTRAINT matching_fallback_calculation_receipt_calculator_version_check,
    DROP CONSTRAINT matching_fallback_calculation_receipt_digest_version_check,
    DROP CONSTRAINT matching_fallback_calculation_receipt_edge_calculations_check,
    DROP CONSTRAINT matching_fallback_calculation_receipt_schema_version_check;

ALTER TABLE public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_layout_decision_check CHECK (
        (schema_version = 4) IS TRUE
        AND (calculator_version = 4) IS TRUE
        AND (digest_version = 4) IS TRUE
        AND (jsonb_typeof(layout_decision) = 'object') IS TRUE
        AND ((layout_decision->>'schema_version') = '1') IS TRUE
        AND ((layout_decision->>'policy_version') = 'matching-layout-v1') IS TRUE
        AND (jsonb_typeof(layout_decision->'pair_threshold') = 'string') IS TRUE
        AND ((layout_decision->>'pair_threshold') ~ '^(0|1|0\.[0-9]*[1-9])$') IS TRUE
        AND ((layout_decision->>'status') IN ('selected', 'none', 'indeterminate')) IS TRUE
        AND (octet_length(decision_digest) = 32) IS TRUE
        AND (
            (
                ((layout_decision->>'status') = 'selected') IS TRUE
                AND (jsonb_typeof(layout_decision->'provider_release_id') = 'string') IS TRUE
                AND ((layout_decision->>'provider_release_id') <> '') IS TRUE
                AND (jsonb_typeof(layout_decision->'assignments') = 'array') IS TRUE
                AND (CASE WHEN jsonb_typeof(layout_decision->'assignments') = 'array'
                    THEN jsonb_array_length(layout_decision->'assignments') ELSE 0 END > 0) IS TRUE
            ) IS TRUE
            OR (
                ((layout_decision->>'status') IN ('none', 'indeterminate')) IS TRUE
                AND ((layout_decision->'provider_release_id') IS NULL) IS TRUE
                AND ((layout_decision->'assignments') IS NULL) IS TRUE
            ) IS TRUE
        ) IS TRUE
    ) NOT VALID;

-- One receipt per calculation stage: a stage index may not repeat within the
-- same acquisition revision of a run.
ALTER TABLE public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_run_id_stage_index_key UNIQUE (run_id, stage_index);
