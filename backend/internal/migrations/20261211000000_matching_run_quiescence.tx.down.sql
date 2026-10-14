DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM matching_run WHERE quiesced_at IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back matching run quiescence while quiesced runs remain';
    END IF;
END; $$;

ALTER TABLE matching_run
    DROP CONSTRAINT matching_run_quiescence_has_no_active_owner;

ALTER TABLE matching_run
    DROP COLUMN quiesced_at;
