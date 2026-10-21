DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM publication_source_cleanup_request) THEN
        RAISE EXCEPTION 'refusing to drop populated current source cleanup requests';
    END IF;
END $$;

DROP TABLE publication_source_cleanup_request;
