# Music Ingest: worker guide

## Purpose and non-negotiable safety model

Music Ingest receives Lidarr/manual incoming audio, records immutable provenance, analyzes it, and publishes a managed copy for review. The application is **source-agnostic**: Lidarr is only one intake mechanism.

Treat every configured source root and its files as read-only evidence:

- Never edit, rename, move, replace, or delete an incoming source file, including `.nfo` files.
- A changed source is a new observation/version, never an overwrite of prior provenance.
- Source roots must be existing, non-symlink immediate children of `MUSIC_INGEST_SOURCE_ROOTS_PARENT`.
- Write derived files only to the staging, managed-media, or report roots. Dry-run reports must be outside the source tree.
- Publish through the existing staged-validation/atomic-replacement flow. Do not expose a new publication before its manifest and hashes are validated; preserve publication history.
- Uncertain, stale, unsafe, unavailable, ambiguous, or low-confidence matching results must remain reviewable rather than being auto-selected.

The stable domain identity is `LibraryRecord`, not a source path, filename, webhook, or current publication. Preserve append-only source, metadata, and event history.

## Repository map

| Path | What belongs here |
| --- | --- |
| `src/music_ingest/api/` | FastAPI routes, startup/runtime composition, and static UI serving |
| `src/music_ingest/cli/` | CLI commands, notably the non-mutating `dry-run` |
| `src/music_ingest/intake/`, `processing/`, `publication/` | Intake, worker orchestration, and safe staged publication pipeline |
| `src/music_ingest/persistence/` | SQLAlchemy models, repositories, and durable state |
| `src/music_ingest/matching/`, `enrichment/`, `inspectors/`, `sanitizers/`, `normalize/`, `lyrics/` | Media analysis and optional provider capabilities |
| `src/music_ingest/review/`, `integrations/`, `config/` | Review domain, Lidarr/Navidrome adapters, typed policy parsing |
| `alembic/` | PostgreSQL schema migrations; add a migration for persisted-schema changes |
| `tests/` | Unit and contract tests; `tests/integration/` holds opt-in/disposable-infrastructure scenarios |
| `frontend/src/` | React/Vite review UI, API client, types, and components |
| `frontend/e2e/` | Playwright end-to-end tests against an externally started service |
| `test_stand/` | Disposable local Docker Compose stand: PostgreSQL, application, Lidarr, and Navidrome |
| `komodo/` and `docs/deployment.md` | Production deployment contract; production is Komodo-only, not Docker Compose |
| `DESIGN.md` | UI/product contract: library model, visual tokens, layouts, accessibility, and accepted debt |

## Runtime flow

`Lidarr webhook -> api/lidarr_intake.py -> intake/service.py -> persistence -> processing/worker.py -> inspection/sanitization/normalization -> publication/service.py -> managed media + review records`

`serve` starts FastAPI plus an in-process polling worker. Migrations run before readiness. Production uses PostgreSQL; test code may use SQLite where the relevant model behavior is portable.

## Tooling and validation

Python requires 3.14+ and uses `uv`. Install dependencies once with:

```bash
uv sync
```

Run the applicable checks after changes:

```bash
uv run pytest
uv run ruff check src tests alembic
uv run ruff format --check src tests alembic
uv run ty check
```

Ruff targets Python 3.14, has a 120-character line limit, and formats with single quotes. `pytest` already adds `src` to `pythonpath`. `ty` is also required by pre-commit. Do not weaken tests or suppress typing errors.

For focused work, run the affected test module first, then the full suite when practical:

```bash
uv run pytest tests/test_source_roots.py
uv run pytest tests/test_publication_attempts.py
```

The frontend has its own Node toolchain and Biome conventions: two-space indentation, double quotes, semicolons, trailing commas, and a 100-column formatter width.

```bash
npm run check --prefix frontend
npm run test --prefix frontend
npm run build --prefix frontend
```

Playwright requires a live service URL and seeds its own test state:

```bash
MUSIC_INGEST_E2E_BASE_URL=http://127.0.0.1:8787 npm run test:e2e --prefix frontend
```

For UI work, use a real browser and verify responsive behavior, keyboard/focus treatment, loading/error feedback, and reduced-motion behavior. `DESIGN.md` is authoritative: source, publication, match, and metadata evidence belongs in the selected track inspector; only final metadata is editable.

## Running the application

```bash
PYTHONPATH=src uv run python -m music_ingest dry-run SOURCE_DIRECTORY REPORT_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest POLICY_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest serve
```

`serve` needs `MUSIC_INGEST_DATABASE_URL` plus configured source, incoming, staging, and media roots. The local UI/API is public; production authentication is handled by the reverse proxy. Do not add app-level token authentication merely because the deployment is public.

Use the disposable integration stand only for end-to-end changes that need it:

```bash
cd test_stand
docker compose up --build --wait
curl --fail http://127.0.0.1:8787/healthz
docker compose down
```

Live tests require explicit opt-in with `MUSIC_INGEST_ENABLE_LIVE_TESTS=1`. Never use the local stand's environment files as production configuration. Production deployment changes must preserve the Komodo stack contract and avoid committing secrets or `.env` values.

## Implementation conventions

- Follow the existing layer boundary: routes parse/return HTTP models, services own domain decisions, repositories/persistence own durable access, and adapters isolate external systems.
- Use typed models and explicit domain states. Keep `original`, `analyzed`, and editable `final` metadata distinct.
- Add or update Alembic migrations with model/schema changes, and test migration behavior in `tests/test_migrations.py` where relevant.
- Keep external/provider failures contained as capability outcomes; they must not erase provenance or bypass review.
- Test behavior at its boundary: FastAPI/API changes with API tests, worker/publication changes with focused unit or contract tests, and browser-visible UI changes with frontend checks plus browser QA.
- Keep backend and frontend API types in sync. The browser client lives in `frontend/src/api/client.ts`; avoid duplicating untyped request/response shapes in components.
- Preserve accessible native controls, visible focus indicators, text equivalents for state, and Russian UI copy where the existing surface uses it.
- Do not change `test_stand/` or `komodo/` as a side effect of ordinary application changes; topology and deployment documentation have explicit contract tests.

## Before handing off

1. Re-read the safety boundaries above and confirm no source mutation path was introduced.
2. Run the narrowest relevant tests and formatter/linter/type checks for each changed runtime.
3. If behavior is user-facing, use its surface: CLI command, HTTP endpoint, or browser UI. Build/test success alone is not sufficient.
4. State any environment-bound validation you could not run, especially PostgreSQL, Docker, provider, or live-test checks.
