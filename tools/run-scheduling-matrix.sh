#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GUEST="${PYSOLATE_GUEST:-dist/pysolate.wasm}"
OUTPUT_DIR="${PYSOLATE_MATRIX_OUTPUT:-docs/performance-data/scheduling-matrix/local}"
ITERATIONS="${PYSOLATE_MATRIX_ITERATIONS:-3}"
CASE="${PYSOLATE_MATRIX_CASE:-all}"
DELAYS="${PYSOLATE_MATRIX_DELAYS:-5ms 50ms 200ms 1s}"
HEAPS="${PYSOLATE_MATRIX_HEAPS:-0 8 64}"
PREPARATIONS="${PYSOLATE_MATRIX_PREPARATIONS:-copy cow}"
if [[ -n "${PYSOLATE_MATRIX_COW:-}" ]]; then
  QUEUE_COW="$PYSOLATE_MATRIX_COW"
elif [[ "$(uname -s)" == "Linux" ]]; then
  QUEUE_COW=true
else
  QUEUE_COW=false
fi

if [[ ! -f "$GUEST" ]]; then
  printf 'missing Guest artifact: %s\n' "$GUEST" >&2
  exit 2
fi
if [[ "$(uname -s)" != "Linux" && " $PREPARATIONS " == *" cow "* ]]; then
  printf 'COW measurements require Linux; set PYSOLATE_MATRIX_PREPARATIONS=copy on this host.\n' >&2
  exit 2
fi

mkdir -p "$OUTPUT_DIR"

sanitize() {
  printf '%s' "$1" | tr -c '[:alnum:]' '_'
}

for preparation in $PREPARATIONS; do
  for delay in $DELAYS; do
    key="$(sanitize "$preparation-$delay")"
    for external in false true; do
      go run ./cmd/pysolate-phase-bench \
        -guest "$GUEST" \
        -case "$CASE" \
        -iterations "$ITERATIONS" \
        -preparation "$preparation" \
        -tool-delay "$delay" \
        -park-delay 200ms \
        -external-io="$external" \
        > "$OUTPUT_DIR/phase-${key}-external-${external}.jsonl"
    done
  done
done

for heap in $HEAPS; do
  for delay in $DELAYS; do
    key="$(sanitize "heap-${heap}mib-$delay")"
    for external in false true; do
      go run ./cmd/pysolate-queue-bench \
        -guest "$GUEST" \
        -mode executor \
        -tasks 2 \
        -active 1 \
        -resident 2 \
        -tool-active 2 \
        -heap "$heap" \
        -hold "$delay" \
        -cow="$QUEUE_COW" \
        -external-io="$external" \
        > "$OUTPUT_DIR/live-${key}-external-${external}.json"
    done
  done
done

go run ./cmd/pysolate-schedule-sim > "$OUTPUT_DIR/simulation.jsonl"
go run ./cmd/pysolate-schedule-sim \
  -scenario canonical \
  -tasks 2,8,32 \
  -running 1,2,4 \
  -resident-multipliers 1,2,4,8 \
  -tools 1,2,4,8 \
  -policies fifo \
  > "$OUTPUT_DIR/canonical-sweep.jsonl"

printf 'wrote scheduling matrix to %s\n' "$OUTPUT_DIR"
