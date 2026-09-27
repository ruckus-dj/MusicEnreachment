#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
(cd "$root/backend" && go run ./cmd/openapi > "$root/frontend/openapi.json")
bun run --cwd "$root/frontend" generate
