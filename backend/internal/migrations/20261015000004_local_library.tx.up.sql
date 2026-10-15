-- Fresh-instance baseline: local library.
-- Later baselines attach cross-domain foreign keys and guards where necessary.
SET LOCAL check_function_bodies = false;

-- table: collection
CREATE TABLE public.collection (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT collection_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT collection_metadata_version_check CHECK ((metadata_version >= 1))
);

-- table: collection_artist
CREATE TABLE public.collection_artist (
    collection_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    CONSTRAINT collection_artist_position_check CHECK (("position" >= 0))
);

-- table: collection_release
CREATE TABLE public.collection_release (
    collection_id uuid NOT NULL,
    "position" integer NOT NULL,
    release_id uuid NOT NULL,
    CONSTRAINT collection_release_position_check CHECK (("position" >= 0))
);

-- table: local_artist
CREATE TABLE public.local_artist (
    id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT local_artist_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_artist_metadata_version_check CHECK ((metadata_version >= 1))
);

-- table: local_recording
CREATE TABLE public.local_recording (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT local_recording_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_recording_metadata_version_check CHECK ((metadata_version >= 1))
);

-- table: local_release
CREATE TABLE public.local_release (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT local_release_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_release_metadata_version_check CHECK ((metadata_version >= 1))
);

-- table: local_track
CREATE TABLE public.local_track (
    id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    metadata_version integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT local_track_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT local_track_metadata_version_check CHECK ((metadata_version >= 1))
);

