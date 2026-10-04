# MeloTrove

Server application for managing a personal music library, intended primarily
for deployment on a NAS or another always-on local server. The application is
operated through a desktop-first web UI; it is not a desktop executable, and a
mobile-first interface is not a product goal.

## Filesystem model

MeloTrove treats configured source directories as external inputs, not as
the managed library. Sources are read-only here: MeloTrove never creates,
changes or deletes anything inside them, and they remain separate from the
writable output directory containing managed publications.

Deleting a registered source root is not a file operation. It removes only that
root's inventory rows from the database and leaves the source files on disk and
the managed output library in place. Deleting the source files themselves is not
available in this slice: no operation in this build removes or modifies a file
below a source root. It is future functionality, planned as a separate,
explicitly enabled operator setting that may delete a source file only after a
successful publication.

A source root is registered by its absolute path on the server that runs
MeloTrove, never by a path on the browser workstation. This slice reads a source
directly; staged processing, which copies files into an explicitly configured
work directory (a bind mount or volume rather than container overlay storage),
is later work with its own scratch mount. There is no environment variable for
source roots or for a work directory: registered roots are runtime settings in
PostgreSQL, edited through the UI. An unavailable source or an interrupted scan
must not erase the last successfully observed inventory or existing managed
publications.

The current Windows implementation rejects SMB/UNC source roots (for example,
`\\server\share`, including slash aliases) before filesystem access; legacy
entries remain readable, while scan and analysis fail with an actionable
unsupported-path message. This is an implementation limitation, not a permanent
product prohibition. If NAS/UNC access is required, it may be implemented using
ordinary pathname and `stat` checks subject to functional requirements; this
README does not claim that UNC currently works.

## Supported platforms and external tools

Target platforms are Linux and macOS (`amd64`, `arm64`) and Windows (`amd64`).
Windows `arm64` is not supported yet: the approved Chromaprint releases do not
provide a ready-made `fpcalc` binary for it.

During initial setup, the application downloads tools into a persistent tools
directory from these approved sources:

- `fpcalc`: https://github.com/acoustid/chromaprint/releases
- Numbered GPL releases of `ffmpeg` and `ffprobe`, Windows/Linux:
  https://github.com/BtbN/FFmpeg-Builds/releases
- GPL builds of `ffmpeg` and `ffprobe`, macOS:
  https://ffmpeg.martin-riedl.de/ — release builds only, no snapshots.

