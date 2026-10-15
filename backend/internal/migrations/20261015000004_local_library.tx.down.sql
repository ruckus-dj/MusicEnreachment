-- A baseline rollback is destructive: reject populated application tables before DDL.
DO $baseline_rollback$
DECLARE
    relation_name text;
    populated boolean;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY['collection', 'collection_artist', 'collection_release', 'local_artist', 'local_recording', 'local_release', 'local_track', 'provider_local_artist', 'provider_local_recording', 'provider_local_release', 'provider_local_track', 'recording_artist', 'release_artist', 'release_track', 'track_artist', 'track_source']
    LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I)', relation_name) INTO populated;
        IF populated THEN
            RAISE EXCEPTION 'cannot roll back populated application baseline: % contains data', relation_name;
        END IF;
    END LOOP;
END;
$baseline_rollback$;

ALTER TABLE public.track_source DROP CONSTRAINT track_source_track_id_fkey;
ALTER TABLE public.track_source DROP CONSTRAINT track_source_media_variant_id_fkey;
ALTER TABLE public.track_artist DROP CONSTRAINT track_artist_track_id_fkey;
ALTER TABLE public.track_artist DROP CONSTRAINT track_artist_artist_id_fkey;
ALTER TABLE public.release_track DROP CONSTRAINT release_track_track_id_fkey;
ALTER TABLE public.release_track DROP CONSTRAINT release_track_release_id_fkey;
ALTER TABLE public.release_track DROP CONSTRAINT release_track_recording_id_fkey;
ALTER TABLE public.release_artist DROP CONSTRAINT release_artist_release_id_fkey;
ALTER TABLE public.release_artist DROP CONSTRAINT release_artist_artist_id_fkey;
ALTER TABLE public.recording_artist DROP CONSTRAINT recording_artist_recording_id_fkey;
ALTER TABLE public.recording_artist DROP CONSTRAINT recording_artist_artist_id_fkey;
ALTER TABLE public.provider_local_track DROP CONSTRAINT provider_local_track_provider_id_provider_track_id_fkey;
ALTER TABLE public.provider_local_track DROP CONSTRAINT provider_local_track_local_track_id_fkey;
ALTER TABLE public.provider_local_release DROP CONSTRAINT provider_local_release_provider_id_provider_release_id_fkey;
ALTER TABLE public.provider_local_release DROP CONSTRAINT provider_local_release_local_release_id_fkey;
ALTER TABLE public.provider_local_recording DROP CONSTRAINT provider_local_recording_provider_id_provider_recording_id_fkey;
ALTER TABLE public.provider_local_recording DROP CONSTRAINT provider_local_recording_local_recording_id_fkey;
ALTER TABLE public.provider_local_artist DROP CONSTRAINT provider_local_artist_provider_id_provider_artist_id_fkey;
ALTER TABLE public.provider_local_artist DROP CONSTRAINT provider_local_artist_local_artist_id_fkey;
ALTER TABLE public.collection_release DROP CONSTRAINT collection_release_release_id_fkey;
ALTER TABLE public.collection_release DROP CONSTRAINT collection_release_collection_id_fkey;
ALTER TABLE public.collection_artist DROP CONSTRAINT collection_artist_collection_id_fkey;
ALTER TABLE public.collection_artist DROP CONSTRAINT collection_artist_artist_id_fkey;

DROP TABLE public.track_source;
DROP TABLE public.track_artist;
DROP TABLE public.release_track;
DROP TABLE public.release_artist;
DROP TABLE public.recording_artist;
DROP TABLE public.provider_local_track;
DROP TABLE public.provider_local_release;
DROP TABLE public.provider_local_recording;
DROP TABLE public.provider_local_artist;
DROP TABLE public.local_track;
DROP TABLE public.local_release;
DROP TABLE public.local_recording;
DROP TABLE public.local_artist;
DROP TABLE public.collection_release;
DROP TABLE public.collection_artist;
DROP TABLE public.collection;
