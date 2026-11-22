-- Durable evidence that every accepted release-browse page for one
-- MusicBrainz artist was persisted before a first publication treats the
-- discography as complete. The row is written only after the service has
-- verified the provider's reported total against distinct accepted releases.

-- table: provider_artist_discography
CREATE TABLE public.provider_artist_discography (
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    configuration_identity text NOT NULL,
    artist_mbid uuid NOT NULL,
    release_count integer NOT NULL,
    completed_at timestamp with time zone NOT NULL,
    CONSTRAINT provider_artist_discography_configuration_identity_check CHECK ((length(btrim(configuration_identity)) > 0)),
    CONSTRAINT provider_artist_discography_release_count_check CHECK ((release_count >= 0)),
    CONSTRAINT provider_artist_discography_pkey PRIMARY KEY (provider_source_id, configuration_identity, artist_mbid)
);

-- fk constraint: provider_artist_discography provider_artist_discography_provider_id_provider_source_id_fkey
ALTER TABLE ONLY public.provider_artist_discography
    ADD CONSTRAINT provider_artist_discography_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE CASCADE;
