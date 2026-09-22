#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"

banner "Ordinary Python + dynamic namespaced Host tool"
printf '%s\n' 'Guest Python calls market.get_price(...); the Host grants one allowlisted capability.'
go run ./examples/python-tools -guest "$GUEST" | pretty_json
