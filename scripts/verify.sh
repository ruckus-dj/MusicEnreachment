#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

test -z "$(gofmt -l "$root/backend")"
(cd "$root/backend" && golangci-lint run ./... && go test ./... && go build ./cmd/server)
bun run --cwd "$root/frontend" lint
bun run --cwd "$root/frontend" test
"$root/scripts/generate-api.sh"
git -C "$root" diff --exit-code -- frontend/openapi.json frontend/src/api/generated
bun run --cwd "$root/frontend" build
