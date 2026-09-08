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
