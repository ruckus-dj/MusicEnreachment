CREATE OR REPLACE FUNCTION check_source_analysis_step_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    operation_id_value uuid;
    operation_row operation%ROWTYPE;
    work_id_value uuid;
    step_value text;
    snapshot jsonb;
BEGIN
    IF TG_OP = 'DELETE' THEN
        operation_id_value := OLD.execution_operation_id;
        work_id_value := OLD.work_id;
        step_value := OLD.step;
    ELSE
        operation_id_value := NEW.execution_operation_id;
        work_id_value := NEW.work_id;
        step_value := NEW.step;
    END IF;
    IF operation_id_value IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT * INTO operation_row FROM operation WHERE id=operation_id_value;
    IF NOT FOUND OR operation_row.source_analysis_mode IS NULL OR operation_row.state NOT IN ('queued','running') THEN
        RETURN NULL;
    END IF;
    snapshot := operation_row.input_snapshot;
    IF jsonb_typeof(snapshot->'work_ids') IS DISTINCT FROM 'array' THEN
        RAISE EXCEPTION 'active source-analysis work selection must be an array';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM operation_source_work_hold WHERE operation_id=operation_id_value AND work_id=work_id_value)
       OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(snapshot->'work_ids') AS selected(work_id) WHERE selected.work_id::uuid=work_id_value)
       OR (operation_row.source_analysis_mode='single_step' AND (operation_row.target_work_id<>work_id_value OR operation_row.target_step<>step_value)) THEN
        RAISE EXCEPTION 'active source-analysis execution must match its pinned work and step selection';
    END IF;
    IF snapshot ? 'selected_steps' THEN
        IF operation_row.source_analysis_mode <> 'batch'
           OR jsonb_typeof(snapshot->'selected_steps') IS DISTINCT FROM 'array' THEN
            RAISE EXCEPTION 'active source-analysis selected steps must be a batch array';
        END IF;
        IF jsonb_array_length(snapshot->'selected_steps') = 0 OR EXISTS (
               SELECT 1 FROM jsonb_array_elements(snapshot->'selected_steps') AS selected(tuple)
               WHERE jsonb_typeof(selected.tuple) IS DISTINCT FROM 'object'
                  OR jsonb_typeof(selected.tuple->'work_id') IS DISTINCT FROM 'string'
                  OR jsonb_typeof(selected.tuple->'step') IS DISTINCT FROM 'string'
                  OR selected.tuple->>'step' NOT IN ('sha256','probe','fingerprint')
           ) THEN
            RAISE EXCEPTION 'active source-analysis selected steps contain invalid tuples';
        END IF;
        IF EXISTS (
               SELECT 1 FROM jsonb_array_elements(snapshot->'selected_steps') AS selected(tuple)
               WHERE (selected.tuple->>'step'='sha256' AND snapshot->>'sha256_enabled' IS DISTINCT FROM 'true')
                  OR (selected.tuple->>'work_id')::uuid='00000000-0000-0000-0000-000000000000'::uuid
                  OR NOT EXISTS (
                      SELECT 1 FROM jsonb_array_elements_text(snapshot->'work_ids') AS pinned(work_id)
                      WHERE pinned.work_id::uuid=(selected.tuple->>'work_id')::uuid
                  )
           )
           OR (SELECT count(*) FROM jsonb_array_elements(snapshot->'selected_steps')) <>
              (SELECT count(DISTINCT ((selected.tuple->>'work_id')::uuid, selected.tuple->>'step')) FROM jsonb_array_elements(snapshot->'selected_steps') AS selected(tuple))
           OR (SELECT count(DISTINCT (selected.tuple->>'work_id')::uuid) FROM jsonb_array_elements(snapshot->'selected_steps') AS selected(tuple)) <> jsonb_array_length(snapshot->'work_ids')
           OR EXISTS (
               SELECT 1 FROM jsonb_array_elements_text(snapshot->'work_ids') AS pinned(work_id)
               WHERE NOT EXISTS (
                   SELECT 1 FROM jsonb_array_elements(snapshot->'selected_steps') AS selected(tuple)
                   WHERE (selected.tuple->>'work_id')::uuid=pinned.work_id::uuid
               )
           )
           OR NOT EXISTS (
               SELECT 1 FROM jsonb_array_elements(snapshot->'selected_steps') AS selected(tuple)
               WHERE (selected.tuple->>'work_id')::uuid=work_id_value AND selected.tuple->>'step'=step_value
           ) THEN
            RAISE EXCEPTION 'active source-analysis selected steps must be a valid exact batch selection';
        END IF;
    END IF;
    RETURN NULL;
END $$;
