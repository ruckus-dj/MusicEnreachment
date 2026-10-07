DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM source_analysis_step
        WHERE NOT (
            (execution_operation_id IS NULL AND execution_operation_attempt IS NULL AND execution_job_id IS NULL)
            OR (execution_operation_id IS NOT NULL AND execution_operation_attempt IS NOT NULL AND execution_operation_attempt > 0 AND execution_job_id IS NOT NULL)
        )
    ) THEN
        RAISE EXCEPTION 'cannot restore prior source-analysis execution constraint: partial execution triple exists';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION check_source_analysis_operation_holds() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    operation_row operation%ROWTYPE;
    snapshot jsonb;
    affected_operation_id uuid;
    work_hold_count bigint;
    tool_hold_count bigint;
    has_execution boolean;
BEGIN
    IF TG_TABLE_NAME = 'operation' THEN
        affected_operation_id := COALESCE(NEW.id, OLD.id);
    ELSE
        affected_operation_id := COALESCE(NEW.operation_id, OLD.operation_id);
    END IF;
    SELECT * INTO operation_row FROM operation WHERE id = affected_operation_id;
    IF NOT FOUND OR operation_row.source_analysis_mode IS NULL THEN
        RETURN NULL;
    END IF;
    snapshot := operation_row.input_snapshot;
    SELECT count(*) INTO work_hold_count FROM operation_source_work_hold WHERE operation_id = operation_row.id;
    SELECT count(*) INTO tool_hold_count FROM operation_tool_read_hold WHERE operation_id = operation_row.id;
    SELECT EXISTS(SELECT 1 FROM source_analysis_step WHERE execution_operation_id = operation_row.id) INTO has_execution;
    IF operation_row.state IN ('queued', 'running') THEN
        IF snapshot->>'mode' IS DISTINCT FROM operation_row.source_analysis_mode OR
           (snapshot->>'rerun_target')::boolean IS DISTINCT FROM operation_row.rerun_target OR
           (snapshot->>'tools_read_required')::boolean IS DISTINCT FROM operation_row.tools_read_required OR
           NOT (snapshot ? 'sha256_enabled') OR NOT (snapshot ? 'cache_only_reuse') THEN
            RAISE EXCEPTION 'active normalized source-analysis operation selectors must match its explicit snapshot';
        END IF;
        IF (operation_row.source_analysis_mode='single_step' AND
            (operation_row.target_work_id IS DISTINCT FROM (snapshot->>'target_work_id')::uuid OR operation_row.target_step IS DISTINCT FROM (snapshot->>'target_step'))) OR
           (operation_row.source_analysis_mode='batch' AND
            ((snapshot->>'target_work_id') IS NOT NULL OR (snapshot->>'target_step') IS NOT NULL)) THEN
            RAISE EXCEPTION 'active normalized source-analysis target must match its snapshot';
        END IF;
        IF jsonb_typeof(snapshot->'work_ids') IS DISTINCT FROM 'array' OR work_hold_count <> jsonb_array_length(snapshot->'work_ids') THEN
            RAISE EXCEPTION 'active normalized source-analysis operation must hold exactly its pinned work items';
        END IF;
        IF jsonb_typeof(snapshot->'tools') IS DISTINCT FROM 'array' OR operation_row.tools_read_required <> (tool_hold_count > 0) OR
           tool_hold_count <> (SELECT count(DISTINCT (selected.tool->>'installation_id')::uuid) FROM jsonb_array_elements(snapshot->'tools') AS selected(tool)) THEN
            RAISE EXCEPTION 'source-analysis tool holds must match tools_read_required';
        END IF;
        IF EXISTS (
            SELECT 1 FROM jsonb_array_elements_text(snapshot->'work_ids') AS selected(work_id)
            WHERE NOT EXISTS (SELECT 1 FROM operation_source_work_hold h WHERE h.operation_id=operation_row.id AND h.work_id=selected.work_id::uuid)
        ) THEN
            RAISE EXCEPTION 'active normalized source-analysis holds must match the snapshot';
        END IF;
        IF operation_row.source_analysis_mode='single_step' AND NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements_text(snapshot->'work_ids') AS selected(work_id)
            WHERE selected.work_id::uuid=operation_row.target_work_id
        ) THEN
            RAISE EXCEPTION 'active single-step target must be a pinned work item';
        END IF;
        IF EXISTS (
            SELECT 1 FROM jsonb_array_elements(snapshot->'tools') AS selected(tool)
            WHERE NOT EXISTS (SELECT 1 FROM operation_tool_read_hold h WHERE h.operation_id=operation_row.id AND h.installation_id=(selected.tool->>'installation_id')::uuid)
        ) THEN
            RAISE EXCEPTION 'active normalized source-analysis tool holds must match the snapshot';
        END IF;
    ELSE
        IF work_hold_count <> 0 OR tool_hold_count <> 0 OR has_execution THEN
            RAISE EXCEPTION 'terminal normalized source-analysis operation cannot retain holds or execution triples';
        END IF;
    END IF;
    RETURN NULL;
END $$;

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
    IF NOT EXISTS (SELECT 1 FROM operation_source_work_hold WHERE operation_id=operation_id_value AND work_id=work_id_value)
       OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(operation_row.input_snapshot->'work_ids') AS selected(work_id) WHERE selected.work_id::uuid=work_id_value)
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

ALTER TABLE source_analysis_step
    DROP CONSTRAINT source_analysis_step_execution_all_or_none,
    ADD CONSTRAINT source_analysis_step_execution_all_or_none CHECK (
        (execution_operation_id IS NULL AND execution_operation_attempt IS NULL AND execution_job_id IS NULL)
        OR (execution_operation_id IS NOT NULL AND execution_operation_attempt > 0 AND execution_job_id IS NOT NULL)
    );
