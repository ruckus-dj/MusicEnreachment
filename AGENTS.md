# PROJECT KNOWLEDGE BASE

**Generated:** 2026-09-28T00:00:00.000Z
**Commit:** HEAD
**Branch:** main

## OVERVIEW
### Product decisions — explicit owner approval required

- Current state only: the owner explicitly rejects application history. Update or
  delete obsolete attempts/results; do not preserve failed originals after a
  successful recovery merely for audit/history. Retain unresolved execution
  evidence only while it is needed for the current action or safe recovery.
- Do not make new product decisions or change existing ones without the owner's explicit approval. This rule applies to the primary agent and all subagents, across code, documentation, and plans.
- Preserve the meaning of approved requirements: do not make optional features mandatory, remove settings, choose defaults, or change user-facing behavior without separate approval.
- A request to update documentation, write a plan, or implement a stage does not authorize revising product decisions. The owner's silence is not approval either.
- If requirements are ambiguous or contradictory, ask the owner first. Until they answer, do not record an assumption as an approved decision or implement it. Clearly separate proposals from approved decisions.

MeloTrove (legacy name: MusicEnreachment) - music library manager with source inventory, audio analysis (Chromaprint), transcoding (ffmpeg), and publication. Go 1.27 backend (Huma/chi API, Bun ORM, River jobs, PostgreSQL) + React 19 frontend (Vite, TanStack Query, Orval-generated client). No auth by design; deployment-boundary security only.

