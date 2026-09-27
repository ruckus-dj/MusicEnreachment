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
