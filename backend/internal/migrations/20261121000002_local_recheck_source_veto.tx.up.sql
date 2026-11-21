-- Durable protection for exact sources of an automatic Local assignment while
-- the application rechecks and possibly republishes it. The protection is
-- intentionally independent of the replaceable incoming group.

-- table: matching_local_recheck
CREATE TABLE public.matching_local_recheck (
    id uuid NOT NULL,
    local_apply_operation_id uuid NOT NULL,
    state text NOT NULL,
    next_recheck_at timestamp with time zone NOT NULL,
    incumbent_snapshot jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT matching_local_recheck_pkey PRIMARY KEY (id),
    CONSTRAINT matching_local_recheck_local_apply_operation_key UNIQUE (local_apply_operation_id),
    CONSTRAINT matching_local_recheck_state_check CHECK (state IN ('waiting', 'publishing')),
    CONSTRAINT matching_local_recheck_incumbent_snapshot_check CHECK (
        (jsonb_typeof(incumbent_snapshot) = 'object') IS TRUE
        AND (incumbent_snapshot ? 'version') IS TRUE
        AND (jsonb_typeof(incumbent_snapshot->'version') = 'number') IS TRUE
        AND ((incumbent_snapshot->>'version') = '1') IS TRUE
    )
);

-- index: matching_local_recheck_due_idx
CREATE INDEX matching_local_recheck_due_idx ON public.matching_local_recheck USING btree (state, next_recheck_at);

-- table: matching_local_recheck_source
-- The identity witnesses are deliberately not foreign keys to source_location,
-- source_analysis_work or media_variant: ordinary inventory scans prune those
-- rows. The exact tuple is validated at admission, and the root FK blocks only
-- the lifecycle operation that must explicitly retire this veto first.
CREATE TABLE public.matching_local_recheck_source (
    recheck_id uuid NOT NULL,
    work_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    unlink_target text NOT NULL,
    CONSTRAINT matching_local_recheck_source_pkey PRIMARY KEY (recheck_id, work_id),
    CONSTRAINT matching_local_recheck_source_work_id_key UNIQUE (work_id),
    CONSTRAINT matching_local_recheck_source_source_location_id_key UNIQUE (source_location_id),
    CONSTRAINT matching_local_recheck_source_unlink_target_check CHECK (unlink_target <> '')
);

-- index: matching_local_recheck_source_root_idx
CREATE INDEX matching_local_recheck_source_root_idx ON public.matching_local_recheck_source USING btree (source_root_id);
CREATE INDEX matching_local_recheck_source_unlink_target_idx ON public.matching_local_recheck_source USING hash (unlink_target);

-- fk constraint: matching_local_recheck matching_local_recheck_local_apply_operation_fk
ALTER TABLE ONLY public.matching_local_recheck
    ADD CONSTRAINT matching_local_recheck_local_apply_operation_fk FOREIGN KEY (local_apply_operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: matching_local_recheck_source matching_local_recheck_source_recheck_fk
ALTER TABLE ONLY public.matching_local_recheck_source
    ADD CONSTRAINT matching_local_recheck_source_recheck_fk FOREIGN KEY (recheck_id) REFERENCES public.matching_local_recheck(id) ON DELETE CASCADE;

-- fk constraint: matching_local_recheck_source matching_local_recheck_source_root_fk
ALTER TABLE ONLY public.matching_local_recheck_source
    ADD CONSTRAINT matching_local_recheck_source_root_fk FOREIGN KEY (source_root_id) REFERENCES public.source_root(id) ON DELETE RESTRICT;

-- The incumbent evidence is a durable veto input, not editable progress state.
CREATE FUNCTION public.guard_matching_local_recheck_incumbent() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.local_apply_operation_id IS DISTINCT FROM OLD.local_apply_operation_id
       OR NEW.incumbent_snapshot IS DISTINCT FROM OLD.incumbent_snapshot THEN
        RAISE EXCEPTION 'matching Local recheck incumbent evidence is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER matching_local_recheck_incumbent_guard
    BEFORE UPDATE ON public.matching_local_recheck
    FOR EACH ROW EXECUTE FUNCTION public.guard_matching_local_recheck_incumbent();