## STRUCTURE
```
./
├── backend/               # Go 1.27 module, cmd + internal packages
│   ├── cmd/              # server (HTTP), migrate (rollback), openapi (export)
│   └── internal/         # See backend/internal/*/AGENTS.md for domain details
├── frontend/             # React 19 + Vite + Tailwind 4, see frontend/AGENTS.md
├── docs/                 # Directory map: see docs/AGENTS.md
│   ├── design/           # Authoritative conventions (Russian), see AGENTS.md
│   └── app-design/       # HTML prototype + screenshots, see AGENTS.md
├── deploy/               # Dockerfile + docker-compose.yml
├── tools/                # Node .mjs build helpers (stage-frontend, check-generated)
├── test_stand/           # Local test data
└── build/                # Output (gitignored, never commit)
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| Start server | `task run` or backend/cmd/server/main.go | Requires PostgreSQL; migrations auto-apply at startup |
| API contract | backend/internal/api/AGENTS.md | Huma v2 on chi, humachi adapter |
| Business logic | backend/internal/service/AGENTS.md | SetupService, Operations state machine |
| Data layer | backend/internal/persistence | Bun ORM repos, see subdirectory docs |
| Background jobs | backend/internal/jobs | River queue, PostgreSQL-backed |
| Tool integrations | backend/internal/integrations/tools/AGENTS.md | Managed ffmpeg/fpcalc downloads, GitHub adapters |
| Migrations | backend/internal/migrations/AGENTS.md | Embedded SQL, versioned .tx.up/.down pairs |
| Settings | backend/internal/settings | PostgreSQL-backed runtime registry and filesystem validation |
| Frontend screens | frontend/src/features/ | Setup, settings, sources, incoming grouping, and matching workflows |
| API client | frontend/src/api/generated/ | Orval-generated from OpenAPI, never hand-edit |
| Conventions | docs/design/AGENTS.md | Russian docs: decisions.md (stack, forbidden items), repository-architecture.md (layering) |
| Design prototype | docs/app-design/AGENTS.md | Static HTML/JS prototype with screenshots |
| Build commands | Taskfile.yml at root | `task build/test/verify/generate/run` |

## CODE MAP
| Symbol | Type | Location | Refs | Role |
|--------|------|----------|------|------|
| app.Run | func | backend/internal/app/app.go | 1 | Composition root: DB, migrations, River, chi router, HTTP server |
| service.SetupService | type | backend/internal/service/setup.go | 11+ | First-run setup wizard contract (tools paths, verification) |
| service.Operations | type | backend/internal/service/operations.go | 57+ | Operation state machine (Start/Progress/Succeed/Fail/Retry/Subscribe) |
| persistence.SetupManagerRepository | type | backend/internal/persistence/setup_manager.go | 33+ | Central data layer: installations + operations (24 methods) |
| settings.Registry | type | backend/internal/settings/settings.go | 8+ | PostgreSQL-backed platform and runtime settings hub |
| integrations/tools.Catalog | type | backend/internal/integrations/tools/catalog.go | 6+ | GitHub release adapters for tool downloads |
| integrations/tools.Lifecycle | type | backend/internal/integrations/tools/lifecycle.go | 4+ | Managed tool materialization (download and executable-version verification) |
| SetupManager | component | frontend/src/features/setup/SetupManager.tsx | 2 | First-run setup wizard |
| SettingsScreen | component | frontend/src/features/settings/SettingsScreen.tsx | 1 | Runtime settings and managed tools |
| SourcesScreen | component | frontend/src/features/sources/ | — | Source inventory and analysis |
| AppShell | component | frontend/src/routes/AppShell.tsx | 1 | Hash router and screen wiring |

## CONVENTIONS
**Config model inverted**: No config files; only 3 bootstrap env vars (DATABASE_URL, HTTP_BIND_ADDRESS, HTTP_PORT). All other settings are DB-backed runtime values edited via UI. Do NOT add new env vars.

**No auth by design**: No users, sessions, tokens, roles, CORS. Deployment-boundary auth only (e.g., Caddy + Authentik).

**Layering**: Technical layers (api/service/persistence/integrations/settings/jobs/migrations/static), NOT domain. api -> service -> persistence; only persistence touches Bun/PostgreSQL; River jobs enqueue atomically in the same Bun tx.

**Generated-code pipeline**: Huma generates OpenAPI from Go -> backend/cmd/openapi exports to frontend/openapi.json -> Orval generates TS client/hooks into frontend/src/api/generated. `task generate` enforces sync; tools/check-generated.mjs verifies drift.

**Error handling**: `fmt.Errorf("context: %w", err)` wrap with lowercase messages; `errors.New` for static; no custom error types in service layer; no `nolint`.

**Test org**: `task test` = `go test -tags=integration` + Vitest/RTL (*.test.tsx colocated) + node --test (tools/). PostgreSQL integration tests use `TEST_DATABASE_URL` when supplied, or start embedded PostgreSQL 18 when absent. The fallback downloads and caches platform-specific PostgreSQL binaries and requires that platform's native PostgreSQL runtime libraries; Docker is not required. Integration tests named `*_integration_test.go`.

**Build interface**: Taskfile only (`task build/test/verify/generate/run`); no shell scripts as build commands; pre-commit runs full `task verify` (intentional).

**CI Task version**: Pin `go-task/setup-task` to the locally verified Task version
(currently `3.54.0`), not `3.x`. Range resolution adds a GitHub releases-list API
request that can fail opaquely with `unable to get latest version` before tests.
For infrastructure-only failures, inspect the failed step and rerun failed jobs;
never remove gates or mistake an incomplete platform matrix for full success.

**Lint**: Go 1.27 gofmt + golangci-lint default linters; Biome (not ESLint/Prettier) for frontend; zero `nolint` usage.

**Native artwork dependency**: backend builds (including OpenAPI generation) need
libvips >= 8.14 and its `pkg-config` metadata. Use `libvips-dev pkg-config` on
Debian/Ubuntu or `brew install vips pkg-config` on macOS. Windows CI builds the
pinned dependency set through `task native:windows-vips`; do not replace it with
the upstream all-codecs SDK or copy unrelated codec DLLs into build output.
Artwork headers enforce 10000 pixels per side; publication uses a libvips
derivative fitting 2048×2048. Native decoder buffers are explicitly permitted,
not a promise of a hard RSS ceiling. Keep original and embedded hashes distinct.
`vips_target_new_to_descriptor` duplicates the supplied C descriptor: close the
original after construction, then unref the target to close its duplicate.
Otherwise Unix can hide the leak while Windows cannot remove staged PNG files.
Inspect Windows DLL imports with streamed `objdump` output, retaining only import
names. A complete PE dump can exceed Node's default synchronous stdout buffer;
raising that buffer is not a substitute for streaming the dependency inspection.

**Migrations**: Bun with embedded `//go:embed *.sql`, versioned .tx.up/.down pairs, applied at server startup before River and HTTP.

