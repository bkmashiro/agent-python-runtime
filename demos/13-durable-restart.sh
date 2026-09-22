#!/usr/bin/env bash
set -euo pipefail

source "$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"

banner "Durable effect recovery across a real process kill"
printf '%s\n' 'The child service commits an idempotent Host effect, is killed before completion,'
printf '%s\n' 'then restarts from SQLite and reuses the operation key so the effect occurs once.'
PYSOLATE_GUEST="$GUEST" go test ./service/durable \
  -run '^TestHTTPServiceRecoversAfterRealProcessKill$' -v -count=1
