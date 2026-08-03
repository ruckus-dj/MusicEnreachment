# Local Music Test Stand

This isolated Docker test stand does not contact or change the production server.

## Paths and access boundaries

| Host path | Purpose | Lidarr | music-ingest | Navidrome |
| --- | --- | --- | --- | --- |
| `data/downloads/` | Raw material placed by a download client | read/write at `/data/downloads` | unavailable | unavailable |
| `data/incoming/` | Lidarr-managed import library | read/write at `/data/incoming` | read-only at `/data/incoming` | unavailable |
| `data/.publish/` | Shared atomic staging, media, and state filesystem | unavailable | read/write at `/data/.publish` | media read-only at `/music` |

The PostgreSQL-backed API and its in-process worker receive a read-only `/data`
view. One explicit writable mount exposes the `.publish` tree; its staging,
media, retention, quarantine, and provenance subdirectories share one filesystem
so publication can use atomic rename. `/data/downloads` and `/data/incoming`
remain read-only.
Lidarr and Navidrome retain their read-only media boundary.

## Start

1. From this directory run:

   ```sh
   docker compose up --build --wait
   ```

2. Compose starts PostgreSQL, runs Alembic during API startup, starts the
   in-process worker, configures and tests Lidarr's `music-ingest` webhook, and
   exposes the ready API at <http://localhost:8787/healthz>.
3. Open Lidarr at <http://localhost:8686> and Navidrome at
   <http://localhost:4533>.

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

## Task-7 webhook E2E

Run the deterministic E2E from the repository root:

```sh
uv run python -m tests.integration.task7_e2e
```

The command removes any prior local stand, starts a fresh Compose stack, creates
a tagged FLAC fixture below `data/incoming/`, and posts the same real-shaped
payloads Lidarr uses to the live intake API. It verifies `Test` creates only a
receipt, a byte-identical `Download` replay reuses one receipt and job, and
`Rename` plus `AlbumDelete` are durable webhook work. The worker must publish
the automatic original-tag fallback, mark it `needs_review`, and create a
published release. The runner then checks the release-review queue, edits final
tags from revision 1 to 2, republishes revision 2, scans Navidrome, and verifies
the fixture source inode and SHA-256 did not change.

The stand's real proxy boundary remains Lidarr's internal URL
`http://music-ingest:8000/api/intake/lidarr`. The runner posts fixtures to the
host-mapped API (`http://localhost:8787`) only to deterministically cover all
event variants, including replay; it does not add API authentication or make
public-provider calls. A successful run writes only
`.omo/evidence/music-ingest-platform/task-7-e2e.json`, after its assertions and
cleanup complete. Failed runs leave no task-7 evidence artifact.

## Stop and reset

```sh
docker compose down
```

This preserves the PostgreSQL named volume. Use `docker compose down --volumes`
only when an explicit local database reset is intended.

The task-7 command owns its generated `data/incoming/task-7-*` fixture and its
published/staging/retention sibling paths, removes them, and runs
`docker compose down --volumes --remove-orphans`. It does not clear any other
bind-mounted fixture or configuration path.
