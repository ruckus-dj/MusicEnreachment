# backend/internal/migrations

## OVERVIEW
Embedded SQL migrations via bun/migrate. Versioned .tx.up/.tx.down pairs, applied at server startup in app.go before River and HTTP server start.

The unpublished prototype uses five fresh-instance baselines. Existing development
databases from the former migration chain are not upgraded by this reset; recreate
only disposable databases explicitly. Never replace migration-history rows to make
an old database appear current.

## STRUCTURE
Each migration is a pair: `YYYYMMDDHHMMSS_description.tx.up.sql` + `.tx.down.sql`. Embedded via `//go:embed *.sql` in migrations.go. Applied with `WithMarkAppliedOnSuccess(true)`.

## WHERE TO LOOK
- migrations.go - embed directive, migrator setup, applyMigrations called from app.go
- 20261015000001_application_core.tx.up.sql - settings, tools, operations, reset journal
- 20261015000002_source_inventory_analysis.tx.up.sql - inventory, analyses, work, retained inputs
- 20261015000003_provider_cache.tx.up.sql - providers, cache, source projections
- 20261015000004_local_library.tx.up.sql - local entities, credits, provider and source links
- 20261015000005_incoming_matching.tx.up.sql - grouping, matching, receipts, reconciliation
- backend/cmd/migrate/main.go - rollback CLI (down migrations only; up is startup-only)

## CONVENTIONS
- Forward migrations run at server startup (coupled to deploy, cannot run server against unmigrated DB without this path)
- Rollback is manual CLI-only: `task migrate:rollback` (stop server first)
- Every .tx.up.sql must have matching .tx.down.sql
- Transactional migrations (.tx prefix) ensure atomicity
- Migration version = timestamp (YYYYMMDDHHMMSS)
- Applied migrations tracked in bun_migrations table
- WithMarkAppliedOnSuccess(true) marks migration complete only after successful application
- Bun applies pending files as one migration group, but each `.tx` file has its own transaction
- River owns its separate schema and migration history; application SQL must not recreate or drop them
- Baseline down files refuse populated application tables. The last baseline preflights all application data before group rollback starts dropping objects
- Keep the required `incoming_grouping_state` singleton seed; it is not historical user data

## ANTI-PATTERNS
- Running server against unmigrated DB without startup migration path (forbidden by design)
- Missing down migrations (all must be reversible)
- Manual SQL execution outside migration framework (breaks version tracking)
- Non-transactional migrations for schema changes (always use .tx prefix)
- `DROP SCHEMA public CASCADE` in application rollback (would destroy unrelated and River objects)

## NOTES
Startup migration coupling means you cannot skip migrations or run an old binary against a new schema. Deploy rollback requires explicit `migrate:rollback` before reverting binary.
