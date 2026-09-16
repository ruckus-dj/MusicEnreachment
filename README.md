# Music Ingest

Music Ingest watches one or more read-only incoming music directories, creates a
reviewable managed copy, and provides a web UI and API for configuration and
review. It never edits, moves, or deletes an incoming source file.

This guide deploys the published container image:
`ghcr.io/ruckus-dj/musicenreachment`. It runs the application and PostgreSQL on
one Docker host. Put the UI behind an authenticated reverse proxy before exposing
it outside the host: the application itself has no authentication.

## Requirements

- Docker Engine with the Compose plugin (`docker compose version`)
- A writable directory for Docker configuration, PostgreSQL data, staging, and
  the managed media library
- A directory containing incoming music files. It is mounted read-only.
- Access to the GitHub Container Registry package at
  <https://github.com/ruckus-dj/MusicEnreachment/pkgs/container/musicenreachment>

The image supports `linux/amd64` and `linux/arm64` and includes FFmpeg, FFprobe,
and Chromaprint (`fpcalc`). If GitHub packages are private to your account,
authenticate on the Docker host before starting:

```sh
docker login ghcr.io
```

Use a GitHub personal access token with permission to read packages as the
password. Do not place that token in Compose files or `.env`.

## Install with Docker Compose

1. Create a deployment directory and the host directories that will be mounted
   into the containers. `sources` is a parent only; every configured source root
   must be an immediate child of it.

   ```sh
   mkdir -p music-ingest/{appdata,media,sources/incoming}
   cd music-ingest
   ```

   Copy or configure your downloader to write source media under
   `./sources/incoming`. Do not make `sources` itself a symlink, and do not
   mount source directories read-write.

2. Create `.env` with a database password. The hexadecimal value below is safe
   to embed in a PostgreSQL URL. Keep this file private because it contains the
   database password.

   ```sh
   umask 077
   printf 'POSTGRES_PASSWORD=%s\n' "$(openssl rand -hex 24)" > .env
   ```

3. Create `compose.yaml`:

   ```yaml
   services:
     postgres:
       image: postgres:17
       environment:
         POSTGRES_DB: music_ingest
         POSTGRES_USER: music_ingest
         POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
       volumes:
         - postgres-data:/var/lib/postgresql/data
       healthcheck:
         test: ["CMD-SHELL", "pg_isready -U music_ingest -d music_ingest"]
         interval: 5s
         timeout: 3s
         retries: 12
       restart: unless-stopped

     music-ingest:
       image: ghcr.io/ruckus-dj/musicenreachment:latest
       environment:
         MUSIC_INGEST_DATABASE_URL: postgresql+psycopg://music_ingest:${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}@postgres/music_ingest
       depends_on:
         postgres:
           condition: service_healthy
       ports:
         - "127.0.0.1:8000:8000"
       volumes:
         - ./sources:/data/sources:ro
         - ./media:/data/media
         - ./appdata:/appdata/music-ingest
       healthcheck:
         test: ["CMD", "python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=2)"]
         interval: 30s
         timeout: 3s
         retries: 3
         start_period: 30s
       restart: unless-stopped

   volumes:
     postgres-data:
   ```

   `latest` follows the default branch. For a repeatable upgrade, replace it
   with an immutable digest shown on the package page, for example
   `ghcr.io/ruckus-dj/musicenreachment@sha256:<digest>`.

4. Pull and start the stack. Startup applies database migrations before the
   application reports healthy.

   ```sh
   docker compose pull
   docker compose up -d
   docker compose ps
   curl --fail --silent --show-error http://127.0.0.1:8000/healthz
   ```

   The expected health response is JSON containing `"status":"ok"` and
   `"service":"music-ingest"`. If it does not become healthy, inspect the
   service logs:

   ```sh
   docker compose logs --tail=200 music-ingest
   ```

## First-time configuration

1. Open <http://127.0.0.1:8000/settings>. If the service is on another host,
   connect through your reverse proxy or an SSH tunnel; do not expose port 8000
   directly without authentication.
2. In **Settings → Source roots**, add `/data/sources/incoming` and enable it.
   Only existing, non-symlink immediate children of `/data/sources` are valid.
3. In **Settings → Storage**, confirm `/data/media` as the managed output
   directory. It must be writable and support fsync and atomic rename. Staging
   at `/appdata/music-ingest/staging` is disposable scratch space.
4. Configure matching and optional provider settings in **Settings**. Ambiguous,
   unavailable, unsafe, stale, and low-confidence matches remain available for
   review instead of being selected automatically.
5. Review work at <http://127.0.0.1:8000/review>. The service reconciles enabled
   source roots hourly by default. Set
   `MUSIC_INGEST_RECONCILIATION_INTERVAL_SECONDS` in the `music-ingest`
   environment to a positive number of seconds to change that interval, then run
   `docker compose up -d` to apply it.

To notify the service immediately after a downloader finishes writing files,
send a request from the same Docker network to
`http://music-ingest:8000/api/intake/notification`. The notification queues a
reconciliation; it does not accept or persist downloader-specific payloads.

## Data and operational boundaries

- `./sources` is read-only input. A changed source is observed as a new version;
  incoming media is never modified, moved, or deleted.
- `./media` is the managed published library. Publication is staged and verified
  with manifests and hashes before it supersedes existing managed media. `.nfo`
  files are never removed.
- `./appdata` contains disposable staging data. Do not use it for source media
  or the published library.
- The named `postgres-data` volume holds the database: settings, provenance,
  review decisions, failures, metadata, and publication state. Back it up before
  upgrades or host migrations.

Stop the stack without deleting its database or managed files:

```sh
docker compose down
```

Do not use `docker compose down --volumes` unless intentionally discarding all
PostgreSQL state.

## Upgrades

1. Back up the PostgreSQL volume and `./media`.
2. Change the image reference in `compose.yaml` to the selected immutable digest.
3. Run:

   ```sh
   docker compose pull
   docker compose up -d
   docker compose ps
   curl --fail --silent --show-error http://127.0.0.1:8000/healthz
   ```

Migrations run at service startup. Do not run two Music Ingest workers against
the same database and media root during an upgrade.

## Supported media and publication

Incoming FLAC, MP3, M4A (AAC or ALAC), Ogg Vorbis, and Opus are supported.
Published audio is Matroska (`.mka`) with the source audio stream copied without
re-encoding. Raw AAC and unsupported containers are not published.