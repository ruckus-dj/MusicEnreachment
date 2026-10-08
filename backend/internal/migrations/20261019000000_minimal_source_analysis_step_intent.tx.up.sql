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
        RAISE EXCEPTION 'cannot require minimal source-analysis step intent while active steps have incompatible snapshots';
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
            AND step IN ('sha256', 'probe', 'fingerprint')
            AND jsonb_typeof(input_snapshot->'rerun_target') IS NOT DISTINCT FROM 'boolean'
            AND jsonb_typeof(input_snapshot->'analysis_policy_version') IS NOT DISTINCT FROM 'number'
            AND (input_snapshot->>'analysis_policy_version' ~ '^[1-9][0-9]*$') IS TRUE
        )
    );

ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;
ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN tools_read_required THEN hashtextextended(id::text, 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot->>'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN tools_read_required THEN hashtextextended(id::text, 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot->>'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR tools_read_required
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot->>'target_identity', '') <> ''
        )
    );
