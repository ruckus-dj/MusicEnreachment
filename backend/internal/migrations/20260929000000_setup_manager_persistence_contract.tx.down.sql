DROP INDEX operation_target_installation_active_idx;

ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;
ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        int8range(
            CASE
                WHEN kind = 'move_tools_root' THEN '-9223372036854775808'::bigint
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN '9223372036854775807'::bigint
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)
            END,
            '[]'
        ) WITH &&
    ) WHERE (kind IN ('install', 'move_tools_root') AND state IN ('queued', 'running'));

DROP INDEX operation_one_active_target;
CREATE UNIQUE INDEX operation_one_active_install_target
    ON operation ((input_snapshot ->> 'target_identity'))
    WHERE kind = 'install' AND state IN ('queued', 'running');

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN ('install', 'move_tools_root'));

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_install_target_identity CHECK (
        kind <> 'install' OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP COLUMN attempt,
    DROP COLUMN target_installation_id;

ALTER TABLE tool_installation DROP COLUMN artifact_identities;
