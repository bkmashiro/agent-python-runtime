#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"

banner "Dynamic namespaced tool + repository/config/data workflow"
printf '%s\n' 'Guest code calls market.get_prices(...); the Host forwards it to a real local HTTP endpoint.'
go run ./examples/agent-core-usecases -guest "$GUEST" | pretty_json
