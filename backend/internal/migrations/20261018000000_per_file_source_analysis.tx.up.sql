-- Source-root row locks and the output/admission gate serialize scan conflicts.
-- Per-file analyses may now coexist for one root; work-level holds and fences
-- remain the duplicate-admission authority.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM operation_source_work_hold
        GROUP BY work_id HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot enable per-file source analysis while work items have duplicate active holds';
    END IF;
    IF EXISTS (
        SELECT 1 FROM operation
        WHERE source_analysis_mode = 'batch' AND state IN ('queued', 'running')
          AND (jsonb_typeof(input_snapshot->'work_ids') IS DISTINCT FROM 'array'
               OR jsonb_array_length(CASE WHEN jsonb_typeof(input_snapshot->'work_ids') = 'array'
                                          THEN input_snapshot->'work_ids' ELSE '[]'::jsonb END) <> 1
               OR jsonb_typeof(input_snapshot->'selected_steps') IS DISTINCT FROM 'array'
               OR jsonb_array_length(CASE WHEN jsonb_typeof(input_snapshot->'selected_steps') = 'array'
                                          THEN input_snapshot->'selected_steps' ELSE '[]'::jsonb END) = 0
               OR EXISTS (
                   SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(input_snapshot->'selected_steps') = 'array'
                                                           THEN input_snapshot->'selected_steps' ELSE '[]'::jsonb END) AS selected(tuple)
                   WHERE jsonb_typeof(selected.tuple) IS DISTINCT FROM 'object'
                       OR (selected.tuple - 'work_id' - 'step') IS DISTINCT FROM '{}'::jsonb
                      OR jsonb_typeof(selected.tuple->'work_id') IS DISTINCT FROM 'string'
                      OR jsonb_typeof(selected.tuple->'step') IS DISTINCT FROM 'string'
                      OR selected.tuple->>'step' NOT IN ('sha256', 'probe', 'fingerprint')
                      OR (selected.tuple->>'work_id')::uuid IS DISTINCT FROM
                         (input_snapshot->'work_ids'->>0)::uuid
               )
               OR (SELECT count(*) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(input_snapshot->'selected_steps') = 'array'
                                                                    THEN input_snapshot->'selected_steps' ELSE '[]'::jsonb END)) <>
                  (SELECT count(DISTINCT ((selected.tuple->>'work_id')::uuid, selected.tuple->>'step'))
                   FROM jsonb_array_elements(CASE WHEN jsonb_typeof(input_snapshot->'selected_steps') = 'array'
                                                  THEN input_snapshot->'selected_steps' ELSE '[]'::jsonb END) AS selected(tuple)))
    ) THEN
        RAISE EXCEPTION 'cannot enable per-file source analysis while incompatible active batch operations exist';
    END IF;
END $$;

DROP INDEX operation_one_active_source_root_operation;

CREATE UNIQUE INDEX operation_one_active_source_root_scan
    ON operation (target_source_root_id)
    WHERE kind = 'scan_source' AND state IN ('queued', 'running');

-- Each active analysis holds its exact work items. This allows independent
-- files under one root to proceed concurrently, while preventing duplicate
-- active analysis of the same file, including overlap between batch requests.
CREATE UNIQUE INDEX operation_source_work_hold_one_active_work
    ON operation_source_work_hold (work_id);

ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;
ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind IN ('move_tools_root', 'scan_source', 'analyze_source') THEN NULL
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind IN ('move_tools_root', 'scan_source', 'analyze_source') THEN NULL
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
        )
    );

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

