CREATE OR REPLACE FUNCTION check_source_analysis_step_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    operation_id_value uuid;
    operation_row operation%ROWTYPE;
    work_id_value uuid;
BEGIN
    IF TG_OP = 'DELETE' THEN
        operation_id_value := OLD.execution_operation_id;
        work_id_value := OLD.work_id;
    ELSE
        operation_id_value := NEW.execution_operation_id;
        work_id_value := NEW.work_id;
    END IF;
    IF operation_id_value IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT * INTO operation_row FROM operation WHERE id=operation_id_value;
    IF NOT FOUND OR operation_row.source_analysis_mode IS NULL OR operation_row.state NOT IN ('queued','running') THEN
        RETURN NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM operation_source_work_hold WHERE operation_id=operation_id_value AND work_id=work_id_value)
       OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(operation_row.input_snapshot->'work_ids') AS selected(work_id) WHERE selected.work_id::uuid=work_id_value)
       OR (operation_row.source_analysis_mode='single_step' AND (operation_row.target_work_id<>work_id_value OR operation_row.target_step<>CASE WHEN TG_OP='DELETE' THEN OLD.step ELSE NEW.step END)) THEN
        RAISE EXCEPTION 'active source-analysis execution must match its pinned work and step selection';
    END IF;
    RETURN NULL;
END $$;
