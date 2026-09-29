ALTER TABLE tool_installation
    ADD CONSTRAINT tool_installation_relative_path_matches_identity
    CHECK (
        package_kind IN ('ffmpeg', 'fpcalc')
        AND release_identity <> ''
        AND release_identity NOT IN ('.', '..')
        AND release_identity !~ '[/\\:]'
        AND relative_path = package_kind ||
            CASE WHEN platform_goos = 'windows' THEN chr(92) ELSE '/' END ||
            release_identity
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation
    CHECK (
        kind = 'move_tools_root'
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_target_installation_id_fkey,
    ADD CONSTRAINT operation_target_installation_id_fkey
    FOREIGN KEY (target_installation_id) REFERENCES tool_installation(id) ON DELETE SET NULL;