CREATE OR REPLACE FUNCTION check_source_analysis_step_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    operation_id_value uuid;
    operation_row operation%ROWTYPE;
    work_id_value uuid;
    step_value text;
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
    IF operation_id_value IS NULL THEN RETURN NULL; END IF;
    SELECT * INTO operation_row FROM operation WHERE id=operation_id_value;
    IF NOT FOUND THEN RAISE EXCEPTION 'source-analysis execution must reference an existing operation'; END IF;
    IF operation_row.source_analysis_mode IS NULL THEN RAISE EXCEPTION 'source-analysis execution must reference a normalized analysis operation'; END IF;
    IF operation_row.state NOT IN ('queued','running') THEN
        IF TG_OP = 'DELETE' THEN RETURN NULL; END IF;
        RAISE EXCEPTION 'terminal source-analysis operation cannot acquire execution';
    END IF;
    IF operation_row.source_analysis_mode='batch' AND operation_row.target_source_location_id IS NOT NULL THEN
        RAISE EXCEPTION 'batch source-analysis execution cannot have a single-location target';
    END IF;
    IF operation_row.source_analysis_mode='single_step' AND operation_row.target_source_location_id IS NULL THEN
        RAISE EXCEPTION 'single-step source-analysis execution requires a location target';
    END IF;
    IF NOT EXISTS (
           SELECT 1 FROM source_analysis_work w WHERE w.id=work_id_value
             AND w.source_root_id=operation_row.target_source_root_id
             AND (operation_row.target_source_location_id IS NULL OR w.location_id=operation_row.target_source_location_id)
       ) OR NOT EXISTS (SELECT 1 FROM operation_source_work_hold WHERE operation_id=operation_id_value AND work_id=work_id_value)
       OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(operation_row.input_snapshot->'work_ids') AS selected(work_id) WHERE selected.work_id::uuid=work_id_value)
       OR (operation_row.source_analysis_mode='single_step' AND (operation_row.target_work_id<>work_id_value OR operation_row.target_step<>step_value)) THEN
        RAISE EXCEPTION 'active source-analysis execution must match its source selectors, pinned work, and step selection';
    END IF;
    IF step_value NOT IN ('sha256','probe','fingerprint') THEN
        RAISE EXCEPTION 'active source-analysis execution must select a supported step';
    END IF;
    IF operation_row.source_analysis_mode='batch' AND operation_row.target_work_id IS NOT NULL THEN
        RAISE EXCEPTION 'batch source-analysis execution cannot have a single-work target';
    END IF;
    IF operation_row.source_analysis_mode='batch' THEN
        IF jsonb_typeof(operation_row.input_snapshot->'work_ids') IS DISTINCT FROM 'array' OR
           jsonb_array_length(operation_row.input_snapshot->'work_ids') <> 1 OR
           jsonb_typeof(operation_row.input_snapshot->'selected_steps') IS DISTINCT FROM 'array' OR
           jsonb_array_length(operation_row.input_snapshot->'selected_steps') = 0 THEN
            RAISE EXCEPTION 'active batch source-analysis operation requires one work item and explicit selected steps';
        END IF;
        IF EXISTS (
            SELECT 1 FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps') AS selected(tuple)
            WHERE jsonb_typeof(selected.tuple) IS DISTINCT FROM 'object'
               OR (selected.tuple - 'work_id' - 'step') IS DISTINCT FROM '{}'::jsonb
               OR jsonb_typeof(selected.tuple->'work_id') IS DISTINCT FROM 'string'
               OR jsonb_typeof(selected.tuple->'step') IS DISTINCT FROM 'string'
               OR selected.tuple->>'step' NOT IN ('sha256', 'probe', 'fingerprint')
               OR (selected.tuple->>'work_id')::uuid IS DISTINCT FROM work_id_value
        ) OR
        (SELECT count(*) FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps')) <>
        (SELECT count(DISTINCT ((selected.tuple->>'work_id')::uuid, selected.tuple->>'step'))
         FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps') AS selected(tuple)) OR
        NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements(operation_row.input_snapshot->'selected_steps') AS selected(tuple)
            WHERE (selected.tuple->>'work_id')::uuid = work_id_value AND selected.tuple->>'step' = step_value
        ) THEN
            RAISE EXCEPTION 'active source-analysis execution must belong to an exact selected work/step tuple';
        END IF;
    END IF;
    RETURN NULL;
END $$;
