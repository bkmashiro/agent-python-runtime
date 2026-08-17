#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
CORPUS="$ROOT/research/composableacceptance/testdata/lab-release-readiness-corpus.json"
OUT_ROOT=${1:-"$HOME/.hermes/evidence/pysolate/lab-release-readiness-candidate"}
REPORT="$OUT_ROOT/direct-report.json"
CAPTURE="$OUT_ROOT/body-capture.json"

: "${AGENT_RUNTIME_GUEST:?set AGENT_RUNTIME_GUEST to the pinned WASI Guest artifact}"
SOURCE_COMMIT=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["source_commit"])' "$CORPUS")
install -d -m 0700 "$OUT_ROOT"

PYSOLATE_SPARK_CORPUS="$CORPUS" \
PYSOLATE_ACCEPTANCE_CORE_REPORT="$REPORT" \
PYSOLATE_RESEARCH_BODY_CAPTURE="$CAPTURE" \
PYSOLATE_ACCEPTANCE_SCENARIO=dev-release-readiness \
PYSOLATE_ACCEPTANCE_TREATMENT=all \
PYSOLATE_ACCEPTANCE_MATRIX=smoke \
PYSOLATE_HOST_SOURCE_COMMIT="$SOURCE_COMMIT" \
AGENT_RUNTIME_GUEST="$AGENT_RUNTIME_GUEST" \
go test -v ./integration/e2e -run TestRealGuestSparkScenarioCoreTreatments -count=1

chmod 0600 "$REPORT" "$CAPTURE"
shasum -a 256 "$REPORT" "$CAPTURE"
printf 'candidate evidence written to %s\n' "$OUT_ROOT"
