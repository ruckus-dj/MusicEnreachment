DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM matching_run run JOIN operation ON operation.id=run.operation_id
               WHERE run.quiesced_at IS NOT NULL AND operation.state='failed') THEN
        RAISE EXCEPTION 'cannot roll back failed obsolete matching disposal while failed quiesced runs remain';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION validate_matching_run_retirement_authorization() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    operation_row operation%ROWTYPE;
    acquisition_version integer;
BEGIN
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
    IF NOT FOUND OR operation_row.kind <> 'matching_evaluation_v1' THEN
        RAISE EXCEPTION 'matching run is not eligible for proof retirement';
    END IF;
    SELECT intent_version INTO acquisition_version FROM matching_acquisition WHERE run_id=NEW.run_id;

    IF NEW.authorization_kind = 'obsolete_quiesced' THEN
        IF acquisition_version IS NULL OR acquisition_version NOT IN (2,3)
           OR operation_row.state NOT IN ('queued','running')
           OR NOT EXISTS (SELECT 1 FROM matching_run run WHERE run.id=NEW.run_id
                            AND run.quiesced_at IS NOT NULL
                            AND run.quiesced_at < NEW.cleanup_cutoff)
           OR NOT EXISTS (SELECT 1 FROM matching_run_target target
                           JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
                           WHERE target.run_id=NEW.run_id AND generation.high_water > target.target_generation
                             AND generation.current_run_id IS DISTINCT FROM NEW.run_id)
           OR EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id=NEW.run_id)
           OR EXISTS (SELECT 1 FROM matching_run_target target JOIN incoming_match_evaluation evaluation
                       ON evaluation.target_run_id=NEW.run_id OR evaluation.frozen_run_id=NEW.run_id
                       WHERE target.run_id=NEW.run_id)
           OR EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=NEW.run_id AND delivery.consumed_at IS NULL)
            OR EXISTS (SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
                        WHERE delivery.run_id=NEW.run_id
                          AND job.state IN ('available','pending','running','retryable','scheduled'))
            OR EXISTS (SELECT 1 FROM matching_acquisition_seal seal JOIN river_job job ON job.id=seal.river_job_id
                        WHERE seal.run_id=NEW.run_id
                          AND job.state IN ('available','pending','running','retryable','scheduled'))
           OR EXISTS (SELECT 1 FROM matching_apply_request request
                       LEFT JOIN operation child ON child.id=request.apply_operation_id
                       LEFT JOIN river_job job ON job.id=child.river_job_id
                       WHERE request.parent_run_id=NEW.run_id AND
                         (child.id IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                          OR child.finished_at >= NEW.cleanup_cutoff OR child.state IN ('queued','running')
                          OR job.state IN ('available','pending','running','retryable','scheduled')))
           OR EXISTS (SELECT 1 FROM matching_provider_dependency dependency
                       LEFT JOIN operation child ON child.id=dependency.request_id
                       LEFT JOIN river_job job ON job.id=child.river_job_id
                       WHERE dependency.parent_run_id=NEW.run_id AND
                         (dependency.terminal_outcome IS NULL OR child.id IS NULL
                          OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                          OR child.finished_at >= NEW.cleanup_cutoff OR child.state IN ('queued','running')
                          OR job.state IN ('available','pending','running','retryable','scheduled')))
           OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=operation_row.river_job_id
                       AND job.state IN ('available','pending','running','retryable','scheduled'))
           OR EXISTS (SELECT 1 FROM matching_run run JOIN river_job job ON job.id=run.current_river_job_id
                       WHERE run.id=NEW.run_id AND job.state IN ('available','pending','running','retryable','scheduled'))
        THEN
            RAISE EXCEPTION 'quiesced matching run is not eligible for obsolete-proof retirement';
        END IF;
        RETURN NEW;
    END IF;

    IF operation_row.state <> 'succeeded' OR operation_row.finished_at IS NULL
       OR operation_row.finished_at >= NEW.cleanup_cutoff
       OR acquisition_version IS DISTINCT FROM 3
       OR EXISTS (SELECT 1 FROM matching_run run WHERE run.id=NEW.run_id AND run.state <> 'succeeded')
       OR EXISTS (SELECT 1 FROM incoming_matching_generation generation WHERE generation.current_run_id=NEW.run_id)
       OR EXISTS (SELECT 1 FROM incoming_match_evaluation evaluation
                   WHERE evaluation.target_run_id=NEW.run_id OR evaluation.frozen_run_id=NEW.run_id)
       OR EXISTS (SELECT 1 FROM matching_apply_request request JOIN operation child ON child.id=request.apply_operation_id
                   WHERE request.parent_run_id=NEW.run_id AND
                     (child.state <> 'succeeded' OR child.finished_at IS NULL OR child.finished_at >= NEW.cleanup_cutoff
                      OR child.state IN ('queued','running') OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                          AND job.state IN ('available','pending','running','retryable','scheduled'))))
       OR EXISTS (SELECT 1 FROM matching_provider_dependency dependency JOIN operation child ON child.id=dependency.request_id
                   WHERE dependency.parent_run_id=NEW.run_id AND
                     (dependency.terminal_outcome IS NULL OR child.state NOT IN ('succeeded','failed') OR child.finished_at IS NULL
                      OR child.finished_at >= NEW.cleanup_cutoff OR child.state IN ('queued','running')
                      OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=child.river_job_id
                                 AND job.state IN ('available','pending','running','retryable','scheduled'))))
       OR EXISTS (SELECT 1 FROM river_job job WHERE job.id=operation_row.river_job_id
                   AND job.state IN ('available','pending','running','retryable','scheduled'))
       OR EXISTS (SELECT 1 FROM matching_delivery delivery WHERE delivery.run_id=NEW.run_id AND delivery.consumed_at IS NULL)
    THEN
        RAISE EXCEPTION 'matching run is not eligible for proof retirement';
    END IF;
    RETURN NEW;
END;
$$;
