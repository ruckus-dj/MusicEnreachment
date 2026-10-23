DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.operation o
        JOIN public.operation_tool_read_hold h ON h.operation_id=o.id
        WHERE o.kind='publish_track_v1' AND o.state='failed'
    ) THEN
        RAISE EXCEPTION 'cannot roll back failed-publication tool-hold support while failed publications retain tool holds';
    END IF;
END $$;

CREATE OR REPLACE FUNCTION public.check_source_analysis_operation_holds() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
    IF NOT FOUND THEN RETURN NULL; END IF;

    SELECT count(*) INTO work_hold_count FROM operation_source_work_hold WHERE operation_id = operation_row.id;
    SELECT count(*) INTO tool_hold_count FROM operation_tool_read_hold WHERE operation_id = operation_row.id;
    SELECT EXISTS(SELECT 1 FROM source_analysis_step WHERE execution_operation_id = operation_row.id) INTO has_execution;
    IF operation_row.tools_read_required IS DISTINCT FROM (tool_hold_count > 0) THEN
        RAISE EXCEPTION 'operation tools_read_required must match its tool read holds';
    END IF;
    IF operation_row.source_analysis_mode IS NULL THEN
        IF work_hold_count <> 0 OR has_execution OR
           (operation_row.state NOT IN ('queued', 'running') AND tool_hold_count <> 0) THEN
            RAISE EXCEPTION 'non-analysis operation cannot retain work holds or execution, and terminal operations cannot retain tool holds';
        END IF;
        RETURN NULL;
    END IF;

    snapshot := operation_row.input_snapshot;
    IF operation_row.state IN ('queued', 'running') THEN
        IF snapshot->>'mode' IS DISTINCT FROM operation_row.source_analysis_mode OR
           (snapshot->>'rerun_target')::boolean IS DISTINCT FROM operation_row.rerun_target OR
           (snapshot->>'schema_version')::integer IS DISTINCT FROM 1 OR
           jsonb_typeof(snapshot->'rerun_target') IS DISTINCT FROM 'boolean' THEN
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
        IF operation_row.source_analysis_mode='batch' AND
           (jsonb_array_length(snapshot->'work_ids') <> 1 OR work_hold_count <> 1) THEN
            RAISE EXCEPTION 'active batch source-analysis operation must hold exactly one pinned work item';
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
        IF (operation_row.source_analysis_mode='batch' AND operation_row.target_source_location_id IS NOT NULL) OR
           EXISTS (
               SELECT 1 FROM operation_source_work_hold h JOIN source_analysis_work w ON w.id=h.work_id
               WHERE h.operation_id=operation_row.id
                 AND (w.source_root_id IS DISTINCT FROM operation_row.target_source_root_id
                      OR (operation_row.target_source_location_id IS NOT NULL AND w.location_id IS DISTINCT FROM operation_row.target_source_location_id))
           ) THEN
            RAISE EXCEPTION 'active source-analysis holds must match operation source selectors';
        END IF;
    ELSE
        IF work_hold_count <> 0 OR tool_hold_count <> 0 OR has_execution THEN
            RAISE EXCEPTION 'terminal normalized source-analysis operation cannot retain holds or execution triples';
        END IF;
    END IF;
    RETURN NULL;
END $$;
