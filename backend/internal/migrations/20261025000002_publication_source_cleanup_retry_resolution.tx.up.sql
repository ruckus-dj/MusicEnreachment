ALTER TABLE public.source_cleanup_explicit_retry
    ADD CONSTRAINT source_cleanup_explicit_retry_consumed_resolution_key
    UNIQUE (id, recovery_operation_id, consumed_application_attempt, consumed_river_job_id, consumed_claim_uuid);

CREATE TABLE public.source_cleanup_explicit_retry_resolution (
    retry_id uuid NOT NULL,
    item_id uuid NOT NULL,
    recovery_operation_id uuid NOT NULL,
    application_attempt integer NOT NULL CHECK (application_attempt > 0),
    river_job_id bigint NOT NULL CHECK (river_job_id > 0),
    claim_uuid uuid NOT NULL CHECK (claim_uuid <> '00000000-0000-0000-0000-000000000000'::uuid),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    resolution_kind text,
    PRIMARY KEY (retry_id, item_id),
    CONSTRAINT source_cleanup_retry_resolution_item_fkey
        FOREIGN KEY (retry_id, item_id)
        REFERENCES public.source_cleanup_explicit_retry_item(retry_id, item_id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_retry_resolution_consumed_grant_fkey
        FOREIGN KEY (retry_id, recovery_operation_id, application_attempt, river_job_id, claim_uuid)
        REFERENCES public.source_cleanup_explicit_retry(id, recovery_operation_id, consumed_application_attempt, consumed_river_job_id, consumed_claim_uuid) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_retry_resolution_new_claim_fkey
        FOREIGN KEY (recovery_operation_id, application_attempt, river_job_id, claim_uuid)
        REFERENCES public.publication_execution_claim(operation_id, application_attempt, river_job_id, claim_uuid) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_retry_resolution_completion_check CHECK (
        (
            (completed_at IS NULL AND resolution_kind IS NULL)
            OR
            (completed_at IS NOT NULL AND resolution_kind IS NOT NULL
             AND completed_at >= started_at
             AND resolution_kind IN ('native_removed','absent_after_explicit_retry','original_native_completed'))
        ) IS TRUE
    )
);

CREATE FUNCTION public.protect_source_cleanup_retry_resolution() RETURNS trigger
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

CREATE TRIGGER source_cleanup_retry_resolution_immutable
BEFORE UPDATE OR DELETE ON public.source_cleanup_explicit_retry_resolution
FOR EACH ROW EXECUTE FUNCTION public.protect_source_cleanup_retry_resolution();
