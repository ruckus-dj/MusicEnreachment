ALTER TABLE operation
    DROP CONSTRAINT operation_target_installation_id_fkey,
    ADD CONSTRAINT operation_target_installation_id_fkey
    FOREIGN KEY (target_installation_id) REFERENCES tool_installation(id);

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation
    CHECK (kind = 'move_tools_root' OR target_installation_id IS NOT NULL) NOT VALID;

ALTER TABLE tool_installation
    DROP CONSTRAINT tool_installation_relative_path_matches_identity;