## ANTI-PATTERNS (THIS PROJECT)
- Hand-editing frontend/src/api/generated/** (Orval-generated, always regenerate via `task generate`)
- Snapshot ffmpeg links (only release archive links allowed, see integrations/tools/catalog.go)
- Committing build/ artifacts (always gitignored)
- New env vars (config is DB-backed runtime settings, not env overrides)
- CORS headers (same-origin design, Vite dev proxies /api to 127.0.0.1:8080)
- `nolint` usage (keep backend lint-clean without exceptions)
- Empty safe error messages in operations (user-facing failures require safe error text)
- Auto-downgrades of publication quality (explicitly forbidden in design docs)

## UNIQUE STYLES
**Documentation-first**: Conventions live in docs/design/ (Russian), not inline comments.

**Single-process deployment**: Bun and River share one *sql.DB; no sidecars or separate brokers.

**Operation SSE wake-up only**: SSE is an error/reconnect channel, never data-bearing; clients re-read snapshot after wake.

**Durable worker args**: River job args carry only operation ID; workers always reload inputs from snapshot.

**Frontend build embedding**: Vite builds to ../build/frontend; tools/stage-frontend.mjs prepares it; Go binary embeds it via internal/static (//go:embed).

**Prototype coexistence**: docs/app-design/index.html is a static HTML/JS design prototype, NOT part of the React app.

## COMMANDS

### Owner-directed execution workflow

- When implementing a plan, continue through its unfinished items without waiting
  for a new user message after each slice, commit, hook result, or CI notification.
  A progress report is not a completion boundary. Stop only for a genuine blocker,
  an owner decision, or completion of the requested scope.
- Make small coherent commits along the way, always with the normal hooks. At the
  end of each plan item, push and check the complete GitHub CI result, then continue.
  Never confuse a green prerequisite commit with completion of the whole plan.
- Record recurring failures and their prevention in the relevant repository
  documentation. In particular, integration tests using tagged fixture helpers
  need the integration build tag; prove a valid baseline before testing rejection,
  and explicitly create the ownership/job records that a fixture does not create.
- Frozen publication inputs live in the publication attempt, not the operation's
  summary snapshot. Test transactional enqueue rollback with an actual River job
  inserted through the supplied transaction, not a fabricated job ID.
- Live `CommitPublicationResult` tests must use a claimed journal-free result
  fixture (`preparePublicationResultFixture`), not a legacy exposure/installed
  fixture. Prove the successful commit baseline before testing rejected proof;
  `(false, nil)` before validation does not demonstrate proof rejection.
- Do not let agents edit the working tree while a commit hook is running: even
  unrelated edits can make pre-commit report "files were modified by this hook"
  after a successful gate. Finish edits first, then run the hook against a stable
  working tree; use an isolated worktree for genuinely concurrent commits.
- Run the final `task verify` only after delegated edits and formatting finish.
  A gate against a partially written helper or an unformatted in-flight test is
  not a valid baseline; wait for the edit completion notification before rerunning.
- Association fixtures must supply object-valued JSON metadata (for example `{}`),
  not a nil `json.RawMessage` that Bun can serialize as JSON `null`.
- Cleanup reset tests must cover several owners sharing one operation/job: cancel
  each distinct job once. Preserve legacy ownerless queued-item cancellation too.
  Assert reset's existing outcomes (failed queued operations and removed current
  publications), rather than inventing cancellation states or retained publications.
- When pushing from a temporary worktree, name the intended destination explicitly
  (`HEAD:go-development` for this development branch); do not infer it from the
  temporary worktree's branch name.
- For an isolated commit, keep its worktree free of unrelated untracked code as
  well as tracked edits. Pre-commit stashes unstaged tracked files but leaves
  untracked helpers/tests visible, producing an invalid mixed test/lint context.
- `testpostgres.OpenMigrated(t)` creates an isolated database per call: when
  extending a fixture, use its existing `f.db` for all setup and assertions.
  Clone slices before mutating copied request structs so rejection scenarios do
  not silently change the baseline or omitted-member scenarios.
- A subagent does not inherit an assigned worktree as its tool working directory.
  Use absolute paths for file edits and explicit `workdir` for every shell call;
  verify the target tree before writing. Resolve command paths relative to that
  explicit directory, not the repository root by habit.
- `git -C <worktree>` changes only that Git invocation's directory. Never append
  an unqualified `task verify` or commit to such a command expecting it to run in
  the worktree; invoke the gate separately with its explicit target `workdir`.
- Reused fixtures are not automatically eligible for a new lifecycle phase:
  explicitly establish required root availability, step intent, selected-source
  authority and valid operation snapshots. Do not weaken production checks to
  accommodate a fixture's default `unknown` status or incomplete evidence.
- For delegated worktree edits, enforce the child's session directory with
  `session_move` before permitting writes; prose instructions alone are not
  isolation. Start the child read-only, move that session, then assign edits.
  Verify changed-file locations before accepting the result.
- Normalized analysis fixtures transitioning to terminal states must clear their
  target selectors and set `finished_at` with a nonempty failure diagnostic.
  Retire the baseline operation and remove its exact-work holds/triples in the
  same transaction: deferred constraints reject either half committed alone.
  Rebuild any deleted pending steps before testing admission again. A second work needs
  its own location identity, not a copied `current_location_id`.
- Prefer `SettleNormalizedSourceAnalysisOperation` for retiring analysis fixtures;
  manually deleting operations can invalidate deferred step-history checks.
  Terminal normalized operations cannot retain work holds by schema: cover live
  terminal deliveries without holds separately from active admitted hold owners.
- Rate-gate cancellation tests must not depend on real scheduler latency, including
  an assumption that cancellation starts before a short reservation expires. Use
  fake time and a blocked-wait handshake; assert unchanged reservation state after
  cancellation and exactly one interval for the next successful dispatch.
- New migrations must update the inventory and explicit schema/table-count checks.
  JSON contract tests compare semantic fields or JSONB values, not incidental JSON
  text formatting. A rollback must refuse to discard retained recovery authority.
- PostgreSQL CHECK accepts SQL NULL: authority JSON predicates must require their
  keys and evaluate `IS TRUE`. Nullable composite authority identities need explicit
  all-null/all-present checks (and `MATCH FULL` where applicable); test every
  partial-null combination, missing keys, and malformed nested entries.
  `MATCH FULL` covers the entire FK: it cannot accompany an always-present parent
  ID plus an intentionally absent consumption suffix. Test valid unconsumed and
  consumed baselines, and reject reset/rebinding of consumed authority.
- Filesystem-execution fixtures must freeze native absolute roots before admission:
  Unix-only literals such as `/source` lack a volume on Windows. Use `t.TempDir()`
  and align root/work configured and inventory paths before capturing snapshots;
  never relax production path checks or rewrite frozen grants to fix fixtures.
- Keep one effective trigger-function definition per new migration. A later
  `CREATE OR REPLACE` can silently overwrite an earlier safety fix; inspect the
  final definition and prove rejection through a real SQL bypass regression.
- CURRENT-frontier tests distinguish incoming evidence from new resolution rows:
  acquiring a successor creates no acknowledgements. Completed incoming items
  must acknowledge with zero filesystem callbacks, including interrupted ACKs.
- Re-read canonical metadata into a fresh Bun model after replacing a winning
  capture: scanning SQL NULL into a reused nullable field can retain its former
  value in memory. Test both recording and clearing optional cover diagnostics.
- Apply the artwork size limit to new source ingestion, not generic CURRENT
  inspection. Legacy embedded artwork must remain observable for preservation
  and optional-failure restoration even when it exceeds the new input limit.
- Verify notice filenames against each pinned native source archive; do not
  assume `COPYING`. libvips 8.18.7 uses `LICENSE`, and libtiff 4.7.1 uses
  `LICENSE.md`. A successful dependency compilation followed by failed notice
  staging is still a failed Windows gate, not a validated platform.

**Local verification rule**: `task verify` is the only local test/build gate. Do not run additional local test suites, ad-hoc test selections, platform builds or cross-compilations for the platform matrix — those belong to GitHub CI. Reach for a narrower run only when `task verify` fails and the failure must be diagnosed.

Coordinate local full gates across all agent worktrees: run only one `task verify`
or commit hook at a time. Parallel code work does not authorize concurrent full
PostgreSQL integration suites. If a gate times out, inspect active tests and DB
waits before attributing it to contention; do not raise timeouts, skip tests, or
weaken normal hooks to obtain a pass.

```bash
# Prerequisites: Go 1.27, Node 24 LTS, Task 3, golangci-lint 2.14, Docker Compose, pre-commit

# Full development cycle
```
task verify              # Full gate: generate, lint, test, build

# Build and run
task build               # Frontend (vite) + backend (go build)
task run                 # Docker Compose (PostgreSQL + app on :8080)
task stop                # Stop compose

# Development
task generate            # Huma->OpenAPI->Orval pipeline
task test                # Go integration + Vitest + tools check
task lint                # golangci-lint + Biome
task format              # gofmt + Biome format

# Migration
task migrate:rollback    # Stop server first; rollback via backend/cmd/migrate

# Frontend only (dev server with /api proxy to 127.0.0.1:8080)
npm --prefix frontend run dev

# Backend only
cd backend && golangci-lint run ./...
```

## NOTES
**Legacy naming**: Go module path stays `github.com/ruckus/MusicEnreachment/backend` until prototype merge; product name is MeloTrove.

**Managed tools are downloaded and verified, not installed**: Setup and the Settings screen download a selected release from an approved source into the managed tools directory, verify its checksum when the source publishes one and run every executable's version query before recording it. Do not describe this as installation; an installation is a package manager or an OS installer.

**Windows arm64 unsupported**: Explicitly documented limitation.

**Domain map freshness**: symbol reference counts, file sizes, and hotspot estimates are omitted because they drift quickly; consult the source tree and domain-specific AGENTS.md files.
