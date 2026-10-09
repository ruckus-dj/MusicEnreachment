# Plan 10: real-tools Docker runtime acceptance

**Latest result: six fresh analysis runs and explicit artifact cleanup passed
against the exact-HEAD image.** New in-place and staged roots were created and
each scanned once. All six analysis pipelines freshly executed SHA-256, ffprobe,
and fpcalc; cleanup then removed only the three explicitly selected newly
staged artifacts. See “Fresh analysis rerun” below. Older analysis evidence
below belongs to prior runtimes and is not attributed to current HEAD.

## Fresh analysis rerun

- Distinct isolated Compose project `plan10-analysis-rerun-20261009`, revision `1fcc18eb2072d24b6c3998a783a3504bc125eb85`,
  image `sha256:3def46e987a0b927a8db812f53d6e60f2bfa6f1890c30e3c2e9204e923f210df`,
  label confirmed for that revision. Final rerun container ID
  `de5d4b172c0b07f9600f61f6b5103a8bf4469b190fbd2e6c6bdda5c99c50641a`.
- Created two new independent roots (IDs `ac022e36-ee9e-423e-b716-4d3d3660bc8d`
  in-place and `6551f16c-0b3b-4363-bd72-a15c5392b0aa` staged), each with three
  distinct audio inputs and a single successful generation-1 scan. Inputs were
  read-only copies from six distinct originals; originals were never written.
- Each of six analysis operations succeeded. The persisted step data records
  SHA-256, ffprobe, and fpcalc with `reuse_origin: executed` for every input in
  both modes (not a cache hit). See per-work detail JSONs
  `analysis-detail-inplace-*.json` / `analysis-detail-staged-*.json`,
  and `operations-analysis-final.json`. Analysis operations are terminal
  `succeeded` with stage `applying`. The six detail JSONs, independently read
  by the primary agent, show all 18 steps at attempt 1 with origin `executed`.
  No full user filenames are reproduced here. The dedicated rerun of previously
  analyzed content is also retained separately in the fingerprint-rerun section
   below; this fresh loop used new roots and distinct contents.
- Read-only integrity comparison against `analysis-invariants-before.json`
  confirmed all six originals and their six analysis copies still existed with
   matching SHA-256, byte size, nanosecond mtime and mode (12/12 comparisons,
   zero mismatches). Evidence: `analysis-invariants-after.json` and
   `analysis-invariants-comparison.json`. Copy mode
  was read-only (`0444`). This comparison is scoped to this loop and does not
  claim integrity for earlier runs.
- Cleanup candidate count was nine before the request. Explicit cleanup
  operation `c426d853-ebd2-470e-8146-285b7d0db6b9` succeeded and its API detail
  reported success for the three selected new staged artifacts. The candidate
  list fell to six: all three selected IDs are absent and all six older
  candidates remain registered/present. Evidence: `cleanup-analysis-*`,
  `fresh-artifact-filesystem-after.json`,
  `cleanup-analysis-candidates-before.json`, and
  `cleanup-analysis-candidates-after.json`. No baseline SHA-256 inventory for
  those six older candidate files was captured in this rerun, so their physical
  continued presence is not a hash-based invariance claim.
- No fresh SQL snapshot or database dump was retained for this loop. Its
  evidence is the API JSON and subsequent read-only filesystem comparisons.
  `fresh-compose-state-after.txt` records no remaining containers for
  `plan10-analysis-rerun-20261009`. Temporary evidence and source copies remain.

**Attribution correction (2026-10-09):** this section initially repeated the
historical roots `caf81a80…` / `3e580b82…` and incorrectly cited older SQL/dump
files. Those files (`analysis-execution-step-sql.txt`,
`artifact-registry-final.txt`, `cleanup-items-final-sql.txt`,
`final-database-after-fingerprint-reruns.sql`) predate this fresh loop and are
historical evidence only. Fresh scan operations are
`2f6f48c4-a964-4a00-ae40-0dd96a8db98a` and
`1c03c1a8-d380-48c7-bc99-9b1e6354b399`. Later integrity and filesystem captures
close the initially missing after-manifest; they do not create a fresh DB dump.

## Exact-HEAD cleanup rerun

- **Code revision:** `1fcc18eb2072d24b6c3998a783a3504bc125eb85`.
- **Date/runtime:** 2026-10-09, isolated Compose project
  `plan10-runtime-rerun-20261009`, Linux/arm64.
- **Image:** `plan10-runtime:1fcc18e-final`, image ID
  `sha256:3def46e987a0b927a8db812f53d6e60f2bfa6f1890c30e3c2e9204e923f210df`.
  Built from `deploy/docker/Dockerfile` after the source revision was confirmed;
  the image label `org.opencontainers.image.revision` is the full revision above.
  The Docker build log records the frontend and backend build stages.
- **Container:** `plan10-runtime-rerun-20261009-app-1`, container ID
  `15eba38a092c5deb486c49be7f1ec331ab5cd25f3b43c5fcae42f225208315ba`, created
  after the build and using that exact image ID. The final Compose file pins this
  tag with `pull_policy: never` and has no app build directive.
