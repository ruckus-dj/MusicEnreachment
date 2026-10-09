DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM output_reset_journal WHERE state <> 'finished') THEN
        RAISE EXCEPTION 'cannot roll back output reset schema while a reset is unresolved';
    END IF;
    IF EXISTS (SELECT 1 FROM output_reset_journal) THEN
        RAISE EXCEPTION 'cannot roll back output reset schema while reset history is retained';
    END IF;
END $$;

DROP TABLE output_reset_journal;
