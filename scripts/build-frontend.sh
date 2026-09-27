#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
bun run --cwd "$root/frontend" build
rm -rf "$root/backend/internal/static/dist"
cp -R "$root/frontend/dist" "$root/backend/internal/static/dist"
