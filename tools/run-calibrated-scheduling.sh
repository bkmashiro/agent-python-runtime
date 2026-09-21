#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GUEST="${PYSOLATE_GUEST:-dist/pysolate.wasm}"
OUTPUT_DIR="${PYSOLATE_CALIBRATION_OUTPUT:-docs/performance-data/scheduling-calibration/local}"
TASKS="${PYSOLATE_CALIBRATION_TASKS:-2 4 8}"
REPEATS="${PYSOLATE_CALIBRATION_REPEATS:-3}"
ITERATIONS="${PYSOLATE_CALIBRATION_PHASE_ITERATIONS:-5}"
RUNNING="${PYSOLATE_CALIBRATION_RUNNING:-2}"
RESIDENT="${PYSOLATE_CALIBRATION_RESIDENT:-8}"
TOOLS="${PYSOLATE_CALIBRATION_TOOLS:-4}"
DELAY="${PYSOLATE_CALIBRATION_DELAY:-50ms}"
HEAP_MIB="${PYSOLATE_CALIBRATION_HEAP_MIB:-0}"
TOLERANCE="${PYSOLATE_CALIBRATION_TOLERANCE:-0.15}"
if [[ -n "${PYSOLATE_CALIBRATION_COW:-}" ]]; then
  COW="$PYSOLATE_CALIBRATION_COW"
elif [[ "$(uname -s)" == "Linux" ]]; then
  COW=true
else
  COW=false
fi

if [[ ! -f "$GUEST" ]]; then
  printf 'missing Guest artifact: %s\n' "$GUEST" >&2
  exit 2
fi
if ! [[ "$REPEATS" =~ ^[1-9][0-9]*$ && "$ITERATIONS" =~ ^[1-9][0-9]*$ ]]; then
  printf 'repeat and phase iteration counts must be positive integers\n' >&2
  exit 2
fi

mkdir -p "$OUTPUT_DIR"
PHASE="$OUTPUT_DIR/phase-common.jsonl"
OBSERVED="$OUTPUT_DIR/observed-read-finish.jsonl"
REPORT="$OUTPUT_DIR/comparison.jsonl"
: > "$OBSERVED"

go run ./cmd/pysolate-phase-bench \
  -guest "$GUEST" \
  -case read-finish,tool-chain,numpy-local,read-numpy \
  -iterations "$ITERATIONS" \
  -preparation "$([[ "$COW" == true ]] && printf cow || printf copy)" \
  -tool-delay "$DELAY" \
  -external-io=true \
  > "$PHASE"

max_tasks=0
for tasks in $TASKS; do
  if ! [[ "$tasks" =~ ^[1-9][0-9]*$ ]]; then
    printf 'invalid task count: %s\n' "$tasks" >&2
    exit 2
  fi
  if (( tasks > max_tasks )); then
    max_tasks=$tasks
  fi
  for ((repeat = 0; repeat < REPEATS; repeat++)); do
    for external in false true; do
      go run ./cmd/pysolate-queue-bench \
        -guest "$GUEST" \
        -case read-finish \
        -mode executor \
        -tasks "$tasks" \
        -active "$RUNNING" \
        -resident "$RESIDENT" \
        -tool-active "$TOOLS" \
        -heap "$HEAP_MIB" \
        -hold "$DELAY" \
        -cow="$COW" \
        -external-io="$external" \
        >> "$OBSERVED"
    done
  done
done

for case_name in read-finish tool-chain numpy-local read-numpy; do
  go run ./cmd/pysolate-calibrate \
    -phase "$PHASE" \
    -case "$case_name" \
    -resident-mib "$HEAP_MIB" \
    -tasks "$max_tasks" \
    > "$OUTPUT_DIR/workload-$case_name.json"
done

go run ./cmd/pysolate-calibrate \
  -phase "$PHASE" \
  -case read-finish \
  -resident-mib "$HEAP_MIB" \
  -observed "$OBSERVED" \
  -tolerance "$TOLERANCE" \
  > "$REPORT"

printf 'wrote calibrated scheduling evidence to %s\n' "$OUTPUT_DIR"
