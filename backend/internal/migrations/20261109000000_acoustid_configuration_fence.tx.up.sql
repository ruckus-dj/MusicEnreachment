-- AcoustID configuration identity is stored lazily in app_setting by the
-- atomic credential writer. Do not infer or backfill identity from a saved key.
SELECT 1;
