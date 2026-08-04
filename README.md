# Music Ingest

Music Ingest receives Lidarr download webhooks, records immutable source provenance, validates and sanitizes FLAC files, publishes a reviewable media copy, and exposes the review UI/API.

## Runtime flow

```text
Lidarr webhook
  -> api/lidarr_intake.py
  -> intake/service.py
  -> persistence models and repositories
  -> processing/worker.py
  -> inspectors + sanitizers + normalize
  -> publication/service.py
  -> media library and review records
```

The service starts one FastAPI process and one in-process polling worker. PostgreSQL migrations run before the application becomes ready.

## Package layout

| Package | Responsibility |
| --- | --- |
| `api/` | FastAPI routes and runtime composition |
| `cli/` | CLI-only operations such as the non-mutating dry run |
| `config/` | Typed YAML policy parsing and safe summaries |
| `enrichment/` | Optional artwork and fingerprint capabilities |
| `intake/` | Source intake and provenance registration |
| `integrations/` | Lidarr and Navidrome adapters |
| `inspectors/` | Media structure inspection |
| `lyrics/` | Lyrics validation |
| `matching/` | Provider adapters, evidence, and scoring |
| `normalize/` | Canonical metadata normalization and writing |
| `persistence/` | SQLAlchemy models and repositories |
| `processing/` | Worker orchestration, fallback metadata, and polling runtime |
| `publication/` | Staged-release validation and atomic publication |
| `review/` | Source and release review services |
| `sanitizers/` | Media sanitization |
| `ui/` | Review page assets |

## Commands

Install dependencies with `uv sync`.

```bash
uv run pytest
uv run ruff check src tests alembic
uv run ruff format --check src tests alembic
uv run ty check

PYTHONPATH=src uv run python -m music_ingest dry-run SOURCE_DIRECTORY REPORT_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest POLICY_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest serve
```

The `serve` command requires `MUSIC_INGEST_DATABASE_URL` to be a PostgreSQL URL. Configure only the incoming root, final media root, and transient staging root with `MUSIC_INGEST_*_ROOT` environment variables. Tags, versions, provider evidence, review decisions, failure reasons, and publication metadata are stored in PostgreSQL. Set `MUSIC_INGEST_API_TOKEN` in any network-exposed deployment; when set, it protects every `/api/` route with either `X-API-Key` or `Authorization: Bearer`.

## Local integration stand

```bash
cd test_stand
docker compose up --build --wait
curl --fail http://localhost:8787/healthz
docker compose down
```

The test stand is intentionally disposable infrastructure for quickly checking Lidarr, PostgreSQL, the worker, and Navidrome together. Its local environment file is not production configuration.

## Safety boundaries

- Incoming media is treated as read-only source input.
- Intake records path, inode, size, and SHA-256 provenance.
- Staging is validated before publication.
- Invalid or changed sources are quarantined instead of published.
- Dry-run reports are written outside the source tree and do not mutate source files.
- Provider and enrichment modules are isolated capabilities; the current worker uses the explicit fallback path when provider enrichment is unavailable.
