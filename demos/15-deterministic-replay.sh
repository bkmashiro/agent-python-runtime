#!/usr/bin/env bash
set -euo pipefail

source "$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/common.sh"

banner "Deterministic Guest inputs across separate processes"
PYSOLATE_GUEST="$GUEST" go test . \
  -run '^(TestRecordedDeterminismAcrossProcesses|TestRecordedToolOutcomeAndPythonFailure|TestPreparedRecordedMatchesFresh)$' \
  -v -count=1

banner "Durable replay preserves outcomes and prepared seed state"
PYSOLATE_GUEST="$GUEST" go test ./durable \
  -run '^(TestRealApprovalReopenAndFinalReplay|TestPreparedResumePreservesHistoryAndSeed|TestJournalLookupErrorOnlyAndCompletedReplay)$' \
  -v -count=1
