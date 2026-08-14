#!/usr/bin/env bash
# Reset only Music Ingest products while preserving every other test-stand resource.
set -euo pipefail

stand_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$stand_directory"

docker compose stop music-ingest

output_root="$(docker compose exec -T postgres psql -U music_ingest -d music_ingest -At -c \
  "SELECT output_root FROM storage_config ORDER BY generation DESC LIMIT 1")"
test -n "$output_root"

docker compose exec -T postgres psql -U music_ingest -d music_ingest -v ON_ERROR_STOP=1 -c "
  TRUNCATE TABLE webhook_receipts, jobs, source_records, library_records, provider_snapshots RESTART IDENTITY CASCADE;
  UPDATE source_roots SET scan_state = 'never_scanned', updated_at = CURRENT_TIMESTAMP;
"

docker compose run --rm --no-deps --env "OUTPUT_ROOT=$output_root" music-ingest /bin/sh -c '
  set -eu
  test -d "$OUTPUT_ROOT"
  find "$OUTPUT_ROOT" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
'

docker compose start music-ingest
docker compose exec -T music-ingest python -c "
import urllib.request
urllib.request.urlopen('http://127.0.0.1:8000/healthz', timeout=5).read()
urllib.request.urlopen('http://127.0.0.1:8000/api/settings/source-roots', timeout=5).read()
"
