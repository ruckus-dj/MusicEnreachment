-- Durable, bounded artist photo cache keyed by the exact MusicBrainz artist
-- identity and the MusicBrainz configuration identity that resolved it. The
-- stored bytes are the provider thumbnail, already validated in the service,
-- together with the verified Wikimedia Commons attribution. Serving reads this
-- table only; it never forwards the client to an arbitrary provider URL.

CREATE TABLE public.library_artist_photo (
    config_identity text NOT NULL,
    artist_mbid uuid NOT NULL,
    image_url text NOT NULL,
    source_url text NOT NULL,
    author text NOT NULL DEFAULT '',
    license text NOT NULL,
    license_url text,
    image_data bytea NOT NULL,
    image_mime text NOT NULL,
    image_sha256 bytea NOT NULL,
    image_size_bytes bigint NOT NULL,
    image_width integer NOT NULL,
    image_height integer NOT NULL,
    fetched_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_artist_photo_pkey PRIMARY KEY (config_identity, artist_mbid),
    CONSTRAINT library_artist_photo_config_identity_check CHECK (config_identity = btrim(config_identity) AND length(config_identity) > 0),
    CONSTRAINT library_artist_photo_image_url_check CHECK (image_url ~ '^https://(upload|thumb)\.wikimedia\.org/wikipedia/commons/'),
    CONSTRAINT library_artist_photo_source_url_check CHECK (source_url ~ '^https://commons\.wikimedia\.org/wiki/File:'),
    CONSTRAINT library_artist_photo_license_check CHECK (license = btrim(license) AND length(license) > 0),
    CONSTRAINT library_artist_photo_license_url_check CHECK (license_url IS NULL OR license_url ~ '^https://creativecommons\.org/(licenses|publicdomain)/'),
    CONSTRAINT library_artist_photo_data_check CHECK (
        octet_length(image_data) > 0 AND octet_length(image_data) <= 1048576
        AND image_mime IN ('image/png', 'image/jpeg', 'image/webp', 'image/gif')
        AND octet_length(image_sha256) = 32
        AND image_sha256 = sha256(image_data)
        AND image_size_bytes = octet_length(image_data)
        AND image_width > 0 AND image_width <= 400
        AND image_height > 0 AND image_height <= 400
    )
);
