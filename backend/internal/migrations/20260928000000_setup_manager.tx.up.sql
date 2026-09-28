CREATE TABLE app_setting (
    setting_name text PRIMARY KEY,
    setting_value text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tool_installation (
    id uuid PRIMARY KEY,
    package_kind text NOT NULL CHECK (package_kind IN ('ffmpeg', 'fpcalc')),
    platform_goos text NOT NULL,
    platform_goarch text NOT NULL,
    source_name text NOT NULL,
    release_identity text NOT NULL,
    relative_path text NOT NULL,
    state text NOT NULL CHECK (state IN ('preparing', 'ready', 'failed')),
    executable_versions jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    verified_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tool_installation_relative_path_not_empty CHECK (relative_path <> ''),
    CONSTRAINT tool_installation_release_identity_not_empty CHECK (release_identity <> ''),
    CONSTRAINT tool_installation_identity_unique UNIQUE (package_kind, source_name, release_identity, platform_goos, platform_goarch)
);

CREATE TABLE operation (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('install', 'move_tools_root')),
    state text NOT NULL CHECK (state IN ('queued', 'running', 'failed', 'succeeded')),
    stage text NOT NULL,
    input_snapshot jsonb NOT NULL,
    bytes_completed bigint NOT NULL DEFAULT 0 CHECK (bytes_completed >= 0),
    bytes_total bigint CHECK (bytes_total IS NULL OR bytes_total >= bytes_completed),
    safe_error text,
    river_job_id bigint UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT operation_failed_has_error CHECK (state <> 'failed' OR safe_error IS NOT NULL),
    CONSTRAINT operation_install_target_identity CHECK (
        kind <> 'install' OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    ),
    CONSTRAINT operation_finished_at_matches_state CHECK (
        (state IN ('queued', 'running') AND finished_at IS NULL)
        OR (state IN ('failed', 'succeeded') AND finished_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX operation_one_active_install_target
    ON operation ((input_snapshot ->> 'target_identity'))
    WHERE kind = 'install' AND state IN ('queued', 'running');

CREATE UNIQUE INDEX operation_one_active_tools_root_move
    ON operation (kind)
    WHERE kind = 'move_tools_root' AND state IN ('queued', 'running');

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

CREATE INDEX tool_installation_package_platform_idx
    ON tool_installation (package_kind, platform_goos, platform_goarch);

CREATE INDEX operation_active_updated_at_idx
    ON operation (updated_at)
    WHERE state IN ('queued', 'running', 'failed');
