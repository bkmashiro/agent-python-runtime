#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$DEMO_DIR/common.sh"

COMMON_ARGS=(
  -guest "$GUEST"
  -mode executor
  -tasks 2
  -active 1
  -resident 2
  -tool-active 2
  -heap 0
  -hold 100ms
  -cow=false
)

banner "07: inline Host wait"
go run ./cmd/pysolate-queue-bench "${COMMON_ARGS[@]}" -external-io=false | pretty_json

banner "07: ExternalIO live wait"
go run ./cmd/pysolate-queue-bench "${COMMON_ARGS[@]}" -external-io=true | pretty_json
