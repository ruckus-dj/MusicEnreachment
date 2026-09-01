# Music Ingest contributor guide

## Project map

- `src/music_ingest/` is the Python 3.14 FastAPI service. Follow the domain flow: intake → persisted provenance → processing worker → inspection/sanitization/normalization → staged publication and review.
- `frontend/` is a separate React, TypeScript, and Vite application. Its build output is served by FastAPI from `src/music_ingest/ui/dist`.
- `tests/` contains the backend test suite and fixtures; `alembic/` contains database migrations.
- `test_stand/` is disposable Docker integration infrastructure for PostgreSQL, Lidarr, the worker, and Navidrome.

## Setup and validation

Use `uv` for Python and `npm` for the frontend:

```sh
uv sync --locked
npm ci --prefix frontend
```

Run the checks relevant to changed code. Before handing off a cross-stack change, run the full CI-equivalent set:

```sh
uv run pytest
uv run ruff check src tests alembic
uv run ruff format --check src tests alembic
uv run ty check src
npm run build --prefix frontend
npm run check --prefix frontend
```

Useful focused frontend commands:

```sh
npm run test --prefix frontend
npm run test:e2e --prefix frontend
npm run format:check --prefix frontend
npm run lint --prefix frontend
npm run typecheck --prefix frontend
```

Python media paths require `ffmpeg`, `ffprobe`, and `fpcalc` (Chromaprint). The default pytest suite is offline: do not add network dependencies to it. Live-provider tests are an explicit opt-in only:

```sh
MUSIC_INGEST_ENABLE_LIVE_TESTS=1 uv run pytest -m live -q
```

Pre-commit runs repository-wide Python checks, pytest, and frontend checks:

```sh
uv run pre-commit run --all-files
```

## Runtime and manual verification

```sh
PYTHONPATH=src uv run python -m music_ingest dry-run SOURCE_DIRECTORY REPORT_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest media-stage INPUT OUTPUT_DIRECTORY TMP_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest serve
```

- `dry-run` and `media-stage` must not mutate their input source files.
- `serve` starts FastAPI on port 8000, applies Alembic migrations before readiness, and starts the in-process worker. It requires a PostgreSQL `MUSIC_INGEST_DATABASE_URL`; SQLite is not a supported runtime database.
- Runtime filesystem paths are security boundaries: `MUSIC_INGEST_SOURCE_ROOTS_PARENT` and configured source roots must be existing non-symlink directories; source roots are immediate children of the parent and read-only. `MUSIC_INGEST_STAGING_ROOT` is disposable and writable; `MUSIC_INGEST_MEDIA_ROOT` is writable managed output.

For end-to-end integration work:

```sh
cd test_stand
docker compose up --build --wait
curl --fail http://127.0.0.1:8787/healthz
docker compose down
```

Do not use `docker compose down --volumes` unless an intentional database reset is required.

## Implementation rules

- Preserve provenance and immutability: never edit, delete, replace, or move incoming source media. Changed inputs are a new observed source version; invalid or changed files are quarantined.
- Preserve staged publication: validate staged output, manifest, and hashes before superseding current managed media. Never remove `.nfo` files.
- Keep provider/enrichment code optional and failure-aware. Ambiguous, unsafe, stale, unavailable, or low-confidence matches must remain reviewable rather than auto-selected.
- Keep Python code fully typed and follow existing package boundaries. Use Pydantic DTOs at API boundaries, SQLAlchemy repositories for persistence, and UTC-aware datetimes for persisted/provider timestamps.
- Follow Ruff formatting: 120-character lines and single quotes in Python. Do not bypass typing or linting with suppression comments.
- In frontend code, follow Biome conventions: 2-space indentation, 100-column width, double quotes, semicolons, and trailing commas. Keep API data typed and use the existing client/error patterns.
- When changing database models or persisted behavior, add an Alembic migration and test the migration-sensitive behavior. When changing backend endpoints consumed by the UI, update the typed frontend client and its tests in the same change.

## UI requirements

The product design contract is in `DESIGN.md`. Preserve semantic HTML, labeled native controls, visible focus, responsive reflow, readable contrast, and non-color-only status communication. Respect `prefers-reduced-motion`. Only Final metadata is editable; saving creates a new revision and must not mutate the source file.
