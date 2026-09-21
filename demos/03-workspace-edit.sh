#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"

banner "Private workspace edit + HTTP + SQLite + idempotent Host write"
printf '%s\n' 'The source fixture stays unchanged; only the private workspace is edited and diffed.'
go run ./examples/workspace-edit -guest "$GUEST" | pretty_json
