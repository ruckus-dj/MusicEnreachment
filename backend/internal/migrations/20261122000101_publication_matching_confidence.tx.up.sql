-- The exact score that admitted the publication is frozen on its attempt and
-- copied to CURRENT only when that result commits. NULL means the attempt did
-- not carry a score; a later commit must clear a predecessor's score.
ALTER TABLE public.track_publication
    ADD COLUMN matching_confidence double precision,
    ADD CONSTRAINT track_publication_matching_confidence_check CHECK (
        matching_confidence IS NULL OR (
            matching_confidence >= 0::double precision
            AND matching_confidence <= 1::double precision
        )
    );

ALTER TABLE public.track_publication_attempt
    ADD COLUMN matching_confidence double precision,
    ADD CONSTRAINT track_publication_attempt_matching_confidence_check CHECK (
        matching_confidence IS NULL OR (
            matching_confidence >= 0::double precision
            AND matching_confidence <= 1::double precision
        )
    );
