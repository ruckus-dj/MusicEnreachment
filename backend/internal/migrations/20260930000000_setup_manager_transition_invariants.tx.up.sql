ALTER TABLE tool_installation
    ADD CONSTRAINT tool_installation_ready_is_verified
    CHECK (state <> 'ready' OR verified_at IS NOT NULL) NOT VALID;

ALTER TABLE operation
    ADD CONSTRAINT operation_install_has_target_installation
    CHECK (kind <> 'install' OR target_installation_id IS NOT NULL) NOT VALID;
