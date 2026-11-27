# MeloTrove

- Product requirements: [docs/design/decisions.md](docs/design/decisions.md). Change approved behavior only on the owner's request.
- `backend/` is the Go server; `frontend/` is the application; [`docs/app-design-react/`](docs/app-design-react/) is the standalone UI prototype.
- Build and setup instructions are in [README.md](README.md). `task verify` checks the application; `task design:check` checks the prototype.
- The prototype may lead the application while exploring new designs and UX. Transfer its full `src/ui/` tree to the application only when the owner requests it. In the same change, backport any frontend UI changes that did not come from the prototype, including additions, renames, and deletions, so the next transfer does not lose them. Merge those changes into the prototype without overwriting its newer work. Keep host-specific providers, fixtures, and entry points outside `src/ui/`.
- Never delete `test_stand/data/downloads` or its contents. Keep `/downloads` mounted read-only. All other test stand data may be deleted on reset.
