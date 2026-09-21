#!/usr/bin/env bash

set -euo pipefail
SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=demos/common.sh
source "$SCRIPT_DIR/common.sh"

banner "Official MCP SDK over stdio -> dynamic Guest Python tool"
printf '%s\n' 'The Host starts a local MCP subprocess, discovers its catalog, and injects catalog.lookup without rebuilding the Guest.'
go run ./examples/mcp-stdio -guest "$GUEST" | pretty_json
