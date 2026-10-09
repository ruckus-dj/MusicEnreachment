CREATE TABLE media_metadata_result (
    id uuid PRIMARY KEY,
    source_sha256 bytea,
    observed_tags jsonb NOT NULL CHECK (jsonb_typeof(observed_tags) = 'object'),
    provenance jsonb NOT NULL CHECK (jsonb_typeof(provenance) = 'object'),
    native_matroska jsonb CHECK (native_matroska IS NULL OR jsonb_typeof(native_matroska) = 'object'),
    observed_at timestamptz NOT NULL,
    winning_result_id uuid NOT NULL,
    applied_operation_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT media_metadata_result_sha256_length CHECK (source_sha256 IS NULL OR octet_length(source_sha256) = 32)
);
CREATE UNIQUE INDEX media_metadata_result_current_sha256_idx
    ON media_metadata_result(source_sha256) WHERE source_sha256 IS NOT NULL;

ALTER TABLE operation
    DROP CONSTRAINT operation_source_analysis_target_shape,
    ADD CONSTRAINT operation_source_analysis_target_shape CHECK (
        ((source_analysis_mode IS NULL AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target AND
          (NOT tools_read_required OR kind = 'scan_source')) OR
        (source_analysis_mode = 'batch' AND target_work_id IS NULL AND target_step IS NULL AND NOT rerun_target) OR
        (source_analysis_mode = 'single_step' AND
            ((state IN ('queued', 'running') AND target_work_id IS NOT NULL AND target_step IN ('sha256', 'probe', 'fingerprint', 'metadata')) OR
            (state IN ('succeeded', 'failed') AND target_work_id IS NULL AND target_step IS NULL)) AND
            (NOT rerun_target OR (state IN ('queued', 'running') AND target_step = 'fingerprint') OR
             (state IN ('succeeded', 'failed') AND target_work_id IS NULL AND target_step IS NULL)))) IS TRUE
    ),
    DROP CONSTRAINT operation_normalized_analysis_shape,
    ADD CONSTRAINT operation_normalized_analysis_shape CHECK (
        (kind <> 'analyze_source' OR state NOT IN ('queued','running') OR source_analysis_mode IS NOT NULL) AND
        (source_analysis_mode IS NULL OR (
            kind = 'analyze_source' AND
            ((state IN ('queued','running') AND target_source_root_id IS NOT NULL AND
                ((source_analysis_mode = 'batch' AND target_source_location_id IS NULL AND target_work_id IS NULL AND target_step IS NULL) OR
                 (source_analysis_mode = 'single_step' AND target_source_location_id IS NOT NULL AND target_work_id IS NOT NULL AND target_step IN ('sha256','probe','fingerprint','metadata')))) OR
              (state IN ('succeeded','failed') AND target_source_root_id IS NULL AND target_source_location_id IS NULL AND target_work_id IS NULL AND target_step IS NULL))
        )) IS TRUE
    );

ALTER TABLE source_analysis_step
    DROP CONSTRAINT source_analysis_step_step_check,
    ADD CONSTRAINT source_analysis_step_step_check CHECK (step IN ('sha256','probe','fingerprint','metadata')),
    ADD COLUMN success_metadata_result_id uuid REFERENCES media_metadata_result(id) ON DELETE RESTRICT,
    DROP CONSTRAINT source_analysis_step_result_matches_step,
    ADD CONSTRAINT source_analysis_step_result_matches_step CHECK (
        (success_sha_variant_id IS NULL OR step = 'sha256') AND
        (success_probe_variant_id IS NULL OR step = 'probe') AND
        (success_fingerprint_result_id IS NULL OR step = 'fingerprint') AND
        (success_metadata_result_id IS NULL OR step = 'metadata')
    ),
    DROP CONSTRAINT source_analysis_step_success_has_result,
    ADD CONSTRAINT source_analysis_step_success_has_result CHECK (
        state <> 'succeeded' OR
        (step = 'sha256' AND success_sha_variant_id IS NOT NULL) OR
        (step = 'probe' AND success_probe_variant_id IS NOT NULL) OR
        (step = 'fingerprint' AND success_fingerprint_result_id IS NOT NULL) OR
        (step = 'metadata' AND success_metadata_result_id IS NOT NULL)
    );

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
    IF step_value NOT IN ('sha256','probe','fingerprint','metadata') THEN
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
               OR selected.tuple->>'step' NOT IN ('sha256', 'probe', 'fingerprint', 'metadata')
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

