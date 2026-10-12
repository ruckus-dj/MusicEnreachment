DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_fallback_request_registration) THEN
        RAISE EXCEPTION 'cannot roll back while fallback request registrations remain';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS matching_fallback_request_registration_immutable ON matching_fallback_request_registration;
DROP FUNCTION IF EXISTS reject_matching_fallback_request_registration_mutation();
DROP INDEX IF EXISTS matching_fallback_request_registration_lookup_key;
DROP INDEX IF EXISTS matching_fallback_request_registration_one_search;
DROP TABLE IF EXISTS matching_fallback_request_registration;
