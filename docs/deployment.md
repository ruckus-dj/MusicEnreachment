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
It exposes port 8000 only on Komodo's internal network. Production authentication
is owned by the reverse proxy; the application does not configure credentials.

Lidarr must reach `http://music-ingest:8000/api/intake/lidarr` on that internal
network.

## Operator Configuration

Configure processing and external providers through the Settings tab in the
review UI. The stack keeps only database credentials, container mounts, and
runtime paths in its deployment environment.

The genre catalog is not an operator-maintained setting. On first Settings-tab
load, the service synchronizes the official MusicBrainz genre catalog through
`/ws/2/genre/all` with the configured MusicBrainz User-Agent. The UI keeps the
original MusicBrainz name for matching and shows a readable display label; the
manual aliases JSON is not part of the runtime contract.

Configure Lidarr to import into `/mnt/pool/data/music-incoming`, then mount that
path read-only as an immediate child of `/data/sources` in `music-ingest` and set
`MUSIC_INGEST_SOURCE_ROOTS_PARENT=/data/sources`. Configure the mounted directory
through **Settings → Source roots** before enabling Lidarr. Operators may configure
only existing, non-symlink immediate children of `/data/sources`.
Mount
`/mnt/pool/data/media` as writable `/data/publish/music` for final media, and
mount `/mnt/ssd/appdata/music-ingest` as `/appdata/music-ingest` for disposable
staging only. Navidrome must mount `/mnt/pool/data/media` read-only at its music
root and set `ND_SCANNER_PURGEMISSING=full` so confirmed missing files are removed
after full scans. PostgreSQL is the only durable store for tags, versions, provenance,
review decisions, failure reasons, and publication metadata. The normal workflow
does not replace a Lidarr incoming pathname, and Task 9a remains separately gated.

The source-roots parent must already exist, be a directory, and not be a symlink. Every configured root must be an existing, non-symlink immediate child. Ingest and publication support `.flac`, `.mp3`, `.m4a`, `.ogg`, and `.opus`. M4A accepts AAC and ALAC; Ogg accepts Vorbis and Opus according to its probed codec. Mutagen handles all tag reads, writes, and verification: Vorbis/Opus Comments for FLAC/Ogg/Opus, ID3v2.4 for MP3, and MP4 atoms for M4A. Raw AAC and unsupported containers remain outside the publication contract.

Matching gives priority to explicit MusicBrainz IDs, then scores normalized artist and release text plus duration when available. Ambiguous, stale, unsafe, unavailable, or below-threshold matches stay in review. Published audio keeps the source extension and stable artist, album, and track layout. A replacement is staged, checked by manifest and hash, then atomically exposed. The previous publication is retained until finalization, then marked superseded. Source files are never mutated, and `.nfo` files are never removed.

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
runtime publishes valid media automatically and records unresolved states on the stable library record.
Interrupted runs can be rerun because each report artifact is atomically replaced.

Publication recovery follows the recorded attempt state: `reserved` or `staged` attempts are cleaned up, with an existing backup restored when needed; a valid recoverable staged output advances to `exposed`; an `exposed` attempt is verified by manifest and output hash before finalization; failed verification restores the backup and records a retryable failure. Finalized attempts only need temporary directories removed.

## Validation And Provider Safety

Normal tests deny public sockets. Live provider checks are opt-in only:

```sh
MUSIC_INGEST_ENABLE_LIVE_TESTS=1 uv run pytest -m live -q
```

Do not enable live tests in Komodo health checks, CI defaults, or migration
automation. Before deployment, run the offline test suite, Ruff, and ty. The
stack image supplies `fpcalc`, `ffmpeg`, and `ffprobe`; FLAC structural sanitization is implemented in Python and decoder validation uses FFmpeg. It must not depend on host tools.
