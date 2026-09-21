#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"

banner "Private workspace edit + reviewable Host handoff"
printf '%s\n' 'The source stays unchanged; the demo exports changed contents and verifies that their baseline paths remain conflict-free.'
go run ./examples/workspace-edit -guest "$GUEST" | pretty_json
