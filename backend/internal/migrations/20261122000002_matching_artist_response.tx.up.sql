ALTER TABLE public.matching_provider_dependency
    ADD COLUMN accepted_artist_response jsonb,
    ADD CONSTRAINT matching_artist_response_shape CHECK (
        accepted_artist_response IS NULL OR (
            jsonb_typeof(accepted_artist_response) = 'object'
            AND jsonb_typeof(accepted_artist_response->'payload') = 'object'
            AND (accepted_artist_response->>'kind') IN (
                'provider_artist_search', 'provider_artist_release_browse', 'provider_release_lookup'
            )
            AND (accepted_artist_response->>'cache_key') = cache_key
            AND (NOT (accepted_artist_response ? 'revision') OR jsonb_typeof(accepted_artist_response->'revision') = 'string')
            AND CASE WHEN accepted_artist_response->>'kind' = 'provider_release_lookup'
                THEN jsonb_typeof(accepted_artist_response->'projection') = 'object'
                    AND jsonb_typeof(accepted_artist_response->'projection'->'Source') = 'object'
                    AND jsonb_typeof(accepted_artist_response->'projection'->'Releases') = 'array'
                    AND jsonb_typeof(accepted_artist_response->'projection'->'Responses') = 'object'
                ELSE NOT (accepted_artist_response ? 'projection')
            END
        ) IS TRUE
    );

CREATE FUNCTION public.guard_matching_artist_response() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.accepted_artist_response IS NOT NULL AND (
        NEW.accepted_artist_response IS DISTINCT FROM OLD.accepted_artist_response
        OR NEW.parent_run_id IS DISTINCT FROM OLD.parent_run_id
        OR NEW.request_id IS DISTINCT FROM OLD.request_id
    ) THEN
        RAISE EXCEPTION 'accepted artist matching response is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER matching_artist_response_guard
    BEFORE UPDATE ON public.matching_provider_dependency
    FOR EACH ROW EXECUTE FUNCTION public.guard_matching_artist_response();
