# MusicEnreachment

Server application for managing a personal music library, intended primarily
for deployment on a NAS or another always-on local server. The application is
operated through a desktop-first web UI; it is not a desktop executable, and a
mobile-first interface is not a product goal.

## Supported platforms and external tools

Target platforms are Linux and macOS (`amd64`, `arm64`) and Windows (`amd64`).
Windows `arm64` is not supported yet: the approved Chromaprint releases do not
provide a ready-made `fpcalc` binary for it.

During initial setup, the application is intended to download tools into a
persistent tools directory from these approved sources:

- `fpcalc`: https://github.com/acoustid/chromaprint/releases
- `ffmpeg` and `ffprobe`, Windows/Linux: https://github.com/BtbN/FFmpeg-Builds/releases
- `ffmpeg` and `ffprobe`, macOS: https://ffmpeg.martin-riedl.de/ — release builds only, no snapshots.

Automatic download is not implemented yet. If tools are bundled in a Docker
image, they are immutable fallbacks; a newer downloaded and successfully verified
version takes precedence.

## Development

- Go 1.27 builds and tests the backend.
- Node.js 24 LTS and npm manage and build the Vite/TypeScript frontend.
- [Task](https://taskfile.dev/) is the cross-platform task runner for the
  monorepo. Run `task build`, `task test`, `task generate`, or `task verify`
  from the repository root.

## Local launch

Run `task run` from the repository root to build the application and start it
with PostgreSQL through Docker Compose. The application is available at
`http://127.0.0.1:8080` by default. Stop it with Ctrl+C; add `-d` to the
underlying Compose command when a detached run is needed.

To use an external PostgreSQL instance, run `task build` and start
`./build/backend/server` with `DATABASE_URL` set to its PostgreSQL connection
URL.

Compose persists downloaded tools at `/var/lib/music-enreachment/tools` in the
`tools-data` volume. The image contains no bundled audio tools. The future Setup
Manager will select this directory and download tools from the approved sources.

The application API is an internal contract for the bundled web UI. Operational
probes are available at `/health/live` and `/health/ready`; the image healthcheck
uses readiness automatically. Runtime logs are structured JSON and every HTTP
response includes `X-Request-ID` for correlation.

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
