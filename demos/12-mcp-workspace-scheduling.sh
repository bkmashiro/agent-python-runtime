#!/usr/bin/env bash

set -euo pipefail
DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=demos/common.sh
source "$DEMO_DIR/common.sh"

TASKS="${TASKS:-2}"
ITERATIONS="${ITERATIONS:-3}"
TOOL_DELAY="${TOOL_DELAY:-50ms}"

banner "12: real MCP tool chain -> scheduled handoff -> workspace ChangeSet"
printf '%s\n' 'Compares inline and ExternalIO scheduling, then performs deterministic private-workspace edits from the fetched data.'
go run ./examples/mcp-workspace-scheduling \
  -guest "$GUEST" \
  -tasks "$TASKS" \
  -iterations "$ITERATIONS" \
  -tool-delay "$TOOL_DELAY" | pretty_json