-- table: provider_local_artist
CREATE TABLE public.provider_local_artist (
    provider_id uuid NOT NULL,
    provider_artist_id uuid NOT NULL,
    local_artist_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_artist_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_artist_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

-- table: provider_local_recording
CREATE TABLE public.provider_local_recording (
    provider_id uuid NOT NULL,
    provider_recording_id uuid NOT NULL,
    local_recording_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_recording_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_recording_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

-- table: provider_local_release
CREATE TABLE public.provider_local_release (
    provider_id uuid NOT NULL,
    provider_release_id uuid NOT NULL,
    local_release_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_release_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_release_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

-- table: provider_local_track
CREATE TABLE public.provider_local_track (
    provider_id uuid NOT NULL,
    provider_track_id uuid NOT NULL,
    local_track_id uuid NOT NULL,
    method text NOT NULL,
    policy_version text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT provider_local_track_check CHECK ((((method = 'manual'::text) AND (policy_version IS NULL)) OR ((method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT provider_local_track_method_check CHECK ((method = ANY (ARRAY['manual'::text, 'automatic'::text])))
);

-- table: recording_artist
CREATE TABLE public.recording_artist (
    recording_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    CONSTRAINT recording_artist_position_check CHECK (("position" >= 0))
);

-- table: release_artist
CREATE TABLE public.release_artist (
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    CONSTRAINT release_artist_position_check CHECK (("position" >= 0))
);

-- table: release_track
CREATE TABLE public.release_track (
    release_id uuid NOT NULL,
    "position" integer NOT NULL,
    track_id uuid NOT NULL,
    recording_id uuid,
    medium integer DEFAULT 0 NOT NULL,
    displayed_number text DEFAULT ''::text NOT NULL,
    CONSTRAINT release_track_medium_check CHECK ((medium >= 0)),
    CONSTRAINT release_track_position_check CHECK (("position" >= 0))
);

-- table: track_artist
CREATE TABLE public.track_artist (
    track_id uuid NOT NULL,
    "position" integer NOT NULL,
    artist_id uuid NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    name_join_phrase text DEFAULT ''::text NOT NULL,
    CONSTRAINT track_artist_position_check CHECK (("position" >= 0))
);

-- table: track_source
CREATE TABLE public.track_source (
    id uuid NOT NULL,
    track_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    selected boolean DEFAULT false NOT NULL,
    compatible boolean DEFAULT true NOT NULL,
    decision_method text NOT NULL,
    policy_version text,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT track_source_check CHECK ((((decision_method = 'manual'::text) AND (policy_version IS NULL)) OR ((decision_method = 'automatic'::text) AND (policy_version IS NOT NULL) AND (btrim(policy_version, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'::text) <> ''::text)))),
    CONSTRAINT track_source_decision_method_check CHECK ((decision_method = ANY (ARRAY['manual'::text, 'automatic'::text]))),
    CONSTRAINT track_source_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text))
);

-- constraint: collection collection_pkey
ALTER TABLE ONLY public.collection
    ADD CONSTRAINT collection_pkey PRIMARY KEY (id);

-- constraint: collection_artist collection_artist_pkey
ALTER TABLE ONLY public.collection_artist
    ADD CONSTRAINT collection_artist_pkey PRIMARY KEY (collection_id, "position");

-- constraint: collection_release collection_release_pkey
ALTER TABLE ONLY public.collection_release
    ADD CONSTRAINT collection_release_pkey PRIMARY KEY (collection_id, "position");

-- constraint: local_artist local_artist_pkey
ALTER TABLE ONLY public.local_artist
    ADD CONSTRAINT local_artist_pkey PRIMARY KEY (id);

-- constraint: local_recording local_recording_pkey
ALTER TABLE ONLY public.local_recording
    ADD CONSTRAINT local_recording_pkey PRIMARY KEY (id);

-- constraint: local_release local_release_pkey
ALTER TABLE ONLY public.local_release
    ADD CONSTRAINT local_release_pkey PRIMARY KEY (id);

-- constraint: local_track local_track_pkey
ALTER TABLE ONLY public.local_track
    ADD CONSTRAINT local_track_pkey PRIMARY KEY (id);

-- constraint: provider_local_artist provider_local_artist_pkey
ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_pkey PRIMARY KEY (provider_id, provider_artist_id);

-- constraint: provider_local_artist provider_local_artist_provider_id_local_artist_id_key
ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_provider_id_local_artist_id_key UNIQUE (provider_id, local_artist_id);

-- constraint: provider_local_recording provider_local_recording_pkey
ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_pkey PRIMARY KEY (provider_id, provider_recording_id);

-- constraint: provider_local_recording provider_local_recording_provider_id_local_recording_id_key
ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_provider_id_local_recording_id_key UNIQUE (provider_id, local_recording_id);

-- constraint: provider_local_release provider_local_release_pkey
ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_pkey PRIMARY KEY (provider_id, provider_release_id);

-- constraint: provider_local_release provider_local_release_provider_id_local_release_id_key
ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_provider_id_local_release_id_key UNIQUE (provider_id, local_release_id);

-- constraint: provider_local_track provider_local_track_pkey
ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_pkey PRIMARY KEY (provider_id, provider_track_id);

-- constraint: provider_local_track provider_local_track_provider_id_local_track_id_key
ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_provider_id_local_track_id_key UNIQUE (provider_id, local_track_id);

-- constraint: recording_artist recording_artist_pkey
ALTER TABLE ONLY public.recording_artist
    ADD CONSTRAINT recording_artist_pkey PRIMARY KEY (recording_id, "position");

-- constraint: release_artist release_artist_pkey
ALTER TABLE ONLY public.release_artist
    ADD CONSTRAINT release_artist_pkey PRIMARY KEY (release_id, "position");

-- constraint: release_track release_track_pkey
ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_pkey PRIMARY KEY (release_id, medium, "position");

-- constraint: track_artist track_artist_pkey
ALTER TABLE ONLY public.track_artist
    ADD CONSTRAINT track_artist_pkey PRIMARY KEY (track_id, "position");

-- constraint: track_source track_source_pkey
ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_pkey PRIMARY KEY (id);

-- constraint: track_source track_source_track_id_media_variant_id_key
ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_track_id_media_variant_id_key UNIQUE (track_id, media_variant_id);

-- index: track_source_one_selected_idx
CREATE UNIQUE INDEX track_source_one_selected_idx ON public.track_source USING btree (track_id) WHERE selected;

-- fk constraint: collection_artist collection_artist_artist_id_fkey
ALTER TABLE ONLY public.collection_artist
    ADD CONSTRAINT collection_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

-- fk constraint: collection_artist collection_artist_collection_id_fkey
ALTER TABLE ONLY public.collection_artist
    ADD CONSTRAINT collection_artist_collection_id_fkey FOREIGN KEY (collection_id) REFERENCES public.collection(id) ON DELETE CASCADE;

-- fk constraint: collection_release collection_release_collection_id_fkey
ALTER TABLE ONLY public.collection_release
    ADD CONSTRAINT collection_release_collection_id_fkey FOREIGN KEY (collection_id) REFERENCES public.collection(id) ON DELETE CASCADE;

-- fk constraint: collection_release collection_release_release_id_fkey
ALTER TABLE ONLY public.collection_release
    ADD CONSTRAINT collection_release_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

-- fk constraint: provider_local_artist provider_local_artist_local_artist_id_fkey
ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_local_artist_id_fkey FOREIGN KEY (local_artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

-- fk constraint: provider_local_artist provider_local_artist_provider_id_provider_artist_id_fkey
ALTER TABLE ONLY public.provider_local_artist
    ADD CONSTRAINT provider_local_artist_provider_id_provider_artist_id_fkey FOREIGN KEY (provider_id, provider_artist_id) REFERENCES public.provider_artist(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_local_recording provider_local_recording_local_recording_id_fkey
ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_local_recording_id_fkey FOREIGN KEY (local_recording_id) REFERENCES public.local_recording(id) ON DELETE RESTRICT;

-- fk constraint: provider_local_recording provider_local_recording_provider_id_provider_recording_id_fkey
ALTER TABLE ONLY public.provider_local_recording
    ADD CONSTRAINT provider_local_recording_provider_id_provider_recording_id_fkey FOREIGN KEY (provider_id, provider_recording_id) REFERENCES public.provider_recording(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_local_release provider_local_release_local_release_id_fkey
ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_local_release_id_fkey FOREIGN KEY (local_release_id) REFERENCES public.local_release(id) ON DELETE RESTRICT;

-- fk constraint: provider_local_release provider_local_release_provider_id_provider_release_id_fkey
ALTER TABLE ONLY public.provider_local_release
    ADD CONSTRAINT provider_local_release_provider_id_provider_release_id_fkey FOREIGN KEY (provider_id, provider_release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: provider_local_track provider_local_track_local_track_id_fkey
ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_local_track_id_fkey FOREIGN KEY (local_track_id) REFERENCES public.local_track(id) ON DELETE RESTRICT;

-- fk constraint: provider_local_track provider_local_track_provider_id_provider_track_id_fkey
ALTER TABLE ONLY public.provider_local_track
    ADD CONSTRAINT provider_local_track_provider_id_provider_track_id_fkey FOREIGN KEY (provider_id, provider_track_id) REFERENCES public.provider_release_track(provider_id, id) ON DELETE RESTRICT;

-- fk constraint: recording_artist recording_artist_artist_id_fkey
ALTER TABLE ONLY public.recording_artist
    ADD CONSTRAINT recording_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

-- fk constraint: recording_artist recording_artist_recording_id_fkey
ALTER TABLE ONLY public.recording_artist
    ADD CONSTRAINT recording_artist_recording_id_fkey FOREIGN KEY (recording_id) REFERENCES public.local_recording(id) ON DELETE CASCADE;

-- fk constraint: release_artist release_artist_artist_id_fkey
ALTER TABLE ONLY public.release_artist
    ADD CONSTRAINT release_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

-- fk constraint: release_artist release_artist_release_id_fkey
ALTER TABLE ONLY public.release_artist
    ADD CONSTRAINT release_artist_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE CASCADE;

-- fk constraint: release_track release_track_recording_id_fkey
ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_recording_id_fkey FOREIGN KEY (recording_id) REFERENCES public.local_recording(id) ON DELETE RESTRICT;

-- fk constraint: release_track release_track_release_id_fkey
ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.local_release(id) ON DELETE CASCADE;

-- fk constraint: release_track release_track_track_id_fkey
ALTER TABLE ONLY public.release_track
    ADD CONSTRAINT release_track_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.local_track(id) ON DELETE RESTRICT;

-- fk constraint: track_artist track_artist_artist_id_fkey
ALTER TABLE ONLY public.track_artist
    ADD CONSTRAINT track_artist_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.local_artist(id) ON DELETE RESTRICT;

-- fk constraint: track_artist track_artist_track_id_fkey
ALTER TABLE ONLY public.track_artist
    ADD CONSTRAINT track_artist_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.local_track(id) ON DELETE CASCADE;

-- fk constraint: track_source track_source_media_variant_id_fkey
ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_media_variant_id_fkey FOREIGN KEY (media_variant_id) REFERENCES public.media_variant(id) ON DELETE RESTRICT;

-- fk constraint: track_source track_source_track_id_fkey
ALTER TABLE ONLY public.track_source
    ADD CONSTRAINT track_source_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.local_track(id) ON DELETE CASCADE;
