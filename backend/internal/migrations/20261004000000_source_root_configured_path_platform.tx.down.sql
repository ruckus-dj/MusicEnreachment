-- Restore the pre-relaxation POSIX-only rule. Re-adding the check validates the
-- rows already stored, so a rollback against a database that holds a Windows
-- path fails instead of silently accepting a row the restored check cannot
-- validate. Roll back on a database migrated before such a path was registered.
ALTER TABLE source_root
    ADD CONSTRAINT source_root_configured_path_absolute CHECK (configured_path LIKE '/%');
