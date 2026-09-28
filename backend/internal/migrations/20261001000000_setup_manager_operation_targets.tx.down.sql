ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;

DROP INDEX operation_one_active_installation;

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_install_has_target_installation
        CHECK (kind <> 'install' OR target_installation_id IS NOT NULL) NOT VALID;

CREATE UNIQUE INDEX operation_one_active_target
    ON operation ((input_snapshot ->> 'target_identity'))
    WHERE kind <> 'move_tools_root' AND state IN ('queued', 'running');

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
    ) WHERE (kind IN ('install', 'activate', 'delete', 'move_tools_root') AND state IN ('queued', 'running'));

ALTER TABLE tool_installation
    DROP CONSTRAINT tool_installation_ready_is_verified,
    ADD CONSTRAINT tool_installation_ready_is_verified
        CHECK (state <> 'ready' OR verified_at IS NOT NULL) NOT VALID;
