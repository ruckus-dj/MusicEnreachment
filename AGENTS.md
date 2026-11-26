# MeloTrove

- Product requirements: [docs/design/decisions.md](docs/design/decisions.md). Change approved behavior only on the owner's request.
- `backend/` is the Go server; `frontend/` is the application; [`docs/app-design-react/`](docs/app-design-react/) is the standalone UI prototype.
- Build and setup instructions are in [README.md](README.md). `task verify` checks the application; `task design:check` checks the prototype.
- Keep source files and stand data intact. Keep the stand's `downloads` mount read-only.
