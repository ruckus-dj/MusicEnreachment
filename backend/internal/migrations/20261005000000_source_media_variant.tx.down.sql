-- Reverse of 20261005000000_source_media_variant. Drop the new kind's rows before
-- the restored kind check would reject them; every object that references a new
-- column falls before the column itself.

ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;

DROP INDEX operation_one_active_source_root_operation;
DROP INDEX operation_analysis_installation_idx;

ALTER TABLE operation
    DROP CONSTRAINT operation_analysis_target_shape,
    DROP CONSTRAINT operation_active_analysis_has_target_location,
    DROP CONSTRAINT operation_active_analysis_has_installation;

ALTER TABLE operation
    DROP COLUMN target_source_location_id,
    DROP COLUMN analysis_installation_id,
    DROP COLUMN analysis_media_variant_id;

DROP INDEX source_location_media_variant_idx;

ALTER TABLE source_location
    DROP COLUMN media_variant_id;

DELETE FROM operation WHERE kind = 'analyze_source';

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (
        kind IN ('install', 'activate', 'delete', 'move_tools_root', 'scan_source')
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

-- Restore the pre-analysis root guard and exclusion constraint exactly as
-- 20261003000000 defined them, so the inventory rollback path is unchanged.
CREATE UNIQUE INDEX operation_one_active_source_root_scan
    ON operation (target_source_root_id)
    WHERE kind = 'scan_source' AND state IN ('queued', 'running');

ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind = 'scan_source' THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind = 'scan_source' THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR kind = 'scan_source'
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
        )
    );

DROP TABLE media_variant;
