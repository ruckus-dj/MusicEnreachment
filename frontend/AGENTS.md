# Frontend

- `src/ui/` contains the UI; `src/providers/` connects it to application data and actions.
- Regenerate `src/api/generated/` with `task generate` after API changes. Use Biome for lint and formatting.
- Run `task check:unused` from the repository root for report-only Knip analysis of production files, exports, and types. It is not part of `task verify`.
- Operation SSE is a wake-up signal: read the current snapshot through the API.
