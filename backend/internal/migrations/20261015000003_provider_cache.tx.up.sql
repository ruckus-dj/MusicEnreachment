-- Fresh-instance baseline: provider cache.
-- Later baselines attach cross-domain foreign keys and guards where necessary.
SET LOCAL check_function_bodies = false;

-- sequence: provider_fetch_order_seq
CREATE SEQUENCE public.provider_fetch_order_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

-- table: provider
CREATE TABLE public.provider (
    id uuid NOT NULL,
    code text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_code_check CHECK ((length(btrim(code)) > 0))
);

-- table: provider_artist
CREATE TABLE public.provider_artist (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_key text NOT NULL,
    name text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_artist_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT provider_artist_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

-- table: provider_recording
CREATE TABLE public.provider_recording (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_key text NOT NULL,
    title text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_recording_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT provider_recording_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

-- table: provider_release
CREATE TABLE public.provider_release (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_key text NOT NULL,
    title text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_release_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT provider_release_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

-- table: provider_release_artist_credit
CREATE TABLE public.provider_release_artist_credit (
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint DEFAULT 0 NOT NULL,
    fetch_order bigint DEFAULT 0 NOT NULL,
    CONSTRAINT provider_release_artist_credit_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_release_artist_credit_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_release_artist_credit_position_check CHECK (("position" >= 0))
);

-- table: provider_release_track
CREATE TABLE public.provider_release_track (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    recording_id uuid NOT NULL,
    medium integer,
    "position" integer,
    displayed_number text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint DEFAULT 0 NOT NULL,
    fetch_order bigint DEFAULT 0 NOT NULL,
    CONSTRAINT provider_release_track_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_release_track_check CHECK ((present = ((medium IS NOT NULL) AND ("position" IS NOT NULL)))),
    CONSTRAINT provider_release_track_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_release_track_medium_check CHECK ((medium >= 0)),
    CONSTRAINT provider_release_track_position_check CHECK (("position" >= 0))
);

-- table: provider_response_cache
CREATE TABLE public.provider_response_cache (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    cache_key text NOT NULL,
    payload jsonb,
    fetched_at timestamp with time zone,
    revision text,
    generation bigint DEFAULT 0 NOT NULL,
    fetch_order bigint DEFAULT 0 NOT NULL,
    CONSTRAINT provider_response_cache_check CHECK (((payload IS NULL) = (fetched_at IS NULL))),
    CONSTRAINT provider_response_cache_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_response_cache_generation_check CHECK ((generation >= 0)),
    CONSTRAINT provider_response_cache_payload_check CHECK (((payload IS NULL) OR (jsonb_typeof(payload) = ANY (ARRAY['object'::text, 'array'::text]))))
);

-- table: provider_source
CREATE TABLE public.provider_source (
    id uuid NOT NULL,
    provider_id uuid NOT NULL,
    namespace text NOT NULL,
    endpoint text NOT NULL,
    configuration_identity text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_source_namespace_check CHECK ((length(btrim(namespace)) > 0))
);

-- table: provider_source_entity
CREATE TABLE public.provider_source_entity (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    entity_kind text NOT NULL,
    provider_key text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    field_evidence jsonb DEFAULT '{}'::jsonb NOT NULL,
    raw_source jsonb DEFAULT '{}'::jsonb NOT NULL,
    authority smallint NOT NULL,
    complete boolean DEFAULT false NOT NULL,
    fetch_order bigint NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_source_entity_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_source_entity_entity_kind_check CHECK ((entity_kind = ANY (ARRAY['artist'::text, 'release'::text, 'recording'::text]))),
    CONSTRAINT provider_source_entity_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_source_entity_field_evidence_check CHECK ((jsonb_typeof(field_evidence) = 'object'::text)),
    CONSTRAINT provider_source_entity_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text)),
    CONSTRAINT provider_source_entity_provider_key_check CHECK ((length(btrim(provider_key)) > 0)),
    CONSTRAINT provider_source_entity_raw_source_check CHECK ((jsonb_typeof(raw_source) = ANY (ARRAY['object'::text, 'array'::text])))
);

-- table: provider_source_release_artist_credit
CREATE TABLE public.provider_source_release_artist_credit (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint NOT NULL,
    fetch_order bigint NOT NULL,
    CONSTRAINT provider_source_release_artist_credit_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_source_release_artist_credit_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_source_release_artist_credit_position_check CHECK (("position" >= 0))
);

-- table: provider_source_release_track
CREATE TABLE public.provider_source_release_track (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    track_id uuid NOT NULL,
    release_id uuid NOT NULL,
    recording_id uuid NOT NULL,
    medium integer NOT NULL,
    "position" integer NOT NULL,
    displayed_number text NOT NULL,
    present boolean DEFAULT true NOT NULL,
    authority smallint NOT NULL,
    fetch_order bigint NOT NULL,
    CONSTRAINT provider_source_release_track_authority_check CHECK ((authority >= 0)),
    CONSTRAINT provider_source_release_track_fetch_order_check CHECK ((fetch_order >= 0)),
    CONSTRAINT provider_source_release_track_medium_check CHECK ((medium >= 0)),
    CONSTRAINT provider_source_release_track_position_check CHECK (("position" >= 0))
);

-- constraint: provider provider_code_key
ALTER TABLE ONLY public.provider
    ADD CONSTRAINT provider_code_key UNIQUE (code);

-- constraint: provider provider_pkey
ALTER TABLE ONLY public.provider
    ADD CONSTRAINT provider_pkey PRIMARY KEY (id);

-- constraint: provider_artist provider_artist_pkey
ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_pkey PRIMARY KEY (id);

-- constraint: provider_artist provider_artist_provider_id_id_key
ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_provider_id_id_key UNIQUE (provider_id, id);

-- constraint: provider_artist provider_artist_provider_id_provider_key_key
ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_provider_id_provider_key_key UNIQUE (provider_id, provider_key);

-- constraint: provider_recording provider_recording_pkey
ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_pkey PRIMARY KEY (id);

-- constraint: provider_recording provider_recording_provider_id_id_key
ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_provider_id_id_key UNIQUE (provider_id, id);

-- constraint: provider_recording provider_recording_provider_id_provider_key_key
ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_provider_id_provider_key_key UNIQUE (provider_id, provider_key);

-- constraint: provider_release provider_release_pkey
ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_pkey PRIMARY KEY (id);

-- constraint: provider_release provider_release_provider_id_id_key
ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_provider_id_id_key UNIQUE (provider_id, id);

-- constraint: provider_release provider_release_provider_id_provider_key_key
ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_provider_id_provider_key_key UNIQUE (provider_id, provider_key);

-- constraint: provider_release_artist_credit provider_release_artist_credit_pkey
ALTER TABLE ONLY public.provider_release_artist_credit
    ADD CONSTRAINT provider_release_artist_credit_pkey PRIMARY KEY (provider_id, release_id, "position");

-- constraint: provider_release_track provider_release_track_match_target
ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_match_target UNIQUE (provider_id, release_id, id);

-- constraint: provider_release_track provider_release_track_pkey
ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_pkey PRIMARY KEY (id);

-- constraint: provider_release_track provider_release_track_provider_id_id_key
ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_provider_id_id_key UNIQUE (provider_id, id);

-- constraint: provider_release_track provider_release_track_provider_id_release_id_medium_positi_key
ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_provider_id_release_id_medium_positi_key UNIQUE (provider_id, release_id, medium, "position") DEFERRABLE INITIALLY DEFERRED;

-- constraint: provider_response_cache provider_response_cache_pkey
ALTER TABLE ONLY public.provider_response_cache
    ADD CONSTRAINT provider_response_cache_pkey PRIMARY KEY (provider_source_id, cache_key);

-- constraint: provider_source provider_source_pkey
ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_pkey PRIMARY KEY (id);

-- constraint: provider_source provider_source_provider_id_id_key
ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_provider_id_id_key UNIQUE (provider_id, id);

-- constraint: provider_source provider_source_provider_id_namespace_key
ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_provider_id_namespace_key UNIQUE (provider_id, namespace);

-- constraint: provider_source_entity provider_source_entity_pkey
ALTER TABLE ONLY public.provider_source_entity
    ADD CONSTRAINT provider_source_entity_pkey PRIMARY KEY (provider_source_id, entity_kind, provider_key);

-- constraint: provider_source_release_artist_credit provider_source_release_artist_credit_pkey
ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artist_credit_pkey PRIMARY KEY (provider_source_id, release_id, "position");

-- constraint: provider_source_release_track provider_source_release_track_pkey
ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_pkey PRIMARY KEY (provider_source_id, track_id);

-- index: provider_source_entity_provider_key_idx
CREATE INDEX provider_source_entity_provider_key_idx ON public.provider_source_entity USING btree (provider_id, entity_kind, provider_key);

-- index: provider_source_release_track_position_idx
CREATE UNIQUE INDEX provider_source_release_track_position_idx ON public.provider_source_release_track USING btree (provider_source_id, release_id, medium, "position") WHERE present;

-- fk constraint: operation operation_provider_source_id_fkey
ALTER TABLE ONLY public.operation
    ADD CONSTRAINT operation_provider_source_id_fkey FOREIGN KEY (provider_source_id) REFERENCES public.provider_source(id) ON DELETE RESTRICT;

-- fk constraint: provider_artist provider_artist_provider_id_fkey
ALTER TABLE ONLY public.provider_artist
    ADD CONSTRAINT provider_artist_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

-- fk constraint: provider_recording provider_recording_provider_id_fkey
ALTER TABLE ONLY public.provider_recording
    ADD CONSTRAINT provider_recording_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

-- fk constraint: provider_release provider_release_provider_id_fkey
ALTER TABLE ONLY public.provider_release
    ADD CONSTRAINT provider_release_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

-- fk constraint: provider_release_artist_credit provider_release_artist_credit_provider_id_artist_id_fkey
ALTER TABLE ONLY public.provider_release_artist_credit
    ADD CONSTRAINT provider_release_artist_credit_provider_id_artist_id_fkey FOREIGN KEY (provider_id, artist_id) REFERENCES public.provider_artist(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_release_artist_credit provider_release_artist_credit_provider_id_release_id_fkey
ALTER TABLE ONLY public.provider_release_artist_credit
    ADD CONSTRAINT provider_release_artist_credit_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_release_track provider_release_track_provider_id_recording_id_fkey
ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_provider_id_recording_id_fkey FOREIGN KEY (provider_id, recording_id) REFERENCES public.provider_recording(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_release_track provider_release_track_provider_id_release_id_fkey
ALTER TABLE ONLY public.provider_release_track
    ADD CONSTRAINT provider_release_track_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_response_cache provider_response_cache_provider_id_provider_source_id_fkey
ALTER TABLE ONLY public.provider_response_cache
    ADD CONSTRAINT provider_response_cache_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

-- fk constraint: provider_source provider_source_provider_id_fkey
ALTER TABLE ONLY public.provider_source
    ADD CONSTRAINT provider_source_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES public.provider(id) ON DELETE RESTRICT;

-- fk constraint: provider_source_entity provider_source_entity_provider_id_provider_source_id_fkey
ALTER TABLE ONLY public.provider_source_entity
    ADD CONSTRAINT provider_source_entity_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

-- fk constraint: provider_source_release_artist_credit provider_source_release_artis_provider_id_provider_source__fkey
ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artis_provider_id_provider_source__fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

-- fk constraint: provider_source_release_artist_credit provider_source_release_artist_cred_provider_id_release_id_fkey
ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artist_cred_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_source_release_artist_credit provider_source_release_artist_credi_provider_id_artist_id_fkey
ALTER TABLE ONLY public.provider_source_release_artist_credit
    ADD CONSTRAINT provider_source_release_artist_credi_provider_id_artist_id_fkey FOREIGN KEY (provider_id, artist_id) REFERENCES public.provider_artist(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_source_release_track provider_source_release_track_provider_id_provider_source__fkey
ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_provider_source__fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;

-- fk constraint: provider_source_release_track provider_source_release_track_provider_id_recording_id_fkey
ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_recording_id_fkey FOREIGN KEY (provider_id, recording_id) REFERENCES public.provider_recording(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_source_release_track provider_source_release_track_provider_id_release_id_fkey
ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_source_release_track provider_source_release_track_provider_id_track_id_fkey
ALTER TABLE ONLY public.provider_source_release_track
    ADD CONSTRAINT provider_source_release_track_provider_id_track_id_fkey FOREIGN KEY (provider_id, track_id) REFERENCES public.provider_release_track(provider_id, id) ON DELETE RESTRICT;
