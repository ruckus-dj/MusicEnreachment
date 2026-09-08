# Processing boundaries

`ProcessingWorker` claims jobs, selects a handler, owns the processing savepoint,
classifies failures, finalizes attempts, and cleans disposable job staging. The
caller owns the outer transaction: `run_once` does not commit it.

## Responsibilities

- `config.py`: injected configuration, paths and optional provider dependencies.
- `execution.py`: handler protocol, per-attempt context and explicit outcomes.
- `handlers/`: initial ingest, provider analysis, selection, final publication,
  artwork and reconciliation. Handlers orchestrate their domain operations; they
  do not commit transactions or finalize job attempts.
- `support/sources.py`: source loading, refreshed row locks and source boundaries.
- `support/evidence.py`: observations, fingerprints and decoder evidence.
- `support/settings.py`: an immutable scalar-settings snapshot per attempt and
  provider configuration derived from it.
- `support/staging.py`: disposable job directories and unsorted output naming.
- `support/outcomes.py`: retry, quarantine and changed-source transitions.
- `candidates.py`: candidate, scoring and metadata transformation rules.

Services and the handler registry are rebound before each claim so a reused
worker observes configuration changes between jobs. Scalar settings are frozen
before the claim callback runs and remain consistent within that attempt.

## Transaction and filesystem contract

A handler returns `None` for normal completion, or `QuarantineSource` /
`ChangedSource` for an explicit rejection. The worker applies those outcomes
inside the savepoint, preserving collected evidence. An exception rolls back
the savepoint before failure classification and retry/quarantine recording.
Only still-running attempts are marked successful during finalization.

Selection refresh invokes final publication synchronously using the same attempt
and propagates its outcome. It does not create a second independently completed
attempt or record refresh success after rejection.

Incoming media remains immutable. Job staging is disposable and cleaned by the
worker; durable publication-attempt staging and atomic managed-media replacement
belong to the `publication` package. Publication still validates output and
preserves `.nfo` files. This decomposition changes no database schema, persisted
job kinds or API contracts.
