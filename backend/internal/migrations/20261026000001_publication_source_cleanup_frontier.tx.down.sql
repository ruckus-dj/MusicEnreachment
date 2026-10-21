DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM public.source_cleanup_explicit_retry WHERE frontier_revision IS NOT NULL)
     OR EXISTS (SELECT 1 FROM public.source_cleanup_recovery_state)
     OR EXISTS (SELECT 1 FROM public.source_cleanup_frontier_transfer_proof) THEN
    RAISE EXCEPTION 'cannot roll back pinned source cleanup recovery frontier';
  END IF;
END $$;

CREATE OR REPLACE FUNCTION public.protect_source_cleanup_retry_resolution() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE retry_state text;
BEGIN
    IF TG_OP='DELETE' THEN
        SELECT operation.state INTO retry_state
        FROM public.source_cleanup_explicit_retry grant_row
        JOIN public.operation ON operation.id=grant_row.recovery_operation_id
        WHERE grant_row.id=OLD.retry_id;
        IF OLD.completed_at IS NULL OR OLD.resolution_kind IS NULL OR retry_state IS NULL OR retry_state IN ('queued','running') THEN
            RAISE EXCEPTION 'active source cleanup retry resolution start is immutable' USING ERRCODE='23514';
        END IF;
        RETURN OLD;
    END IF;
    IF ROW(NEW.retry_id, NEW.item_id, NEW.recovery_operation_id, NEW.application_attempt,
        NEW.river_job_id, NEW.claim_uuid, NEW.started_at)
       IS DISTINCT FROM ROW(OLD.retry_id, OLD.item_id, OLD.recovery_operation_id, OLD.application_attempt,
        OLD.river_job_id, OLD.claim_uuid, OLD.started_at) THEN
        RAISE EXCEPTION 'source cleanup retry resolution identity and start are immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.completed_at IS NOT NULL OR OLD.resolution_kind IS NOT NULL THEN
        RAISE EXCEPTION 'completed source cleanup retry resolution is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.completed_at IS NULL OR NEW.resolution_kind IS NULL THEN
        RAISE EXCEPTION 'source cleanup retry resolution completion must be atomic' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;

DROP TRIGGER source_cleanup_frontier_transfer_proof_consumed ON public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER source_cleanup_frontier_transfer_proof_canonical ON public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER source_cleanup_retry_grant_frontier_coverage ON public.source_cleanup_explicit_retry;
DROP TRIGGER source_cleanup_recovery_item_coverage ON public.source_cleanup_recovery_item;
DROP TRIGGER source_cleanup_recovery_state_coverage ON public.source_cleanup_recovery_state;
DROP FUNCTION public.finish_source_cleanup_frontier_transfer_proof();
DROP FUNCTION public.source_cleanup_frontier_transfer_proof(uuid,uuid);
DROP FUNCTION public.validate_source_cleanup_frontier_transfer_proof();
DROP FUNCTION public.compute_source_cleanup_frontier_transfer_proof(uuid,uuid);
DROP TABLE public.source_cleanup_frontier_transfer_proof;
DROP TRIGGER source_cleanup_retry_frontier_immutable ON public.source_cleanup_explicit_retry;
DROP FUNCTION public.protect_source_cleanup_retry_frontier();
ALTER TABLE public.source_cleanup_explicit_retry DROP CONSTRAINT source_cleanup_retry_frontier_revision_fkey;
ALTER TABLE public.source_cleanup_explicit_retry DROP COLUMN frontier_revision;
DROP FUNCTION public.check_source_cleanup_recovery_coverage();
DROP TRIGGER source_cleanup_recovery_item_protected ON public.source_cleanup_recovery_item;
DROP FUNCTION public.protect_source_cleanup_recovery_item();
DROP TRIGGER source_cleanup_recovery_state_protected ON public.source_cleanup_recovery_state;
DROP FUNCTION public.protect_source_cleanup_recovery_state();
DROP TABLE public.source_cleanup_recovery_item;
DROP TABLE public.source_cleanup_recovery_state;
