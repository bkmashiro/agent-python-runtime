#!/usr/bin/env bash
set -euo pipefail

source "$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"

banner "Full-code preparation overlaps independent Host reads"
printf '%s\n' \
  'This uses the complete submitted script; source streaming is not supported.' \
  'Each Host read has the same controlled delay; the result must stay identical.'

go run ./examples/fullcode-overlap \
  -guest "$GUEST" \
  -tool-delay "${PYSOLATE_DEMO_TOOL_DELAY:-150ms}" \
  -samples "${PYSOLATE_DEMO_SAMPLES:-5}" | pretty_json