DROP INDEX operation_scan_source_updated_at_idx;

-- Every object that references target_source_root_id must fall before the column:
-- the scan exclusion constraint, the active-scan unique index, and the two lookup
-- indexes. The down migration restores the pre-scan constraint at the very end.
ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;

DROP INDEX operation_one_active_source_root_scan;
DROP INDEX operation_active_source_root_idx;
DROP INDEX operation_target_source_root_idx;

ALTER TABLE operation DROP COLUMN target_source_root_id;

-- Restore the pre-scan target identity rule before re-adding it, and only then
-- delete the surviving scan rows: the restored CHECK would reject a scan row
-- that no longer has a target_source_root_id to derive its target from.
ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind = 'move_tools_root'
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

DELETE FROM operation WHERE kind = 'scan_source';

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN ('install', 'activate', 'delete', 'move_tools_root'));

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation
    CHECK (
        kind = 'move_tools_root'
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

-- Restore the pre-scan exclusion constraint exactly as the previous migration
-- defined it: a move keeps the whole numeric domain, installations collide on
-- their hashed target identity.
ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                ELSE hashtextextended(target_installation_id::text, 0)::numeric
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                ELSE hashtextextended(target_installation_id::text, 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (kind IN ('install', 'activate', 'delete', 'move_tools_root') AND state IN ('queued', 'running'));

DROP INDEX source_location_root_id_idx;

DROP TABLE source_scan_candidate;

DROP TABLE source_location;

DROP TABLE source_root;
