# Publication and storage recovery

## A01 — durable publication checkpoints

`ProcessingWorker.run_once` owns transaction checkpoints. Handlers only prepare a
validated output inside their savepoint. The worker commits the job and the
`prepared` publication journal before exposing any bytes. Recovery commits exposure,
then verifies the manifest and output hash and commits the new current publication.
Only confirmed finalization permits backup cleanup. Commit errors propagate to the
runtime; a new worker session resumes from the durable journal even with an empty queue.

Filesystem writers and recovery use the same PostgreSQL transaction advisory lock.
It is reacquired after each commit, and destination ownership is checked again before
exposure. Recovery scans at most 100 pending journals and marks completed cleanup;
history remains available. This intentionally serializes worker filesystem operations.

Migration `20260908_0020` adds the prepared state and cleanup timestamp. Stop old
workers before upgrading: mixing workers with the old transaction contract is unsupported.
Do not discard active publication staging or backups. Before filesystem/DB backup,
stop writers and take a consistent snapshot of the database and publication workspace.

Validation: `tests/test_integrity_postgres.py` uses the real worker and migrated
PostgreSQL, injects failed commits at prepared/exposed/finalized, interrupts after
prepared commit and both rename boundaries, and fails cleanup. A new session must
recover matching target/current-publication hashes while retaining source and `.nfo` bytes.
Run explicitly with `MUSIC_INGEST_ENABLE_LIVE_TESTS=1 uv run pytest -q tests/test_integrity_postgres.py`.

## A02 — resumable output relocation

The storage PUT endpoint commits a request and returns the existing root with
`state=migrating`. Settings polls until completion. The worker processes one file
per committed step, recording its SHA-256, cursor, source, destination and last error
in `storage_config.migration_json` (migration `20260909_0021`). A repeated request for
the same destination returns the same job; a different request is rejected.

Copying always uses a temporary file on the destination filesystem, verifies its hash,
fsyncs and renames it. The old root and files stay intact throughout copying. Once all
copies verify, one database transaction updates publication/artwork paths and root.
Only then does cleanup remove old files owned by publication/artwork records. `.nfo`
and unmanaged originals remain in the old root; empty directories may remain too.
Migration remains `migrating` until cleanup finishes. On error, inspect the worker
monitor/logs, fix the underlying disk or permissions issue and let the worker retry;
do not clear the journal or manually set `ready`. A stopped process resumes automatically.

Publication, recovery, migration steps and source-root overlap validation share the
same lock. While migrating, workers perform relocation steps instead of new jobs.
The new root must be empty, disjoint from the old root and source roots, and contain
no symbolic links. Keep enough destination space for the complete copied tree.

Validation includes partial second-file copies, ENOSPC, switch and cleanup commit
failures, fresh-session restart, repeated requests, a real publication racing a move,
and two PostgreSQL workers resuming the same manifest.


## A03 — separate processing and publication filesystems

Processing scratch uses `MUSIC_INGEST_STAGING_ROOT`. After media validation, the
handler copies output to `<target-parent>/.music-ingest-publications/<attempt>/staged/`,
verifies its SHA-256, fsyncs the file and directory ancestry, and only then commits
`prepared`. Backups use the same attempt's `backup/` directory. Both final renames
therefore stay on the target mount, including targets on nested media mounts.
Startup probes writable media, file/directory fsync and rename before readiness.

The production SSD/pool split is supported. Test stand processing staging is also
outside media. Upgraded attempts retain their original paths until recovery, so do
not delete legacy active staging during rollout. New publication recovery does not
need processing scratch: the PostgreSQL crash tests delete it before restarting.

The separate-mount test runs the actual worker twice (create and replace the same
path) inside Linux with independently mounted processing/media tmpfs and migrated
PostgreSQL. It verifies unequal device IDs, current hashes, history, source immutability
and `.nfo` retention. Build the runtime image with
`docker build -t music-enrichment-test-stand-music-ingest:latest .` first, or set
`MUSIC_INGEST_INTEGRITY_IMAGE` to a compatible runtime image; repository code and
migrations are mounted read-only into the test runner.

Initial ingest and reprocessing use the same durable publication journal as final
metadata publication. Migration `20260909_0022` preserves the intended post-publication
review/provider state across restart. Cleanup failures remain pending for retry;
cleanup removes only known attempt files and preserves any `.nfo` sidecars.

Manual destination cleanup/replacement APIs also acquire the shared lock, use the
persisted output root and reject changes during migration or pending publication
recovery. Their cleanup preserves `.nfo`. Resuming an already copied file fsyncs its
content and directory ancestry before recording progress, including a process that
stopped immediately after rename.
