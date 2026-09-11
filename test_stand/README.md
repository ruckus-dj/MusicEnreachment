# Local Music Test Stand

This isolated Docker test stand does not contact or change the production server.

## Paths and access boundaries

| Host path | Purpose | Lidarr | music-ingest | Navidrome |
| --- | --- | --- | --- | --- |
| `data/downloads/` | Raw material placed by a download client | read/write at `/data/downloads` | unavailable | unavailable |
| `data/sources/legacy/` | Lidarr-managed legacy import library | read/write at `/data/sources/legacy` | read-only at `/data/sources/legacy` | unavailable |
| `data/media/` | Published canonical media library | unavailable | read/write at `/data/media` | media read-only at `/music` |

The PostgreSQL-backed API and its in-process worker receive only the incoming
source tree read-only, the final media tree read/write, and the disposable
`appdata/music-ingest/` workspace read/write. PostgreSQL stores tags, revisions,
provider evidence, review decisions, job failures, and publication metadata.
Processing scratch lives in `appdata/music-ingest/staging/` and is disposable.
Durable publication manifests and backups live in `.music-ingest-publications/`
next to each destination until database finalization and cleanup complete. Keep
these directories with media backups; never clean them as processing scratch.

## Start

1. From this directory run:

   ```sh
   docker compose up --build --wait
   ```

The Compose network has IPv6 enabled so the application can use MusicBrainz's
IPv6 endpoint when the Docker host has a working IPv6 route. If the stand was
already running, recreate its network after this change:

```sh
docker compose down
docker compose up --build --wait
```

2. Compose starts PostgreSQL, runs Alembic during API startup, starts the
   in-process worker, configures and tests Lidarr's `music-ingest` webhook, and
   exposes the ready API at <http://127.0.0.1:8787/healthz>.
3. Open Lidarr at <http://127.0.0.1:8686>, Navidrome at
   <http://127.0.0.1:4533>, and Feishin at <http://127.0.0.1:9180>.

### Checking synchronized lyrics

Navidrome's built-in web player does not render external `.lrc` sidecars. Use
Feishin at <http://127.0.0.1:9180> to check the synchronized lyrics that
Navidrome exposes through the OpenSubsonic API. The client is preconfigured for
this test stand's browser-facing Navidrome URL (`http://127.0.0.1:4533`); sign
in with the same Navidrome credentials. No credentials are stored in Compose or
this repository.

### Colima host-port recovery

On macOS with Colima, recreating a container can leave the SSH port-forwarder
holding an old `8787` connection. If the container is healthy but
`curl http://127.0.0.1:8787/healthz` returns an empty reply, refresh Colima's
forwarder and retry:

```sh
colima restart
docker compose up --wait
curl --fail --silent --show-error http://127.0.0.1:8787/healthz
```

If the old SSH multiplex forward is still holding the port, use the current
Colima VM address directly until the forwarder is reset:

```sh
curl --fail --silent --show-error "http://$(colima status --json | jq -r .ip_address):8787/healthz"
```

## Test workflow

Verify the actual API and its migrated PostgreSQL runtime with:

```sh
curl --fail --silent --show-error http://127.0.0.1:8787/healthz
curl --fail --silent --show-error http://127.0.0.1:8787/api/settings/source-roots
docker compose exec postgres psql -U music_ingest -d music_ingest -tAc 'select version_num from alembic_version'
docker compose logs lidarr-webhook
```

The local UI and API are public; production authentication is owned by the reverse
proxy. `lidarr-webhook` is a one-shot setup task, not a healthy long-running
service: an exit code of zero means Lidarr saved the endpoint and its `testall`
validation reported success.

## Stable library flow

Use the incoming directory or a provider webhook to create immutable source
observations. The worker creates managed publication versions and exposes the
stable record, source history, publication history, metadata revisions, and
processing events through `/api/library/records`.

The source file remains unchanged throughout intake and publication. A source
replacement is attached to the existing stable record and makes the current
publication stale instead of creating a second library identity.

## Stop and reset

```sh
docker compose down
```

This preserves the PostgreSQL named volume. Use `docker compose down --volumes`
only when an explicit local database reset is intended.

Any temporary fixture should own its generated incoming and published paths,
remove them, and run
`docker compose down --volumes --remove-orphans`. It does not clear any other
bind-mounted fixture or configuration path.

### Reset for a first library scan

To repeat an initial import while retaining the configured source roots and the
Lidarr/Navidrome settings, run:

```sh
./scripts/reset-library.sh
```

The script stops only `music-ingest`, clears its imported database tables, resets
the saved source roots to `never_scanned`, and clears only the active `output_root`
from Music Ingest's `storage_config`. It does not modify `data/incoming/`,
`data/sources/`, `data/downloads/`, `appdata/`, `config/`, or `.env`; it does not
stop or restart Lidarr or Navidrome. It then starts only `music-ingest` and checks
its API from inside the service. After it completes, open the UI and select
**«Сканировать новые и изменённые»** to process the source library as a first import.
