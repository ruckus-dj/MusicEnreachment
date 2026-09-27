#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$root/build/backend"
(cd "$root/backend" && go build -o "$root/build/backend/music-enreachment" ./cmd/server)
