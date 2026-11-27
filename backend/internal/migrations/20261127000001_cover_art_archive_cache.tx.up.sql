-- Durable Cover Art Archive cache keyed by exact MusicBrainz release MBID.
-- Front availability and complete-gallery availability are independent: a
-- failed gallery listing must not invalidate a known front image, and a failed
-- front fetch must not erase a complete gallery. "unchecked" includes failed
-- attempts and is deliberately distinct from a definitive absence.

CREATE TABLE public.cover_art_archive_release (
    release_mbid uuid NOT NULL,
    front_status text NOT NULL DEFAULT 'unchecked',
    front_image_id text,
    front_data bytea,
    front_mime text,
    front_sha256 bytea,
    front_size_bytes bigint,
    front_checked_at timestamp with time zone,
    front_error text,
    gallery_status text NOT NULL DEFAULT 'unchecked',
    gallery_listed_at timestamp with time zone,
    gallery_checked_at timestamp with time zone,
    gallery_error text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cover_art_archive_release_pkey PRIMARY KEY (release_mbid),
    CONSTRAINT cover_art_archive_release_front_status_check CHECK (front_status IN ('unchecked', 'present', 'absent')),
    CONSTRAINT cover_art_archive_release_gallery_status_check CHECK (gallery_status IN ('unchecked', 'complete')),
    CONSTRAINT cover_art_archive_release_front_tuple_check CHECK (
        (front_status = 'unchecked'
            AND front_image_id IS NULL AND front_data IS NULL AND front_mime IS NULL
            AND front_sha256 IS NULL AND front_size_bytes IS NULL AND front_checked_at IS NULL)
        OR (front_status = 'absent'
            AND front_image_id IS NULL AND front_data IS NULL AND front_mime IS NULL
            AND front_sha256 IS NULL AND front_size_bytes IS NULL AND front_checked_at IS NOT NULL)
        OR (front_status = 'present'
            AND front_data IS NOT NULL AND octet_length(front_data) > 0
            AND front_mime IN ('image/png', 'image/jpeg')
            AND front_sha256 IS NOT NULL AND octet_length(front_sha256) = 32
            AND front_sha256 = sha256(front_data)
            AND front_size_bytes = octet_length(front_data) AND front_size_bytes > 0
            AND front_checked_at IS NOT NULL)
    ),
    CONSTRAINT cover_art_archive_release_gallery_complete_check CHECK (
        (gallery_status = 'unchecked' AND gallery_checked_at IS NULL)
        OR (gallery_status = 'complete' AND gallery_checked_at IS NOT NULL AND gallery_listed_at IS NOT NULL)
    ),
    CONSTRAINT cover_art_archive_release_front_error_check CHECK (front_error IS NULL OR length(front_error) > 0),
    CONSTRAINT cover_art_archive_release_gallery_error_check CHECK (gallery_error IS NULL OR length(gallery_error) > 0)
);

-- One row per image ever listed for a release. `listed` follows the latest
-- successful complete listing while bytes and download diagnostics survive
-- listing failures, reorderings and temporary disappearance from the listing.
CREATE TABLE public.cover_art_archive_image (
    release_mbid uuid NOT NULL,
    image_id text NOT NULL,
    position integer NOT NULL DEFAULT 0,
    front boolean NOT NULL DEFAULT false,
    back boolean NOT NULL DEFAULT false,
    types jsonb NOT NULL DEFAULT '[]'::jsonb,
    comment text NOT NULL DEFAULT '',
    thumbnail_url text NOT NULL DEFAULT '',
    listed boolean NOT NULL DEFAULT true,
    data bytea,
    mime text,
    sha256 bytea,
    size_bytes bigint,
    downloaded_at timestamp with time zone,
    download_error text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cover_art_archive_image_pkey PRIMARY KEY (release_mbid, image_id),
    CONSTRAINT cover_art_archive_image_release_fkey FOREIGN KEY (release_mbid)
        REFERENCES public.cover_art_archive_release(release_mbid) ON DELETE CASCADE,
    CONSTRAINT cover_art_archive_image_id_check CHECK (image_id ~ '^[1-9][0-9]*$'),
    CONSTRAINT cover_art_archive_image_position_check CHECK (position >= 0),
    CONSTRAINT cover_art_archive_image_types_check CHECK (jsonb_typeof(types) = 'array'),
    CONSTRAINT cover_art_archive_image_data_check CHECK (
        (data IS NULL AND mime IS NULL AND sha256 IS NULL AND size_bytes IS NULL AND downloaded_at IS NULL)
        OR (data IS NOT NULL AND octet_length(data) > 0
            AND mime IN ('image/png', 'image/jpeg')
            AND sha256 IS NOT NULL AND octet_length(sha256) = 32
            AND sha256 = sha256(data)
            AND size_bytes = octet_length(data) AND size_bytes > 0
            AND downloaded_at IS NOT NULL)
    ),
    CONSTRAINT cover_art_archive_image_download_error_check CHECK (download_error IS NULL OR length(download_error) > 0)
);
