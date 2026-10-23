#!/bin/sh
set -eu

curl --fail-with-body --silent --show-error \
  -X POST "${BROKER_URL:-http://localhost:8080}/asset-uploads" \
  -H 'Content-Type: application/json' \
  --data '{"asset_id":"asset-42","player_id":"player-7","live_event":"winter-cup","filename":"banner.png","content_type":"image/png","bytes":4096}'
