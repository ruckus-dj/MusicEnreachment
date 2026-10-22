ALTER TABLE public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_operation_position_key UNIQUE (operation_id, position_id);
ALTER TABLE public.release_track
    ADD CONSTRAINT release_track_id_release_key UNIQUE (id, release_id);

CREATE TABLE public.publication_attempt_cover_input (
    operation_id uuid NOT NULL,
    position_id uuid NOT NULL,
    release_id uuid NOT NULL,
    authority_revision bigint NOT NULL,
    authority_mode text NOT NULL,
    state text NOT NULL,
    generation uuid,
    image_bytes bytea,
    image_mime text,
    image_sha256 bytea,
    image_size bigint,
    CONSTRAINT publication_attempt_cover_input_pkey PRIMARY KEY (operation_id),
    CONSTRAINT publication_attempt_cover_input_attempt_fkey FOREIGN KEY (operation_id, position_id)
        REFERENCES public.track_publication_attempt(operation_id, position_id) ON DELETE CASCADE,
    CONSTRAINT publication_attempt_cover_input_release_position_fkey FOREIGN KEY (position_id, release_id)
        REFERENCES public.release_track(id, release_id) ON DELETE RESTRICT,
    CONSTRAINT publication_attempt_cover_input_revision_check CHECK (authority_revision > 0),
    CONSTRAINT publication_attempt_cover_input_mode_check CHECK (authority_mode IN ('automatic', 'manual')),
    CONSTRAINT publication_attempt_cover_input_state_check CHECK (state IN ('absent', 'value')),
    CONSTRAINT publication_attempt_cover_input_tuple_check CHECK ((
        (state = 'absent' AND generation IS NULL AND image_bytes IS NULL AND image_mime IS NULL AND image_sha256 IS NULL AND image_size IS NULL)
        OR
        (state = 'value' AND generation IS NOT NULL AND generation <> '00000000-0000-0000-0000-000000000000'
            AND image_bytes IS NOT NULL AND octet_length(image_bytes) > 0
            AND image_mime IN ('image/png', 'image/jpeg')
            AND image_sha256 IS NOT NULL AND octet_length(image_sha256) = 32
            AND image_sha256 = sha256(image_bytes)
            AND image_size = octet_length(image_bytes) AND image_size > 0)
    ) IS TRUE)
);

CREATE FUNCTION public.reject_publication_attempt_cover_input_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'publication attempt cover inputs are immutable';
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.track_publication_attempt
        WHERE operation_id = OLD.operation_id AND position_id = OLD.position_id
    ) THEN
        RAISE EXCEPTION 'cannot delete publication attempt cover input while its attempt exists';
    END IF;
    RETURN OLD;
END
$$;

CREATE TRIGGER publication_attempt_cover_input_immutable
    BEFORE DELETE OR UPDATE ON public.publication_attempt_cover_input
    FOR EACH ROW EXECUTE FUNCTION public.reject_publication_attempt_cover_input_mutation();
