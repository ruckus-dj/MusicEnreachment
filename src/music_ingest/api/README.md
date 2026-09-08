# API composition

`create_app` is the composition root: it configures the lifespan, static assets,
application flags and domain routers. Its public arguments remain the entry point
for the server and tests.

Each `routers` module exposes `create_router` with only its required dependencies.
The factory binds those dependencies to its own endpoints; there is no shared
global session, provider or application configuration. Sessions and commits remain
owned by the handlers, preserving existing transaction boundaries.

- `catalog`: library reads, manual-action lists, record details and artwork.
- `matching`: candidate inspection/selection and recording/source association.
- `metadata`: source attachment, record identity and final metadata edits.
- `recovery`: retries, reprocessing and destination-conflict actions.
- `settings`: runtime settings, source roots and managed storage configuration.
- `genres`: genre catalog retrieval and synchronization.
- `intake`, `reconciliation`, `workers`: ingestion and background-work endpoints.
- `pages`, `e2e`: UI/health routes and the explicitly gated fixture endpoint.

`catalog_views` and `candidate_views` own response presentation helpers.
`library_access` holds shared source-boundary and recovery helpers used by library
routes. These helpers never commit a transaction.

This decomposition preserves URLs, endpoint names, OpenAPI schemas, status codes,
and transaction behavior. Moving filesystem operations/business rules out of the
HTTP layer and splitting DTOs are separate changes, not part of router extraction.
