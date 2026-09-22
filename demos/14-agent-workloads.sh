#!/usr/bin/env bash
set -euo pipefail

source "$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"

banner "20 frozen common Agent Python workloads"
go run ./cmd/pysolate-corpus \
  -guest "$GUEST" \
  -corpus examples/corpus/agent-workloads.jsonl \
  -prepared copy \
  -iterations "${PYSOLATE_WORKLOAD_ITERATIONS:-1}"

banner "Private workspace edit and failure inspection"
PYSOLATE_GUEST="$GUEST" go test ./integration \
  -run '^(TestWorkspaceGuestReadsAndEditsPrivateFiles|TestFailedWorkspaceRunRemainsInspectableUntilCleanup)$' \
  -v -count=1

banner "Real MCP stdio call from Python"
PYSOLATE_GUEST="$GUEST" go test ./examples/mcp-stdio \
  -run '^TestRealStdioMCPToolRunsInsideGuest$' -v -count=1

banner "Durable idempotent effect across process kill"
PYSOLATE_GUEST="$GUEST" go test ./service/durable \
  -run '^TestHTTPServiceRecoversAfterRealProcessKill$' -v -count=1
