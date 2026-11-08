-- Restoring the composite reference-target FKs is impossible while any credit or
-- track points at a record owned by another provider, so refuse before the
-- destructive DDL below.
DO $guard$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.provider_release_artist_credit credit
        WHERE NOT EXISTS (
            SELECT 1
            FROM public.provider_artist artist
            WHERE artist.id = credit.artist_id
              AND artist.provider_id = credit.provider_id
        )
    ) THEN
        RAISE EXCEPTION 'cannot roll back local provider catalog: artist credits reference records owned by another provider';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.provider_release_track track
        WHERE NOT EXISTS (
            SELECT 1
            FROM public.provider_recording recording
            WHERE recording.id = track.recording_id
              AND recording.provider_id = track.provider_id
        )
    ) THEN
        RAISE EXCEPTION 'cannot roll back local provider catalog: release tracks reference records owned by another provider';
    END IF;
END;
$guard$;

ALTER TABLE public.provider_release_track
    DROP CONSTRAINT provider_release_track_metadata_check,
    DROP COLUMN metadata,
    DROP CONSTRAINT provider_release_track_recording_id_fkey,
    ADD CONSTRAINT provider_release_track_provider_id_recording_id_fkey FOREIGN KEY (provider_id, recording_id) REFERENCES public.provider_recording(provider_id, id) ON DELETE RESTRICT;

ALTER TABLE public.provider_release_artist_credit
    DROP CONSTRAINT provider_release_artist_credit_artist_id_fkey,
    ADD CONSTRAINT provider_release_artist_credit_provider_id_artist_id_fkey FOREIGN KEY (provider_id, artist_id) REFERENCES public.provider_artist(provider_id, id) ON DELETE RESTRICT,
    DROP COLUMN credit_name;

DELETE FROM public.provider WHERE id='00000000-0000-4000-8000-000000000001' AND code='local';
