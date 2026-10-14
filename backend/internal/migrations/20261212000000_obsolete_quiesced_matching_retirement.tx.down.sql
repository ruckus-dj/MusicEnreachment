DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_run WHERE quiesced_at IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back obsolete quiesced matching retirement while quiesced runs remain';
    END IF;
    IF EXISTS (SELECT 1 FROM matching_run_retirement_authorization WHERE authorization_kind='obsolete_quiesced') THEN
        RAISE EXCEPTION 'cannot roll back obsolete quiesced matching retirement while authorizations remain';
    END IF;
END;
$$;

DROP TRIGGER matching_run_quiescence_timestamp_immutable ON matching_run;
DROP FUNCTION protect_matching_run_quiescence_timestamp();
ALTER TABLE matching_run_retirement_authorization DROP CONSTRAINT matching_run_retirement_authorization_obsolete_v2;

CREATE OR REPLACE FUNCTION validate_matching_run_retirement_authorization() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    operation_row operation%ROWTYPE;
BEGIN
    -- Restore the 2026-12-07 authorization contract verbatim.
    PERFORM pg_advisory_xact_lock_shared(1297371734, 3);
    PERFORM pg_advisory_xact_lock(1297371734, 4);
    PERFORM pg_advisory_xact_lock_shared(hashtext('musicbrainz-config'));
    IF NEW.transaction_id <> pg_current_xact_id() THEN
        RAISE EXCEPTION 'matching run retirement authorization must belong to this transaction';
    END IF;
    IF NEW.authorized_at <> transaction_timestamp() THEN
        RAISE EXCEPTION 'matching run retirement authorization timestamp must equal transaction_timestamp()';
    END IF;
    IF NEW.cleanup_cutoff > transaction_timestamp() - interval '24 hours' THEN
        RAISE EXCEPTION 'matching run retirement cleanup cutoff exceeds the 24-hour eligibility boundary';
    END IF;
    SELECT operation.* INTO operation_row
      FROM matching_run run JOIN operation ON operation.id = run.operation_id
     WHERE run.id = NEW.run_id FOR UPDATE OF run, operation;
    IF NOT FOUND OR operation_row.kind <> 'matching_evaluation_v1' OR operation_row.state <> 'succeeded'
       OR operation_row.finished_at IS NULL OR operation_row.finished_at >= NEW.cleanup_cutoff
       OR NOT EXISTS (SELECT 1 FROM matching_acquisition acquisition
                        WHERE acquisition.run_id = NEW.run_id AND acquisition.intent_version = 3)
       OR EXISTS (SELECT 1 FROM matching_run run WHERE run.id = NEW.run_id AND run.state <> 'succeeded')
       OR EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id = NEW.run_id)
       OR EXISTS (SELECT 1 FROM incoming_match_evaluation evaluation
                     WHERE evaluation.target_run_id = NEW.run_id OR evaluation.frozen_run_id = NEW.run_id)
       OR EXISTS (SELECT 1 FROM matching_apply_request request JOIN operation child ON child.id = request.apply_operation_id
                    WHERE request.parent_run_id = NEW.run_id AND
                       (child.state <> 'succeeded' OR child.finished_at IS NULL OR child.finished_at >= NEW.cleanup_cutoff
                        OR child.state IN ('queued','running') OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                            AND job.state IN ('available','pending','running','retryable','scheduled'))))
       OR EXISTS (SELECT 1 FROM matching_provider_dependency dependency JOIN operation child ON child.id=dependency.request_id
                     WHERE dependency.parent_run_id=NEW.run_id AND
                       (dependency.terminal_outcome IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                          OR child.finished_at >= NEW.cleanup_cutoff
                         OR child.state IN ('queued','running')
                        OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                                   AND job.state IN ('available','pending','running','retryable','scheduled'))))
       OR EXISTS (SELECT 1 FROM matching_run parent_run JOIN operation parent ON parent.id=parent_run.operation_id
                     JOIN river_job job ON job.id=parent.river_job_id WHERE parent_run.id=NEW.run_id
                       AND job.state IN ('available','pending','running','retryable','scheduled'))
       OR EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id = NEW.run_id AND delivery.consumed_at IS NULL)
    THEN
        RAISE EXCEPTION 'matching run is not eligible for proof retirement';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE matching_run_retirement_authorization DROP COLUMN authorization_kind;
