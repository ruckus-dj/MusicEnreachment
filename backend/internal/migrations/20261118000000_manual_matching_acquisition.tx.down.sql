DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM manual_matching_admission) THEN
        RAISE EXCEPTION 'manual matching admissions contain durable lookup runs';
    END IF;
END;
$$;

DROP INDEX manual_matching_admission_group_created_idx;
DROP TABLE manual_matching_admission;
