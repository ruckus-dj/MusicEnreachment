# Frontend

- `src/ui/` contains the UI; `src/providers/` connects it to application data and actions.
- Regenerate `src/api/generated/` with `task generate` after API changes. Use Biome for lint and formatting.
- Operation SSE is a wake-up signal: read the current snapshot through the API.
