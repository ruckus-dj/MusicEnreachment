# MusicEnreachment

Desktop-oriented web application for managing a personal music library.

## Development

The cross-platform frontend toolchain and unified build command are selected
before implementation. No JavaScript runtime, package manager, shell build
scripts, Docker build recipe, or CI build command is currently prescribed.

## Generated files

Build output is always generated locally in `build/` and is never committed.
The staging assets for Go embedding are ignored as well. Commit source code,
lockfiles, and generated API contracts only — never build artifacts.
