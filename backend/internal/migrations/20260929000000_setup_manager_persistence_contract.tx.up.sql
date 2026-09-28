ALTER TABLE tool_installation
    ADD COLUMN artifact_identities jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE operation
    ADD COLUMN target_installation_id uuid REFERENCES tool_installation(id),
    ADD COLUMN attempt integer NOT NULL DEFAULT 1 CHECK (attempt > 0);

ALTER TABLE operation
    DROP CONSTRAINT operation_install_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind = 'move_tools_root' OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN ('install', 'activate', 'delete', 'move_tools_root'));

DROP INDEX operation_one_active_install_target;
CREATE UNIQUE INDEX operation_one_active_target
    ON operation ((input_snapshot ->> 'target_identity'))
    WHERE kind <> 'move_tools_root' AND state IN ('queued', 'running');

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
    ) WHERE (kind IN ('install', 'activate', 'delete', 'move_tools_root') AND state IN ('queued', 'running'));

CREATE INDEX operation_target_installation_active_idx
    ON operation (target_installation_id)
    WHERE state IN ('queued', 'running');
