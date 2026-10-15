-- A baseline rollback is destructive: reject populated application tables before DDL.
DO $baseline_rollback$
DECLARE
    relation_name text;
    populated boolean;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY['provider', 'provider_artist', 'provider_recording', 'provider_release', 'provider_release_artist_credit', 'provider_release_track', 'provider_response_cache', 'provider_source', 'provider_source_entity', 'provider_source_release_artist_credit', 'provider_source_release_track']
    LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I)', relation_name) INTO populated;
        IF populated THEN
            RAISE EXCEPTION 'cannot roll back populated application baseline: % contains data', relation_name;
        END IF;
    END LOOP;
END;
$baseline_rollback$;

ALTER TABLE public.provider_source_release_track DROP CONSTRAINT provider_source_release_track_provider_id_track_id_fkey;
ALTER TABLE public.provider_source_release_track DROP CONSTRAINT provider_source_release_track_provider_id_release_id_fkey;
ALTER TABLE public.provider_source_release_track DROP CONSTRAINT provider_source_release_track_provider_id_recording_id_fkey;
ALTER TABLE public.provider_source_release_track DROP CONSTRAINT provider_source_release_track_provider_id_provider_source__fkey;
ALTER TABLE public.provider_source_release_artist_credit DROP CONSTRAINT provider_source_release_artist_credi_provider_id_artist_id_fkey;
ALTER TABLE public.provider_source_release_artist_credit DROP CONSTRAINT provider_source_release_artist_cred_provider_id_release_id_fkey;
ALTER TABLE public.provider_source_release_artist_credit DROP CONSTRAINT provider_source_release_artis_provider_id_provider_source__fkey;
ALTER TABLE public.provider_source_entity DROP CONSTRAINT provider_source_entity_provider_id_provider_source_id_fkey;
ALTER TABLE public.provider_source DROP CONSTRAINT provider_source_provider_id_fkey;
ALTER TABLE public.provider_response_cache DROP CONSTRAINT provider_response_cache_provider_id_provider_source_id_fkey;
ALTER TABLE public.provider_release_track DROP CONSTRAINT provider_release_track_provider_id_release_id_fkey;
ALTER TABLE public.provider_release_track DROP CONSTRAINT provider_release_track_provider_id_recording_id_fkey;
ALTER TABLE public.provider_release_artist_credit DROP CONSTRAINT provider_release_artist_credit_provider_id_release_id_fkey;
ALTER TABLE public.provider_release_artist_credit DROP CONSTRAINT provider_release_artist_credit_provider_id_artist_id_fkey;
ALTER TABLE public.provider_release DROP CONSTRAINT provider_release_provider_id_fkey;
ALTER TABLE public.provider_recording DROP CONSTRAINT provider_recording_provider_id_fkey;
ALTER TABLE public.provider_artist DROP CONSTRAINT provider_artist_provider_id_fkey;
ALTER TABLE public.operation DROP CONSTRAINT operation_provider_source_id_fkey;

DROP TABLE public.provider_source_release_track;
DROP TABLE public.provider_source_release_artist_credit;
DROP TABLE public.provider_source_entity;
DROP TABLE public.provider_source;
DROP TABLE public.provider_response_cache;
DROP TABLE public.provider_release_track;
DROP TABLE public.provider_release_artist_credit;
DROP TABLE public.provider_release;
DROP TABLE public.provider_recording;
DROP TABLE public.provider_artist;
DROP TABLE public.provider;
DROP SEQUENCE public.provider_fetch_order_seq;
