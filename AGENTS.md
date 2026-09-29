# PROJECT KNOWLEDGE BASE

**Generated:** 2026-09-28T00:00:00.000Z
**Commit:** HEAD
**Branch:** main

## OVERVIEW
MeloTrove (legacy name: MusicEnreachment) - music library manager with source inventory, audio analysis (Chromaprint), transcoding (ffmpeg), and publication. Go 1.27 backend (Huma/chi API, Bun ORM, River jobs, PostgreSQL) + React 19 frontend (Vite, TanStack Query, Orval-generated client). No auth by design; deployment-boundary security only.

## STRUCTURE
```
./
├── backend/               # Go 1.27 module, cmd + internal packages
│   ├── cmd/              # server (HTTP), migrate (rollback), openapi (export)
│   └── internal/         # See backend/internal/*/AGENTS.md for domain details
├── frontend/             # React 19 + Vite + Tailwind 4, see frontend/AGENTS.md
├── docs/
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
| Settings | backend/internal/settings | Filesystem-backed registry |
| Frontend screens | frontend/src/features/ | setup/ and settings/ only (early prototype) |
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
| settings.Registry | type | backend/internal/settings/settings.go | 8+ | Platform/log settings hub, filesystem-backed |
| integrations/tools.Catalog | type | backend/internal/integrations/tools/catalog.go | 6+ | GitHub release adapters for tool downloads |
| integrations/tools.Lifecycle | type | backend/internal/integrations/tools/lifecycle.go | 4+ | Tool materialization (download, verify, install) |
| SetupManager | component | frontend/src/features/setup/SetupManager.tsx | 2 | Frontend setup wizard (150 lines, largest frontend file) |
| SettingsScreen | component | frontend/src/features/settings/SettingsScreen.tsx | 1 | Settings UI (116 lines) |
| AppShell | component | frontend/src/routes/AppShell.tsx | 1 | Hash router: /setup and /settings |

## CONVENTIONS
**Config model inverted**: No config files; only 3 bootstrap env vars (DATABASE_URL, HTTP_BIND_ADDRESS, HTTP_PORT). All other settings are DB-backed runtime values edited via UI. Do NOT add new env vars.

**No auth by design**: No users, sessions, tokens, roles, CORS. Deployment-boundary auth only (e.g., Caddy + Authentik).

**Layering**: Technical layers (api/service/persistence/integrations/settings/jobs/migrations/static), NOT domain. api -> service -> persistence; only persistence touches Bun/PostgreSQL; River jobs enqueue atomically in the same Bun tx.

**Generated-code pipeline**: Huma generates OpenAPI from Go -> backend/cmd/openapi exports to frontend/openapi.json -> Orval generates TS client/hooks into frontend/src/api/generated. `task generate` enforces sync; tools/check-generated.mjs verifies drift.

**Error handling**: `fmt.Errorf("context: %w", err)` wrap with lowercase messages; `errors.New` for static; no custom error types in service layer; no `nolint`.

**Test org**: `task test` = `go test -tags=integration` (requires TEST_DATABASE_URL) + Vitest/RTL (*.test.tsx colocated) + node --test (tools/). Integration tests named `*_integration_test.go`.

**Build interface**: Taskfile only (`task build/test/verify/generate/run`); no shell scripts as build commands; pre-commit runs full `task verify` (intentional).

**Lint**: Go 1.27 gofmt + golangci-lint default linters; Biome (not ESLint/Prettier) for frontend; zero `nolint` usage.

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

**Local verification rule**: `task verify` is the only local test/build gate. Do not run additional local test suites, ad-hoc test selections, platform builds or cross-compilations for the platform matrix — those belong to GitHub CI. Reach for a narrower run only when `task verify` fails and the failure must be diagnosed.

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

**Hotspots for refactoring**: setup_manager.go (~300 lines, 24 methods), setup_manager_integration_test.go (446 lines), lifecycle.go (272 lines), catalog.go (233 lines).
