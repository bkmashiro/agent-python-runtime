#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$DEMO_DIR/common.sh"

banner "08: deterministic scheduling policy simulation"
printf 'Fixed common-agent phase traces; no LLM, random arrivals, or runtime policy change.\n\n'

go run ./cmd/pysolate-schedule-sim "$@"
