# Music Ingest

Music Ingest receives Lidarr download webhooks, records immutable source provenance, validates media, publishes a reviewable media copy, and exposes the review UI/API.

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
PYTHONPATH=src uv run python -m music_ingest media-stage INPUT OUTPUT_DIRECTORY TMP_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest POLICY_DIRECTORY
PYTHONPATH=src uv run python -m music_ingest serve
```

`media-stage` runs the same database-free media inspection, fingerprint calculation, staging, metadata, artwork,
and final decoder validation stages used by the production worker. It writes one derived audio file below
`OUTPUT_DIRECTORY`, keeps temporary files below `TMP_DIRECTORY`, and never mutates the input. It does not persist
fingerprint evidence or publish to the managed library, because those are database-backed worker orchestration steps.

For a local profile, wrap the same command with Python's profiler:

```bash
PYTHONPATH=src uv run python -m cProfile -o media-stage.prof -m music_ingest media-stage INPUT OUTPUT_DIRECTORY TMP_DIRECTORY
```

The `serve` command requires `MUSIC_INGEST_DATABASE_URL` to be a PostgreSQL URL. Configure `MUSIC_INGEST_SOURCE_ROOTS_PARENT`, then add each source root through Settings; configure final media and transient staging roots with environment variables. Tags, versions, provider evidence, review decisions, failure reasons, and publication metadata are stored in PostgreSQL. Production authentication is owned by the reverse proxy; the local UI and API are public.
Enabled source roots are automatically reconciled once per hour by default; set `MUSIC_INGEST_RECONCILIATION_INTERVAL_SECONDS` to change the interval.

`MUSIC_INGEST_SOURCE_ROOTS_PARENT` must be an existing, non-symlink directory. Each configured source root must be an existing, non-symlink immediate child of that mounted parent. Source roots are read-only inputs. The final media root is writable, while the staging root is disposable.

## Formats and matching

Ingest supports FLAC, MP3, M4A (AAC or ALAC), Ogg Vorbis, and Opus. Every publication is an MKA/Matroska container with the source audio stream copied without re-encoding. Canonical Matroska tags use the same allowlisted names as FLAC/Vorbis Comments and are written and verified through FFmpeg/FFprobe. Raw AAC and arbitrary scanner-recognized extensions are not publication formats.

Candidate matching prefers an explicit MusicBrainz ID. Otherwise it scores normalized artist and release text, with duration and release-position matches contributing when available. Ambiguous, stale, unsafe, unavailable, or below-threshold results remain in review rather than being auto-selected.

Published audio uses the `.mka` extension and stable artist, album, and track layout. A replacement is staged and verified before the current publication is superseded. The incoming source pathname is never replaced, and source files are never mutated.

## Local integration stand

```bash
cd test_stand
docker compose up --build --wait
curl --fail http://127.0.0.1:8787/healthz
docker compose down
```

The test stand is intentionally disposable infrastructure for quickly checking Lidarr, PostgreSQL, the worker, and Navidrome together. Its local environment file is not production configuration.

## Safety boundaries

- Incoming media is treated as read-only source input.
- Intake records path, inode, size, and SHA-256 provenance.
- Staging is validated before publication.
- Invalid or changed sources are quarantined instead of published.
- Dry-run reports are written outside the source tree and do not mutate source files.
- Publication supersedes current audio only after staged output, manifest, and hash checks succeed. `.nfo` files are never removed.
- Provider and enrichment modules are isolated capabilities; the current worker uses the explicit fallback path when provider enrichment is unavailable.
