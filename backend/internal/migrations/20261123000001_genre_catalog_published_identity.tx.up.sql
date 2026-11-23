-- Setup requires a catalog published for the exact verified MusicBrainz
-- configuration. configuration_identity remains the last attempt fence; this
-- column records only the identity whose complete catalog is currently served.
ALTER TABLE public.genre_catalog
    ADD COLUMN published_configuration_identity text;
