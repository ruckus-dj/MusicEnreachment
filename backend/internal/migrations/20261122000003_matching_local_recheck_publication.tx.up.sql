-- Durable membership tying one Local recheck to the publication operation that
-- replaces its predecessor output. The recheck remains the owner until it
-- settles; publication attempt and execution-claim rows are consumed on
-- settlement, so their identities are frozen evidence here rather than foreign
-- keys.

-- table: matching_local_recheck_publication
CREATE TABLE public.matching_local_recheck_publication (
    operation_id uuid NOT NULL,
    recheck_id uuid NOT NULL,
    output_epoch uuid NOT NULL,
    position_id uuid NOT NULL,
    candidate_generation uuid NOT NULL,
    predecessor_position_id uuid NOT NULL,
    predecessor_generation uuid NOT NULL,
    predecessor_claim_id uuid NOT NULL,
    same_path boolean NOT NULL,
    predecessor_snapshot jsonb NOT NULL,
    committed_at timestamp with time zone,
    candidate_payload_sha256 text,
    CONSTRAINT matching_local_recheck_publication_candidate_payload_check CHECK (
        candidate_payload_sha256 IS NULL OR (NOT same_path AND candidate_payload_sha256 ~ '^[0-9a-f]{64}$')
    ),
    CONSTRAINT matching_local_recheck_publication_pkey PRIMARY KEY (operation_id),
    CONSTRAINT matching_local_recheck_publication_predecessor_snapshot_check CHECK ((
        jsonb_typeof(predecessor_snapshot) = 'object'
        AND predecessor_snapshot->'version' = '1'::jsonb
        AND predecessor_snapshot->>'position_id' = predecessor_position_id::text
        AND predecessor_snapshot->>'output_epoch' = output_epoch::text
        AND predecessor_snapshot->>'generation' = predecessor_generation::text
        AND predecessor_snapshot->>'namespace_claim_id' = predecessor_claim_id::text
        AND jsonb_typeof(predecessor_snapshot->'relative_path') = 'string'
        AND predecessor_snapshot->>'relative_path' <> ''
        AND jsonb_typeof(predecessor_snapshot->'current') = 'object'
        AND predecessor_snapshot->'current'->>'generation' = predecessor_generation::text
        AND predecessor_snapshot->'current'->>'relative_path' = predecessor_snapshot->>'relative_path'
        AND (position_id <> predecessor_position_id OR same_path)
    ) IS TRUE)
);

-- index: matching_local_recheck_publication_recheck_idx
CREATE INDEX matching_local_recheck_publication_recheck_idx ON public.matching_local_recheck_publication USING btree (recheck_id);

-- index: matching_local_recheck_publication_live_predecessor_claim_key
-- A different-path member still consumes its predecessor after the member
-- commits: its recheck's complete-success barrier owns retirement.
CREATE UNIQUE INDEX matching_local_recheck_publication_live_predecessor_claim_key ON public.matching_local_recheck_publication USING btree (predecessor_claim_id) WHERE (committed_at IS NULL OR NOT same_path);

CREATE INDEX matching_local_recheck_publication_live_predecessor_position_idx ON public.matching_local_recheck_publication USING btree (predecessor_position_id, output_epoch) WHERE (committed_at IS NULL OR NOT same_path);

CREATE INDEX matching_local_recheck_publication_destination_position_idx ON public.matching_local_recheck_publication USING btree (position_id, output_epoch);

-- fk constraint: matching_local_recheck_publication matching_local_recheck_publication_operation_fk
ALTER TABLE ONLY public.matching_local_recheck_publication
    ADD CONSTRAINT matching_local_recheck_publication_operation_fk FOREIGN KEY (operation_id) REFERENCES public.operation(id) ON DELETE RESTRICT;

-- fk constraint: matching_local_recheck_publication matching_local_recheck_publication_recheck_fk
-- Rows are removed with their recheck owner when it settles.
ALTER TABLE ONLY public.matching_local_recheck_publication
    ADD CONSTRAINT matching_local_recheck_publication_recheck_fk FOREIGN KEY (recheck_id) REFERENCES public.matching_local_recheck(id) ON DELETE CASCADE;

-- fk constraint: matching_local_recheck_publication matching_local_recheck_publication_position_fk
ALTER TABLE ONLY public.matching_local_recheck_publication
    ADD CONSTRAINT matching_local_recheck_publication_position_fk FOREIGN KEY (position_id) REFERENCES public.release_track(id) ON DELETE RESTRICT;

-- The predecessor identity and frozen snapshot are a durable admission
-- decision. Only the one-time committed result receipt may be added.
CREATE FUNCTION public.guard_matching_local_recheck_publication() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.recheck_id IS DISTINCT FROM OLD.recheck_id
       OR NEW.output_epoch IS DISTINCT FROM OLD.output_epoch
       OR NEW.position_id IS DISTINCT FROM OLD.position_id
       OR NEW.candidate_generation IS DISTINCT FROM OLD.candidate_generation
       OR NEW.predecessor_position_id IS DISTINCT FROM OLD.predecessor_position_id
       OR NEW.predecessor_generation IS DISTINCT FROM OLD.predecessor_generation
       OR NEW.predecessor_claim_id IS DISTINCT FROM OLD.predecessor_claim_id
       OR NEW.same_path IS DISTINCT FROM OLD.same_path
       OR NEW.predecessor_snapshot IS DISTINCT FROM OLD.predecessor_snapshot
       OR (OLD.committed_at IS NOT NULL
           AND (NEW.committed_at IS DISTINCT FROM OLD.committed_at
                OR NEW.candidate_payload_sha256 IS DISTINCT FROM OLD.candidate_payload_sha256)) THEN
        RAISE EXCEPTION 'matching Local recheck publication membership is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER matching_local_recheck_publication_immutable
    BEFORE UPDATE ON public.matching_local_recheck_publication
    FOR EACH ROW EXECUTE FUNCTION public.guard_matching_local_recheck_publication();
