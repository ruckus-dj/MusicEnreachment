DROP TABLE provider_source_entity;
ALTER TABLE provider_release_artist_credit DROP COLUMN fetch_order, DROP COLUMN authority;
ALTER TABLE provider_release_track DROP COLUMN fetch_order, DROP COLUMN authority;
ALTER TABLE provider_response_cache DROP COLUMN fetch_order;
DROP SEQUENCE provider_fetch_order_seq;
