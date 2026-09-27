# MusicEnreachment

Desktop-oriented web application for managing a personal music library.

## Development

```sh
bun install --cwd frontend
bun run --cwd frontend build
cd backend && go build ./cmd/server
```

The application and its PostgreSQL dependency can be started with Docker Compose
after the runtime foundation is configured.

## Generated files

Build output is always generated locally in `build/` and is never committed.
`scripts/build-frontend.sh` stages that output for Go embedding; the staged
assets are ignored as well. Commit source code, lockfiles, and generated API
contracts only — never build artifacts.
