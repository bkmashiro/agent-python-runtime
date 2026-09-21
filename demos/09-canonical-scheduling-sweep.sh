#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$DEMO_DIR/common.sh"

banner "09: idealized live-I/O scheduling sweep"
printf 'Canonical CPU -> ExternalIO -> CPU tasks; deterministic model only.\n\n'

go run ./cmd/pysolate-schedule-sim \
  -scenario canonical \
  -tasks 2,8 \
  -io-ratios 0.1,0.25,0.5,1,2,5,10,50,100 \
  -running 1,2 \
  -resident-multipliers 1,2,4 \
  -tools 1,2,4 \
  -policies fifo \
  "$@"
