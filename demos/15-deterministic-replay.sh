#!/usr/bin/env bash
set -euo pipefail

source "$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"

banner "Deterministic randomness and Tool outcome replay"
go run ./examples/replay-tool-random -guest "$GUEST" | pretty_json

if [[ "${PYSOLATE_REPLAY_CONFORMANCE:-1}" == "0" ]]; then
  exit 0
fi

banner "Deterministic Guest inputs across separate processes"
PYSOLATE_GUEST="$GUEST" go test . \
  -run '^(TestRecordedDeterminismAcrossProcesses|TestRecordedToolOutcomeAndPythonFailure|TestPreparedRecordedMatchesFresh)$' \
  -v -count=1

banner "Durable replay preserves outcomes and prepared seed state"
PYSOLATE_GUEST="$GUEST" go test ./durable \
  -run '^(TestRealApprovalReopenAndFinalReplay|TestPreparedResumePreservesHistoryAndSeed|TestJournalLookupErrorOnlyAndCompletedReplay)$' \
  -v -count=1
