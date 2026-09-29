# MeloTrove

Server application for managing a personal music library, intended primarily
for deployment on a NAS or another always-on local server. The application is
operated through a desktop-first web UI; it is not a desktop executable, and a
mobile-first interface is not a product goal.

## Filesystem model

MeloTrove treats configured source directories as external inputs, not as
the managed library. Sources are read-only by default and remain separate from
the writable output directory containing managed publications. Optional source
deletion is allowed only when explicitly enabled and only after a successful
publication.

Each source root will support direct in-place analysis or staged processing. The
staged mode uses an explicitly configured work directory, which should be a bind
mount or volume rather than container overlay storage. An unavailable source or
an interrupted scan must not erase the last successfully observed inventory or
existing managed publications.

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
`tools-data` volume. The image contains no bundled audio tools. Setup installs
selected tools there from the approved sources. Compose bind-mounts
`${MELOTROVE_OUTPUT_DIR:-./music}` from the host at
`/var/lib/melotrove/output`; runtime settings store and the backend use only
this server/container path, never the host path. During Setup the output
directory must be empty; an existing library is not imported.

## Initial setup and managed tools

On a fresh database MeloTrove opens the one-time Setup Manager. It records the
current supported instance platform, requires an absolute writable tools path,
an absolute **empty** writable output path, an explicit publication format and
verified active FFmpeg and Chromaprint installations. Completing Setup is
irreversible: a later configuration problem is shown as configuration health in
Settings and does not reopen Setup. Settings also edits runtime/provider/logging
configuration and manages installed tool versions through explicit install,
activation, deletion, and tools-root move operations.

The tools root may contain unrelated files. MeloTrove writes and removes only
exact paths for installations recorded in PostgreSQL (`ffmpeg/<version>/` and
`fpcalc/<version>/`); it does not scan the tools root for unknown files or
delete them. An unknown file
at an exact installation target requires explicit overwrite confirmation. When
moving a tools root, only recorded installation files are copied and verified
before switching the setting.

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
  managed tools ownership and platform recovery.
- [Plans](docs/plans/README.md) explain completed, executable, and future work.
