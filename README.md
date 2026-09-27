# MusicEnreachment

Desktop-oriented web application for managing a personal music library.

## Development

- Go 1.27 builds and tests the backend.
- Node.js 24 LTS and npm manage and build the Vite/TypeScript frontend.
- [Task](https://taskfile.dev/) is the cross-platform task runner for the
  monorepo. Run `task build`, `task test`, `task generate`, or `task verify`
  from the repository root.

## Generated files

Build output is always generated locally in `build/` and is never committed.
Task stages the frontend only transiently for Go embedding; those assets are
ignored as well. Commit source code, lockfiles, and generated API contracts
only — never build artifacts.
