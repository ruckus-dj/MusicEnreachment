-- Recompute the materialized groups once with the direct-folder analysis gate.
UPDATE public.incoming_grouping_state
SET revision = revision + 1, needs_refresh = true, updated_at = now()
WHERE singleton = true AND NOT needs_refresh;
