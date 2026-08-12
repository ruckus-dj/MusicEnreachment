#!/bin/sh
set -eu

api_key="$(sed -n 's|.*<ApiKey>\(.*\)</ApiKey>.*|\1|p' /config/config.xml)"
test -n "$api_key"

payload='{"name":"music-ingest","onGrab":false,"onReleaseImport":true,"onUpgrade":true,"onRename":true,"onArtistAdd":false,"onArtistDelete":false,"onAlbumDelete":true,"onHealthIssue":false,"onHealthRestored":false,"onDownloadFailure":false,"onImportFailure":false,"onTrackRetag":false,"onApplicationUpdate":false,"includeHealthWarnings":false,"fields":[{"name":"url","value":"http://music-ingest:8000/api/intake/lidarr"},{"name":"method","value":1}],"implementation":"Webhook","configContract":"WebhookSettings","tags":[]}'
headers="X-Api-Key: $api_key"

notifications="$(curl --fail --silent --show-error "$LIDARR_URL/api/v1/notification" --header "$headers")"
if printf '%s' "$notifications" | grep -F -q 'http://music-ingest:8000/api/intake/lidarr'; then
    notification_id="$(printf '%s' "$notifications" | sed -n 's/.*"id":[[:space:]]*\([0-9][0-9]*\).*/\1/p')"
    test -n "$notification_id"
    curl --fail --silent --show-error --request DELETE "$LIDARR_URL/api/v1/notification/$notification_id" \
        --header "$headers" >/dev/null
fi
curl --fail --silent --show-error --request POST "$LIDARR_URL/api/v1/notification" \
    --header "$headers" --header 'Content-Type: application/json' --data "$payload" >/dev/null

curl --fail --silent --show-error "$LIDARR_URL/api/v1/notification" --header "$headers" \
    | grep -F -q 'http://music-ingest:8000/api/intake/lidarr'
test_results="$(curl --fail --silent --show-error --request POST "$LIDARR_URL/api/v1/notification/testall" --header "$headers")"
printf '%s' "$test_results" | grep -E -q '"isValid"[[:space:]]*:[[:space:]]*true'
