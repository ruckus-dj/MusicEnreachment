# frontend

**Score: 11** (distinct domain: 10 config files + features, module boundary, code-heavy)

## OVERVIEW
React 19 + Vite + Tailwind 4 + TanStack Query + react-aria-components. Orval-generated API client from OpenAPI. The hash-routed UI includes first-run setup, runtime settings, source inventory/analysis, and incoming grouping and matching workflows.

## STRUCTURE
```
frontend/
├── src/
│   ├── main.tsx           # React 19 createRoot entry
│   ├── routes/
│   │   └── AppShell.tsx   # Hash router, useSyncExternalStore
│   ├── features/
│   │   ├── setup/         # First-run setup flow
│   │   ├── settings/      # Runtime settings and managed tools
│   │   ├── sources/       # Source roots, inventory, and analysis
│   │   ├── incoming/      # Incoming grouping and matching review
│   │   └── ...            # Other product features
│   ├── components/        # Shared UI components
│   └── api/
│       ├── client/        # Operation event-stream helpers
│       └── generated/     # Orval output (DO NOT EDIT)
├── openapi.json           # Exported from backend/cmd/openapi
├── orval.config.ts        # Orval codegen config
├── biome.json             # Biome lint/format (no ESLint/Prettier)
├── vite.config.ts         # Dev proxy: /api and /health -> 127.0.0.1:8080
└── vitest.config.ts       # Vitest + React Testing Library + MSW
```

## WHERE TO LOOK
- src/features/setup/ - first-run wizard, including tool catalog, preflight, and download flow
- src/features/settings/ - runtime settings and managed tools
- src/features/sources/ - source roots, inventory, and analysis
- src/features/incoming/ - incoming grouping, matching review, and manual corrections
- src/routes/AppShell.tsx - hash routing, screen wiring
- src/api/generated/client.ts - Orval-generated hooks/types (NEVER hand-edit)
- src/api/client/operations.ts - operation event-stream subscription helper

## CONVENTIONS
**Generated API client**: Orval generates the API client and schemas in src/api/generated/ from openapi.json. Run `task generate` to sync; tools/check-generated.test.mjs enforces drift detection. Test suites use handwritten MSW handlers, with shared setup in src/test/server.ts.

**Biome only**: No ESLint, no Prettier. Biome handles lint and format.

**Tests colocated**: *.test.tsx next to components. Vitest + React Testing Library + MSW.

**Dev proxy**: Vite proxies /api and /health to 127.0.0.1:8080 for same-origin during dev.

**Build output**: Vite builds to ../build/frontend; tools/stage-frontend.mjs stages it; backend embeds it via internal/static.

**Hash routing**: Uses hash router (#/setup, #/settings) for embedded static serving without backend routing.

## ANTI-PATTERNS
- Hand-editing src/api/generated/** (always regenerate via `task generate`)
- Adding ESLint or Prettier (Biome is the toolchain)
- Direct fetch to /api without using generated client (except where Orval client doesn't fit)
- Committing build/ output
