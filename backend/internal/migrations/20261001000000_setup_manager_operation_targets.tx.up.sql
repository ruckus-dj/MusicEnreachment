ALTER TABLE tool_installation
    VALIDATE CONSTRAINT tool_installation_ready_is_verified;

ALTER TABLE operation
    VALIDATE CONSTRAINT operation_install_has_target_installation;

DROP INDEX operation_one_active_target;

ALTER TABLE operation
    DROP CONSTRAINT operation_active_tools_operations_exclusive,
    DROP CONSTRAINT operation_install_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation
        CHECK (kind = 'move_tools_root' OR target_installation_id IS NOT NULL) NOT VALID;

ALTER TABLE operation
    VALIDATE CONSTRAINT operation_mutation_has_target_installation;

CREATE UNIQUE INDEX operation_one_active_installation
    ON operation (target_installation_id)
    WHERE kind <> 'move_tools_root' AND state IN ('queued', 'running');

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
