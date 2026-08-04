# Music Ingest Deployment Contract

Production deployment is a Komodo-held Stack only. The sole stack description is
[`komodo/music-ingest.stack.yaml`](../komodo/music-ingest.stack.yaml); do not
create a host Compose configuration.

## Runtime Topology

Set Komodo's `MUSIC_INGEST_IMAGE` variable to the immutable image digest produced
by CI, for example `ghcr.io/your-org/music-ingest@sha256:<digest>`. The stack
rejects deployment when this variable is absent, and it deliberately has no
checked-in placeholder image.

Komodo runs one `music-ingest` container with the `python -m music_ingest serve`
command. That runtime upgrades the externally managed PostgreSQL database to
Alembic head before serving and runs the API and processing worker together.
It exposes port 8000 only on Komodo's internal network; do not publish an
unauthenticated application port to the host or public network.

Lidarr must reach `http://music-ingest:8000/api/intake/lidarr` on that internal
network. The API has no application authentication by design, so this endpoint
is restricted by the deployment network boundary.

## Operator Configuration

Copy the six files in `config/templates/` to
`/mnt/ssd/appdata/music-ingest/`. Templates are safe defaults, not credentials.
Inject the PostgreSQL URL and any optional AcoustID reference through Infisical.
Replace the placeholder MusicBrainz contact before enabling MusicBrainz. Keep
AcoustID disabled unless an Infisical reference has been configured.

Configure Lidarr to import into `/mnt/pool/data/music-incoming`, then mount that
path read-only as `/data/incoming` in `music-ingest`. Mount
`/mnt/pool/data/media` as writable `/data/publish/music` for final media, and
mount `/mnt/ssd/appdata/music-ingest` as `/appdata/music-ingest` for disposable
staging only. Navidrome must mount `/mnt/pool/data/media` read-only at its music
root. PostgreSQL is the only durable store for tags, versions, provenance,
review decisions, failure reasons, and publication metadata. The normal workflow
does not replace a Lidarr incoming pathname, and Task 9a remains separately gated.

The PostgreSQL URL is an external Infisical reference. Do not add PostgreSQL to
the Komodo stack or use a local SQLite fallback. Startup migrations are owned by
the runtime and complete before the health check succeeds.

## Dry-Run Migration

Run the scanner against a copied fixture or an operator-selected read-only
library root:

```sh
PYTHONPATH=src uv run python -m music_ingest dry-run /path/to/library reports/dry-run/sample
```

It writes `summary.json`, `review.csv`, and `proposals.jsonl` outside the source
tree. It records hashes, timestamps, FLAC/MP3 inspection state, and NFO/LRC
presence without reading untrusted sidecar text into reports. It never removes `.nfo`,
changes audio, writes tags, or publishes media. Review the initial
Anacondaz, Noize MC, and Linkin Park sample reports separately; the event-driven
runtime publishes valid media automatically and its review queue is attention-only.
Interrupted runs can be rerun because each artifact is atomically replaced.

## Validation And Provider Safety

Normal tests deny public sockets. Live provider checks are opt-in only:

```sh
MUSIC_INGEST_ENABLE_LIVE_TESTS=1 uv run pytest -m live -q
```

Do not enable live tests in Komodo health checks, CI defaults, or migration
automation. Before deployment, run the offline test suite, Ruff, and ty. The
stack image supplies `fpcalc`, `flac`, and `ffprobe`; it must not depend on host
tools.
