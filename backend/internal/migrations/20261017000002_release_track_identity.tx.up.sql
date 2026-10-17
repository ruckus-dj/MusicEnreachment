ALTER TABLE public.release_track
    ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();

ALTER TABLE public.release_track
    ADD CONSTRAINT release_track_id_key UNIQUE (id);