- A fresh isolated PostgreSQL data directory was initialized and restored from
  the retained `final-database.sql` dump captured by the preceding runtime before
  starting the app. Existing
  staged output files, managed-tool directory, and read-only source mounts in
  the isolated evidence directory were reused. App startup applied migrations.

### Explicit cleanup result

- The retained database contained earlier cleanup jobs 21 and 22 as `discarded`
  with `operation delivery is no longer current`. They were not edited, replayed,
  or retried. Their operation snapshots remain historical records.
- The candidate API listed six eligible staged artifacts. A new explicit
  `POST /api/source-analysis/artifacts/cleanup` selected only
  `5990b84d-bd8c-4fb6-bc68-92e3360edb20`,
  `981a59b0-0431-43fd-91cb-40a630d319dd`, and
  `bd81d57e-cbfb-4e4d-ba74-d6bac24508da`. It created operation
  `ae100151-7bde-4d5e-8ea9-1d473fa4a01f`, associated with River job 24.
- The operation reached `succeeded` / `finished` on its first attempt. Its
  operation-detail API response and durable cleanup-item rows report `succeeded`
  for all three selected artifacts. The selected files are physically absent
  and no longer registered as artifacts. The three unselected artifacts remain
  present with unchanged SHA-256 values; the candidate API now lists those three
  remaining artifacts. No manual SQL mutation was used (apart from restoring the
  retained database dump); SQL was read-only evidence collection.
- This confirms the explicit cleanup delivery, terminal snapshot/outcomes,
  registry removal, physical deletion, and unselected-file preservation against
  the exact-HEAD image.

### Exact-image fingerprint reruns (earlier exact-image phase)

- The exact image was restarted from the retained isolated PostgreSQL restore;
  its image ID remained
  `sha256:3def46e987a0b927a8db812f53d6e60f2bfa6f1890c30e3c2e9204e923f210df`
  and revision label `1fcc18eb2072d24b6c3998a783a3504bc125eb85`. The two already
  registered read-only source roots remained distinct: in-place
  `caf81a80-ea38-477b-adf1-203d67deb9cf` and staged
  `3e580b82-dcac-42cb-be6b-3e0f82a8bcc0`.
- Before and after the six API reruns, SHA-256, byte size, and nanosecond mtime
  were captured for the five originals in `test_stand` and all six in-place /
  staged source copies. All 11 comparisons matched. The analysis roots used the
  first three approved tracks; the other two originals were only included in
  the integrity comparison and were not scanned.
- The explicit `POST .../fingerprint/rerun` API was submitted for all three
  source locations in each root. All six operations succeeded, incremented the
  fingerprint step to attempt 2, and returned `reuse_origin: executed` with
  `applied_operation_id` matching the new operation. fpcalc `1.6.1` returned
  durations 193.40, 197.47, and 173.36 seconds and non-empty fingerprints of
  3,434, 3,471, and 3,594 characters in each mode. Operation IDs, requests,
  terminal snapshots, and post-rerun details are retained in the evidence
  directory; see `exact-image-fingerprint-reruns-summary.json`.
- The two roots contain the same content digests, so the shared canonical
  fingerprint rows have separate stable row IDs and `winning_result_id` values;
  neither is the per-work execution identity. Per-work details retain each
  location's own successful rerun operation as `applied_operation_id`. After
  the staged reruns completed last, the canonical rows' winning
  `applied_operation_id` values are the staged operation IDs for each digest.
  The read-only dump extraction in
  `exact-image-fingerprint-canonical-provenance.txt` records these rows; it
  avoids mistaking a stable canonical row ID for the execution that currently
  wins provenance.
- At this phase, this did **not** establish a fresh exact-image SHA-256 or ffprobe execution.
  In the restored rows, SHA-256 provenance predates this rerun and probe steps
  have `reuse_origin: sha256`; both came from earlier runs. The retry API only
  accepts a failed step, while these probe steps are already succeeded, and the
  fingerprint rerun endpoint reruns only fpcalc. No database cache/canonical
  result was deleted or modified to force execution. Consequently the requested
  fresh SHA-256 + ffprobe + fpcalc execution in both modes was still outstanding
  at that phase (the later fresh analysis rerun above closes this gap);
  do not treat this as full Plan 10 real-analysis acceptance.
- The two roots and their generation-1 scan inventories were restored from the
  earlier runtime; this restart did not create new roots or run a new scan.
  The later rerun below captured the originals/copies baseline, created distinct
  read-only in-place and staged roots, and scanned each once on the exact image.
  That established fresh SHA-256 execution. A genuinely fresh ffprobe run
   at that phase still required a cache-miss path: the existing retry API rejects
  these already-succeeded probe steps, and changing input bytes, tool version,
   or canonical cache state was not approved/performed here. The later fresh
   loop used six distinct previously unanalyzed inputs and closed this gap.
- A post-rerun database dump, final API detail responses, app log, exact image
  container identity, and the before/after integrity manifests are retained in
  the evidence directory. Only this isolated Compose project was stopped after
  capture; the database directory and evidence remain available.

