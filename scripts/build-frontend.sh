#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
bun run --cwd "$root/frontend" build
static="$root/backend/internal/static/dist"
find "$static" -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
cp -R "$root/build/frontend/." "$static"
