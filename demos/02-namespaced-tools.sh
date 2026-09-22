#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"

banner "Ordinary Python + dynamic namespaced Host tool"
printf '%s\n' 'Observe: Guest Python calls one allowlisted market.get_price(...) capability; the output reports the Host-call count.'
go run ./examples/python-tools -guest "$GUEST" | pretty_json