### Integrity, evidence, and teardown

- The three originals under `test_stand` had matching SHA-256, byte size, and
  nanosecond mtime in the retained baseline and the post-cleanup comparison.
  **History limitation:** no pre-analysis original-source metadata baseline was
  retained, so this comparison proves integrity for this cleanup rerun only; it
  does not retroactively cover earlier analysis runs.
- Build/image/container identity, app logs, restored DB dump, request/operation
  JSON, final candidate list, SQL cleanup outcomes, staged-file status, and
  source comparison are retained in
  `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/plan10-runtime-rerun-20261009`.
  Key files include `rebuild-final-1fcc18e.log`, `image-final-identity.txt`,
  `container-final-identity.txt`, `container-final-inspect.json`,
  `image-final-inspect.json`, `restore-final-db.log`,
   `cleanup-final-operation-detail.json`, `cleanup-items-final-sql.txt`,
   `cleanup-candidates-final-image.json`, `staged-files-final-status.json`,
   `source-originals-after-final.json`, and
   `final-database-after-fingerprint-reruns.sql`. Fingerprint rerun evidence is
   retained alongside these as `exact-image-fingerprint-reruns-summary.json`,
   `exact-image-source-and-copies-before.json`,
   `exact-image-source-and-copies-after.json`, and
   `exact-image-source-and-copies-comparison.json`, plus
   `exact-image-fingerprint-canonical-provenance.txt`. Fresh analysis rerun
   evidence is in `analysis-invariants-before.json`,
   `sources-before-analysis.json`, `operations-analysis-final.json`, six
   `analysis-detail-*.json` files, `analysis-execution-step-sql.txt`,
   `cleanup-analysis-operation-detail.json`,
   `cleanup-analysis-candidates-before.json`, and
   `cleanup-analysis-candidates-after.json`.
- At that stage only this run's Compose project was stopped; subsequent rerun
  teardown is described above. No other containers or user files were stopped
  or removed. Evidence and temporary data remain available.
- No production-source changes, dependency changes, local tests, lint, or
  `task verify` were performed; this was a focused runtime acceptance rerun.

## Earlier runtime evidence and correction

An earlier report attributed source-analysis and cleanup observations to a
“corrected-HEAD” runtime using image
`sha256:9915da343ece7678f0af60349bc1eaaa7a61264acd9c8e1f508fc4dbd4d57d6b`.
That image was created at approximately 11:09, before the current exact-HEAD
image build at approximately 11:36; it is not proof of the binary now tested.
The earlier analysis observations remain historical but are not accepted as
verification of revision `1fcc18e`. Its cleanup operations 21 and 22 failed and
remain untouched. The exact-HEAD cleanup result above is a successor runtime,
not a replay of either failed operation.

The observations made in that earlier runtime were:

- A first app-mediated MusicBrainz setup check timed out, but a later repeat
  succeeded. The Linux/arm64 catalog returned FFmpeg `9.0` from `btbn` and
  fpcalc `v1.6.1` from `chromaprint`, both with published checksums; app-mediated
  preflight, download, and verification succeeded. Managed
  versions reported ffmpeg/ffprobe `n9.0.2-23-g27b46f0fbc-20261008` and fpcalc
  `1.6.1`.
- That runtime recorded fresh in-place and staged scans as successful, earlier
  staged jobs 6–8 with SHA-256, ffprobe, and fpcalc executed, and fresh rerun
  jobs 15–20 using the SHA-keyed probe/fingerprint reuse path. Those records
  remain evidence of the historical runtime only, not fresh analysis on the
  exact-HEAD image above.
- A new cleanup request there created operation
  `ebc2e100-0500-43f9-844d-c49958f2a273` / job 22. Like earlier operation
  `d621722d-861c-421d-b7d1-ca73e84dc51e` / job 21, the River job was discarded
  with `operation delivery is no longer current`; the selected three staged
  artifacts remained present then. Neither failed job was replayed in this
  exact-HEAD run. Its preserved full DB dump was restored to the fresh isolated
  PostgreSQL directory used above.
- At that time, all three unselected artifacts were also present and hash-
  unchanged. The source manifest had no baseline from before the earlier full
  analysis loop, so its before/after hash, size, and mtime comparison did not
  retroactively cover that loop. See the evidence directory for the historical
  request/response, SQL, logs, tool versions, and analysis metadata.

The still earlier initial attempt used revision
`9d4b1ca4c9dfe09c0295a04e5b12c1f44926a25f` in isolated Compose project
`plan10-runtime-20261009`; its MusicBrainz check timed out on three attempts, so
Setup-dependent analysis and cleanup were not run. That dated result is retained
as historical context, not rewritten as a result for current HEAD.

For that initial attempt, the real MusicBrainz API check failed three times
with the retry-safe timeout response, while direct public endpoint probes from
the host and app network namespace returned HTTP 200. Setup therefore remained
incomplete; no source roots, scans, analysis work, or cleanup were created in
that attempt.
