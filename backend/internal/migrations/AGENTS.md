# Migrations

Embedded Bun migrations apply at server startup. Use paired `.tx.up.sql` and
`.tx.down.sql` files. Rollbacks are atomic, preserve River's schema, and refuse
to discard retained recovery authority.
