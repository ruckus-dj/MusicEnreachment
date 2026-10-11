DROP TABLE manual_matching_lookup_seal;
UPDATE manual_matching_admission SET evaluation_revision='manual-no-evaluation'
    WHERE evaluation_revision IS NULL;
ALTER TABLE manual_matching_admission
    DROP CONSTRAINT manual_matching_admission_evaluation_revision_check;
ALTER TABLE manual_matching_admission
    ADD CONSTRAINT manual_matching_admission_evaluation_revision_check
    CHECK (length(btrim(evaluation_revision)) > 0);
ALTER TABLE manual_matching_admission
    ALTER COLUMN evaluation_revision SET NOT NULL;
