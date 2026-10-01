-- The absolute-path rule belongs to the application boundary, not to a
-- POSIX-shaped SQL check. settings.NormalizePath accepts a platform absolute
-- path (a POSIX "/..." path or a Windows drive/UNC path such as "C:\Music"),
-- and the source-root service rejects a relative path before it is persisted.
-- The old CHECK pinned every stored path to a leading slash, so it rejected a
-- valid Windows server path that the documented Windows amd64 support requires.
-- Drop only that check: configured_path stays NOT NULL and unique.
ALTER TABLE source_root DROP CONSTRAINT source_root_configured_path_absolute;
