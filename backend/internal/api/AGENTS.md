# backend/internal/api

**Score: 13** (distinct domain: 6 files, module boundary, code-heavy)

## OVERVIEW
HTTP API layer: Huma v2 on chi router via humachi adapter. Health endpoints, middleware, and domain endpoints (RegisterSetup).

## WHERE TO LOOK
- api.go - Huma/chi setup, calls RegisterSetup
- setup.go - RegisterSetup: binds SetupService to /api/setup/* endpoints
- health.go - /health/live and /health/ready
- middleware.go - CORS forbidden, same-origin only
- middleware_test.go, health_test.go - unit tests

## CONVENTIONS
**Huma v2 on chi**: Uses humachi adapter, OpenAPI schema generated from Huma operations.

**No CORS**: Same-origin design; CORS headers forbidden. Vite dev proxies /api and /health to 127.0.0.1:8080.

**No auth**: No users, sessions, tokens, roles. Deployment-boundary auth only (e.g., Caddy + Authentik).

**Generated OpenAPI contract**: backend/cmd/openapi exports schema -> frontend/openapi.json -> Orval generates TS client. `task generate` enforces sync.

**Error responses**: Huma standard error format, service layer provides safe error messages.

**Health endpoints**: /health/live (liveness, always 200 if running) and /health/ready (readiness, checks DB connection).

## STRUCTURE
api.go wires chi router with Huma, calls domain-specific Register* functions (RegisterSetup in setup.go). middleware.go is currently minimal (no CORS, no auth, no rate limit by design).

## ANTI-PATTERNS
- CORS headers (explicitly forbidden, same-origin design)
- Auth middleware (no in-app auth by design)
- Trusted-proxy headers (no deployment switches in runtime logic)
- Rate limiting (not implemented, deployment-boundary concern)
- Leaking Bun or persistence types into Huma DTOs (only service layer types allowed)
- Bypassing Huma validation (all input validation via Huma schemas)
