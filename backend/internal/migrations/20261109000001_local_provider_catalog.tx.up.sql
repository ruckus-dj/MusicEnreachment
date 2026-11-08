-- Keep a stable, server-owned provider namespace for operator-managed catalog records.
INSERT INTO public.provider(id, code)
VALUES ('00000000-0000-4000-8000-000000000001', 'local');

-- Provider ownership remains on the referencing row via the composite owner FKs,
-- while graph references may point at records owned by another provider. Only
-- the reference-target FKs relax; the (provider_id, release_id) owner FKs stay
-- composite so a credit or track cannot leave its release's provider.
ALTER TABLE public.provider_release_artist_credit
    DROP CONSTRAINT provider_release_artist_credit_provider_id_artist_id_fkey,
    ADD CONSTRAINT provider_release_artist_credit_artist_id_fkey FOREIGN KEY (artist_id) REFERENCES public.provider_artist(id) ON DELETE RESTRICT,
    ADD COLUMN credit_name text DEFAULT ''::text NOT NULL;

ALTER TABLE public.provider_release_track
    DROP CONSTRAINT provider_release_track_provider_id_recording_id_fkey,
    ADD CONSTRAINT provider_release_track_recording_id_fkey FOREIGN KEY (recording_id) REFERENCES public.provider_recording(id) ON DELETE RESTRICT,
    ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT provider_release_track_metadata_check CHECK (jsonb_typeof(metadata) = 'object');
