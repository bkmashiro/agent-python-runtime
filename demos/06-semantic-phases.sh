#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$DEMO_DIR/common.sh"

banner "06: durable phase measurements"
printf 'Fixed source, controlled Host delays, explicit park and re-admission; output is JSON Lines.\n\n'

go run ./cmd/pysolate-phase-bench \
  -guest "$GUEST" \
  -case "${PYSOLATE_PHASE_CASE:-all}" \
  -iterations "${PYSOLATE_PHASE_ITERATIONS:-3}" \
  -preparation "${PYSOLATE_PHASE_PREPARATION:-copy}" \
  -tool-delay "${PYSOLATE_PHASE_TOOL_DELAY:-50ms}" \
  -park-delay "${PYSOLATE_PHASE_PARK_DELAY:-200ms}"