FFmpeg is developed by the [FFmpeg project](https://ffmpeg.org/); its source code
is available at https://github.com/FFmpeg/FFmpeg. MeloTrove does not bundle or
redistribute FFmpeg: Setup downloads a numbered release from its approved
third-party source and runs it as a separate executable.

## Development

Install these prerequisites:

- [Go 1.27](https://go.dev/doc/install);
- [Node.js 24 LTS](https://nodejs.org/) with npm;
- [Task 3](https://taskfile.dev/docs/installation);
- [golangci-lint 2.14](https://golangci-lint.run/docs/welcome/install/);
- [Docker with Compose](https://docs.docker.com/engine/install/) for the local
  PostgreSQL deployment and deployment smoke checks;
- [pre-commit](https://pre-commit.com/#install) for the repository hook.

Install locked frontend dependencies with `task frontend:install`, then run
`task build`, `task test`, `task generate`, or the complete `task verify` from
the repository root. Enable the hook with `pre-commit install`. The pre-commit
hook intentionally runs the complete `task verify`; it can be split into faster
stages later if repository growth makes that necessary.

For frontend development, `npm --prefix frontend run dev` starts Vite. Its
`/api` and `/health` requests are proxied to a backend listening on
`127.0.0.1:8080`, preserving the production same-origin request model without
CORS.

## Local launch

Run `task run` from the repository root to build the application and start it
with PostgreSQL through Docker Compose. The application is available at
`http://127.0.0.1:8080` by default. Stop it with Ctrl+C; add `-d` to the
underlying Compose command when a detached run is needed.

To use an external PostgreSQL instance, run `task build` and start
`./build/backend/server` with `DATABASE_URL` set to its PostgreSQL connection
URL.

The approved configuration model keeps environment variables limited to startup
bootstrap: `DATABASE_URL` plus optional `HTTP_BIND_ADDRESS` (an IP address,
default `0.0.0.0`) and `HTTP_PORT` (an integer from `1` through `65535`, default
`8080`). All other settings—including log level and external API keys—belong to
the runtime UI and PostgreSQL rather than environment variables or config files.

Compose persists downloaded tools at `/var/lib/melotrove/tools` in the
`tools-data` volume. The image contains no bundled audio tools. Setup downloads
and verifies selected tools there from the approved sources. Compose bind-mounts
`${MELOTROVE_OUTPUT_DIR:-./music}` from the host at
`/var/lib/melotrove/output`; runtime settings store and the backend use only
this server/container path, never the host path. During Setup the output
directory must be empty; an existing library is not imported.

## Initial setup and managed tools

On a fresh database MeloTrove opens the one-time Setup Manager. It records the
current supported instance platform, requires an absolute writable tools path,
an absolute **empty** writable output path, an explicit publication format, and FFmpeg and Chromaprint
versions that were downloaded and verified. Completing Setup is
irreversible: a later configuration problem is shown as configuration health in
Settings and does not reopen Setup. Settings also edits runtime/provider/logging
configuration and manages verified tool versions through explicit download,
activation, deletion, and tools-root move operations.

The tools root may contain unrelated files. MeloTrove writes and removes only
exact paths for managed versions recorded in PostgreSQL (`ffmpeg/<version>/` and
`fpcalc/<version>/`); it does not scan the tools root for unknown files or
delete them. An unknown file at an exact managed executable path requires
explicit overwrite confirmation. When moving a tools root, only recorded tool
files are copied and verified before switching the setting.

Platform is immutable for an instance. Moving a PostgreSQL database to a
different OS/architecture leaves diagnostics and the UI available, but marks
readiness unsuccessful and blocks Setup/product operations. There is no automatic
platform migration: restore the recorded platform or initialize a separate
instance database. macOS Intel (`darwin/amd64`) can use the final compatible
release build offered by the approved source; if that source stops producing
Intel builds, no newer compatible release will be available.

The application API is an internal contract for the bundled web UI. Operational
probes are available at `/health/live` and `/health/ready`; the image healthcheck
uses readiness automatically. Runtime logs are structured JSON and every HTTP
response includes `X-Request-ID` for correlation.

## Source inventory

After Setup, the Sources screen (`#/sources`) registers server directories and
reads their inventory. Every path shown and accepted there belongs to the
machine running MeloTrove, and a root must be the absolute path of an existing
readable directory that overlaps neither the managed tools root nor the output
directory. The browser never reads the filesystem of the workstation it runs on,
and no file is uploaded to the server.

Mount host source directories into the container read-only. The base Compose
file contains no such mount; add one to the `app` service that maps an absolute
host directory to the same absolute container path, so that the operator
registers the path they already know:

```yaml
    volumes:
      - tools-data:/var/lib/melotrove/tools
      - ${MELOTROVE_OUTPUT_DIR:-./music}:/var/lib/melotrove/output
      - /srv/music/sources:/srv/music/sources:ro
```

Registering a root does not scan it. A scan starts only from the scan button and
runs as a background operation that reports the server stages `queued`,
`traversing` and `applying` instead of a percentage. It is refused while Setup is
incomplete, the instance platform is unsupported or mismatched, the root is
disabled, or another scan of the same root is already active. The walk reads the
tree directly, does not follow symlinks and cannot leave the root.

The managed `ffprobe` then answers one question only: whether the file contains
at least one audio stream. It runs on regular files whose extension matches one
of the thirteen approved audio extensions listed in
[Deployment and first setup](docs/design/deployment.md); the extension comparison
ignores case, while the stored relative path keeps the exact case of the file on
disk. A file without an audio
stream stays in the inventory with its own status. A failed probe becomes a
problem of that file with a safe reason, does not stop the rest of the scan, and
is checked again on the next scan, while a file that a successful probe already
answered is not probed again until it changes. The scan itself records only that
status: no fingerprint (`fpcalc`), no SHA-256, no tags, codec or duration, and no
publication.

A separate, explicit **Analyze** action on one `audio` location reads that file
directly through the managed `ffprobe` and stores a technical result on the
location. The result contains the container format and duration, every audio
stream in stream-index order with its codec, profile, sample rate, sample format,
bits per sample, channels, channel layout, bit rate and duration, the tags
observed in the source (names uppercased, every value kept as its own entry and
never split on `;` or `/`), and the unmodified `ffprobe` JSON. Non-audio streams
such as an attached picture stay in that raw JSON. The result also records the
analysis policy version, the `ffprobe` version that produced it, and the time of
the analysis; an unknown technical value is shown as unknown, never as zero.

Analysis is read-only and in place. This slice has no staged mode, no work
directory, and no SHA-256 setting, and it never runs automatically: registering a
root or scanning it does not start an analysis. It is started only for a single
file that has an `audio` status, on an enabled root with a current inventory,
after Setup is complete and a managed FFmpeg package is ready, through the same
explicit action. Repeating the analysis re-reads the source file and replaces the
stored result; the source bytes are never created, changed or deleted. A failure
(for example, the file became unreadable between the read that provided its
identity and the analysis) leaves the previous valid result and its timestamp in
place, reports a safe reason on the operation, and changes neither the inventory
nor the availability of the root. After the file changes on disk and a successful
scan observes the new size or mtime, the stored result no longer describes the
location and is unlinked from it until a new analysis is requested. Deleting a
root removes only that root's inventory rows; source files on disk and the
managed output library stay untouched. Fingerprint (`fpcalc`), SHA-256 exact
deduplication, staged mode, automatic analysis of new files, incoming grouping
and publication remain later work.

Only a fully successful traversal replaces the inventory. A new, changed or
deleted path appears after such a scan, while an unavailable root, a failed,
interrupted or canceled scan leaves the last successful inventory visible
together with the error instead of an empty result. Editing a root's path marks
the existing inventory stale: the UI keeps listing the files with the path they
were found under (`inventory_path`) until a successful scan of the new path
replaces them, so old records are never presented as the inventory of the new
path. Disabling a root keeps its records and refuses new scans. Deleting a root,
after explicit confirmation of its path and record count, removes only that
root's inventory rows from the database: the source files on disk and the
managed output library stay untouched. No shipped operation deletes a source
file; that remains a later, explicitly enabled setting.

## Security and network exposure

MeloTrove intentionally has no built-in authentication or authorization:
there are no users, sessions, API tokens, roles, or permissions. Every client
that can reach the backend has full access to all application operations. The
deployment operator is responsible for TLS, authentication, and network access,
for example with Caddy and Authentik forward-auth or another reverse proxy.

The bundled UI and API are expected to use the same origin; the application does
not enable CORS or trust identity headers supplied by a proxy. The default
Compose deployment publishes port 8080 only on `127.0.0.1`, which is suitable
for a reverse proxy running on the host. The standalone binary listens on
`:8080` on all interfaces, so do not expose it to an untrusted network without
an appropriate external access boundary.

The image currently runs with Docker's default user for straightforward NAS
volume compatibility. A deployment that manages host permissions can override
`user: "UID:GID"` in Compose after ensuring that the tools, work, output, and
database-adjacent mounts required by the app are writable by that identity.

To roll back the last application migration group, stop the application and run
`task migrate:rollback` with `DATABASE_URL` pointing to the database. This uses
embedded down-migrations; River's schema is managed separately. Starting the
application again reapplies pending application migrations.

## Generated files

Build output is always generated locally in `build/` and is never committed.
Task stages the frontend only transiently for Go embedding; those assets are
ignored as well. Commit source code, lockfiles, and generated API contracts
only — never build artifacts.

The GitHub repository name and Go module/import path temporarily retain the
legacy `MusicEnreachment` identifier. They will be renamed together after the
prototype branch is merged, so Go imports remain valid in the meantime.

## Design documentation

- [Requirements](docs/design/requirements.md) describe the product boundary.
- [Decisions](docs/design/decisions.md) contain approved product and technical
  decisions.
- [Repository architecture](docs/design/repository-architecture.md) describes
  component responsibilities and runtime structure.
- [Deployment and first setup](docs/design/deployment.md) explains server paths,
  read-only source mounts, managed tools ownership and platform recovery.
- [Plans](docs/plans/README.md) explain completed, executable, and future work.