ALTER TABLE source_analysis_artifact
    DROP CONSTRAINT source_analysis_artifact_requested_steps_order,
    DROP CONSTRAINT source_analysis_artifact_requested_steps_unique,
    DROP CONSTRAINT source_analysis_artifact_requested_steps_check,
    ADD CONSTRAINT source_analysis_artifact_requested_steps_check
        CHECK (requested_steps <@ ARRAY['sha256','probe','fingerprint','metadata']::text[]),
    ADD CONSTRAINT source_analysis_artifact_requested_steps_unique CHECK (
        cardinality(array_positions(requested_steps, 'sha256')) <= 1 AND
        cardinality(array_positions(requested_steps, 'probe')) <= 1 AND
        cardinality(array_positions(requested_steps, 'fingerprint')) <= 1 AND
        cardinality(array_positions(requested_steps, 'metadata')) <= 1
    ),
    ADD CONSTRAINT source_analysis_artifact_requested_steps_order CHECK (
        (array_position(requested_steps, 'sha256') IS NULL OR array_position(requested_steps, 'probe') IS NULL OR array_position(requested_steps, 'sha256') < array_position(requested_steps, 'probe')) AND
        (array_position(requested_steps, 'sha256') IS NULL OR array_position(requested_steps, 'fingerprint') IS NULL OR array_position(requested_steps, 'sha256') < array_position(requested_steps, 'fingerprint')) AND
        (array_position(requested_steps, 'sha256') IS NULL OR array_position(requested_steps, 'metadata') IS NULL OR array_position(requested_steps, 'sha256') < array_position(requested_steps, 'metadata')) AND
        (array_position(requested_steps, 'probe') IS NULL OR array_position(requested_steps, 'fingerprint') IS NULL OR array_position(requested_steps, 'probe') < array_position(requested_steps, 'fingerprint')) AND
        (array_position(requested_steps, 'probe') IS NULL OR array_position(requested_steps, 'metadata') IS NULL OR array_position(requested_steps, 'probe') < array_position(requested_steps, 'metadata')) AND
        (array_position(requested_steps, 'fingerprint') IS NULL OR array_position(requested_steps, 'metadata') IS NULL OR array_position(requested_steps, 'fingerprint') < array_position(requested_steps, 'metadata'))
    );

ALTER TABLE source_analysis_work_artifact_binding
    DROP CONSTRAINT source_analysis_work_artifact_binding_requested_steps,
    ADD CONSTRAINT source_analysis_work_artifact_binding_requested_steps
        CHECK (requested_steps <@ ARRAY['sha256','probe','fingerprint','metadata']::text[]);

-- Keep the active-input contract from migration 190, extending only the
-- supported step set. Existing active operations are refused rather than
-- rewritten into metadata intent.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM source_analysis_step
        WHERE state IN ('queued', 'running')
          AND (
              input_snapshot IS NULL
              OR jsonb_typeof(input_snapshot) IS DISTINCT FROM 'object'
              OR CASE WHEN jsonb_typeof(input_snapshot) = 'object'
                      THEN (input_snapshot
                            - 'schema_version' - 'mode' - 'work_ids'
                            - 'target_work_id' - 'target_step' - 'rerun_target'
                            - 'analysis_policy_version') IS DISTINCT FROM '{}'::jsonb
                      ELSE true END
              OR jsonb_typeof(input_snapshot->'schema_version') IS DISTINCT FROM 'number'
              OR input_snapshot->>'schema_version' IS DISTINCT FROM '1'
              OR input_snapshot->>'mode' IS DISTINCT FROM 'single_step'
              OR jsonb_typeof(input_snapshot->'work_ids') IS DISTINCT FROM 'array'
              OR CASE WHEN jsonb_typeof(input_snapshot->'work_ids') = 'array'
                      THEN jsonb_array_length(input_snapshot->'work_ids') <> 1 ELSE true END
              OR input_snapshot->'work_ids'->>0 IS DISTINCT FROM work_id::text
              OR input_snapshot->>'target_work_id' IS DISTINCT FROM work_id::text
              OR input_snapshot->>'target_step' IS DISTINCT FROM step
              OR jsonb_typeof(input_snapshot->'rerun_target') IS DISTINCT FROM 'boolean'
              OR jsonb_typeof(input_snapshot->'analysis_policy_version') IS DISTINCT FROM 'number'
              OR input_snapshot->>'analysis_policy_version' !~ '^[1-9][0-9]*$'
          )
    ) THEN
        RAISE EXCEPTION 'cannot extend source-analysis step intent while active steps have incompatible snapshots';
    END IF;
END $$;

ALTER TABLE source_analysis_step
    DROP CONSTRAINT source_analysis_step_active_input_snapshot,
    ADD CONSTRAINT source_analysis_step_active_input_snapshot CHECK (
        state NOT IN ('queued', 'running') OR (
            input_snapshot IS NOT NULL
            AND jsonb_typeof(input_snapshot) = 'object'
            AND CASE WHEN jsonb_typeof(input_snapshot) = 'object'
                     THEN (input_snapshot
                           - 'schema_version' - 'mode' - 'work_ids'
                           - 'target_work_id' - 'target_step' - 'rerun_target'
                           - 'analysis_policy_version') = '{}'::jsonb
                     ELSE false END
            AND jsonb_typeof(input_snapshot->'schema_version') IS NOT DISTINCT FROM 'number'
            AND input_snapshot->>'schema_version' IS NOT DISTINCT FROM '1'
            AND input_snapshot->>'mode' IS NOT DISTINCT FROM 'single_step'
            AND jsonb_typeof(input_snapshot->'work_ids') IS NOT DISTINCT FROM 'array'
            AND CASE WHEN jsonb_typeof(input_snapshot->'work_ids') = 'array'
                     THEN jsonb_array_length(input_snapshot->'work_ids') = 1 ELSE false END
            AND input_snapshot->'work_ids'->>0 IS NOT DISTINCT FROM work_id::text
            AND input_snapshot->>'target_work_id' IS NOT DISTINCT FROM work_id::text
            AND input_snapshot->>'target_step' IS NOT DISTINCT FROM step
            AND step IN ('sha256', 'probe', 'fingerprint', 'metadata')
            AND jsonb_typeof(input_snapshot->'rerun_target') IS NOT DISTINCT FROM 'boolean'
            AND jsonb_typeof(input_snapshot->'analysis_policy_version') IS NOT DISTINCT FROM 'number'
            AND (input_snapshot->>'analysis_policy_version' ~ '^[1-9][0-9]*$') IS TRUE
        )
    );
