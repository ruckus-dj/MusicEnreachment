# backend/internal/migrations

**Score: 16** (high complexity: 11 files, module boundary, distinct domain, code-heavy)

## OVERVIEW
Embedded SQL migrations via bun/migrate. Versioned .tx.up/.tx.down pairs, applied at server startup in app.go before River and HTTP server start.

## STRUCTURE
Each migration is a pair: `YYYYMMDDHHMMSS_description.tx.up.sql` + `.tx.down.sql`. Embedded via `//go:embed *.sql` in migrations.go. Applied with `WithMarkAppliedOnSuccess(true)`.

## WHERE TO LOOK
- migrations.go - embed directive, migrator setup, applyMigrations called from app.go
- 20260927000000_bootstrap.tx.up.sql - initial schema
- 20260928000000_setup_manager.tx.up.sql - setup_manager tables
- 20260929000000_setup_manager_persistence_contract.tx.up.sql - constraints
- 20260930000000_setup_manager_transition_invariants.tx.up.sql - state machine guards
- backend/cmd/migrate/main.go - rollback CLI (down migrations only; up is startup-only)

## CONVENTIONS
- Forward migrations run at server startup (coupled to deploy, cannot run server against unmigrated DB without this path)
- Rollback is manual CLI-only: `task migrate:rollback` (stop server first)
- Every .tx.up.sql must have matching .tx.down.sql
- Transactional migrations (.tx prefix) ensure atomicity
- Migration version = timestamp (YYYYMMDDHHMMSS)
- Applied migrations tracked in bun_migrations table
- WithMarkAppliedOnSuccess(true) marks migration complete only after successful application

## ANTI-PATTERNS
- Running server against unmigrated DB without startup migration path (forbidden by design)
- Missing down migrations (all must be reversible)
- Manual SQL execution outside migration framework (breaks version tracking)
- Non-transactional migrations for schema changes (always use .tx prefix)

## NOTES
Startup migration coupling means you cannot skip migrations or run an old binary against a new schema. Deploy rollback requires explicit `migrate:rollback` before reverting binary.
