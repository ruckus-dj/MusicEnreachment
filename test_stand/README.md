# Local Music Test Stand

This isolated Docker test stand does not contact or change the production server.

## Paths and access boundaries

| Host path | Purpose | Lidarr | music-ingest | Navidrome |
| --- | --- | --- | --- | --- |
| `data/downloads/` | Raw material placed by a download client | read/write at `/data/downloads` | unavailable | unavailable |
| `data/incoming/` | Lidarr-managed import library | read/write at `/data/incoming` | read-only at `/data/incoming` | unavailable |
| `data/media/` | Published canonical media library | unavailable | read/write at `/data/media` | media read-only at `/music` |

The PostgreSQL-backed API and its in-process worker receive only the incoming
source tree read-only, the final media tree read/write, and the disposable
`appdata/music-ingest/` workspace read/write. PostgreSQL stores tags, revisions,
provider evidence, review decisions, job failures, and publication metadata.
There are no provenance, quarantine, retention, or rollback files. The staging
workspace is consumed by atomic publication and is not a second media library.

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
   exposes the ready API at <http://localhost:8787/healthz>.
3. Open Lidarr at <http://localhost:8686> and Navidrome at
   <http://localhost:4533>.

### Colima host-port recovery

On macOS with Colima, recreating a container can leave the SSH port-forwarder
holding an old `8787` connection. If the container is healthy but
`curl http://localhost:8787/healthz` returns an empty reply, refresh Colima's
forwarder and retry:

```sh
colima restart
docker compose up --wait
curl --fail --silent --show-error http://localhost:8787/healthz
```

If the old SSH multiplex forward is still holding the port, use the current
Colima VM address directly until the forwarder is reset:

```sh
curl --fail --silent --show-error "http://$(colima status --json | jq -r .ip_address):8787/healthz"
```

## Test workflow

Verify the actual API and its migrated PostgreSQL runtime with:

```sh
curl --fail --silent --show-error http://localhost:8787/healthz
curl --fail --silent --show-error http://localhost:8787/review >/dev/null
docker compose exec postgres psql -U music_ingest -d music_ingest -tAc 'select version_num from alembic_version'
docker compose logs lidarr-webhook
```

Lidarr delivers webhooks through the internal Compose network to
`http://music-ingest:8000/api/intake/lidarr`; the app deliberately has no
authentication layer in this trusted local topology. `lidarr-webhook` is a
one-shot setup task, not a healthy long-running service: an exit code of zero
means Lidarr saved the endpoint and its `testall` validation reported success.

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
