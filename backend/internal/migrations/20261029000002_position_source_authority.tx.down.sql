DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM position_source_authority)
       OR EXISTS (SELECT 1 FROM track_publication_attempt WHERE frozen_inputs->>'source_authority_mode' IN ('manual', 'automatic')) THEN
        RAISE EXCEPTION 'refusing to discard live position source authority';
    END IF;
END $$;
DROP TABLE position_source_authority;
