DO $guard$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.matching_fallback_calculation_receipt
        WHERE schema_version = 4
           OR decision_digest IS NOT NULL
           OR (jsonb_typeof(layout_decision) = 'object') IS TRUE
    ) THEN
        RAISE EXCEPTION 'cannot roll back matching layout receipt schema while v4 layout decisions remain';
    END IF;
END;
$guard$;

ALTER TABLE public.matching_fallback_calculation_receipt
    DROP CONSTRAINT matching_fallback_calculation_receipt_layout_decision_check,
    DROP CONSTRAINT matching_fallback_calculation_receipt_run_id_stage_index_key,
    DROP COLUMN decision_digest;

ALTER TABLE public.matching_fallback_calculation_receipt
    RENAME COLUMN layout_decision TO edge_calculations;

ALTER TABLE public.matching_fallback_calculation_receipt
    ADD CONSTRAINT matching_fallback_calculation_receipt_calculator_version_check CHECK (((schema_version = 3) AND (calculator_version = 3) AND (digest_version = 3))),
    ADD CONSTRAINT matching_fallback_calculation_receipt_digest_version_check CHECK (((schema_version = 3) AND (calculator_version = 3) AND (digest_version = 3))),
    ADD CONSTRAINT matching_fallback_calculation_receipt_edge_calculations_check CHECK ((jsonb_typeof(edge_calculations) = 'array'::text)),
    ADD CONSTRAINT matching_fallback_calculation_receipt_schema_version_check CHECK (((schema_version = 3) AND (calculator_version = 3) AND (digest_version = 3)));
