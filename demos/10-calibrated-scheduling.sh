#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$DEMO_DIR/common.sh"

banner "10: calibrated scheduling prediction"
printf 'Measure one Run, replay its phases, then compare real 2/4-Run batches.\n\n'

OUTPUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pysolate-calibration-demo.XXXXXX")"
trap 'rm -rf "$OUTPUT_DIR"' EXIT

PYSOLATE_GUEST="$GUEST" \
PYSOLATE_CALIBRATION_OUTPUT="$OUTPUT_DIR" \
PYSOLATE_CALIBRATION_TASKS='2 4' \
PYSOLATE_CALIBRATION_REPEATS=1 \
PYSOLATE_CALIBRATION_PHASE_ITERATIONS=3 \
PYSOLATE_CALIBRATION_RUNNING=2 \
PYSOLATE_CALIBRATION_RESIDENT=4 \
PYSOLATE_CALIBRATION_TOOLS=4 \
"$REPO_ROOT/tools/run-calibrated-scheduling.sh"

python3 - "$OUTPUT_DIR/comparison.jsonl" <<'PY'
import json
import sys

rows = [json.loads(line) for line in open(sys.argv[1], encoding="utf-8")]
metadata = rows[0]
print(
    f"comparisons={metadata['comparisons']} "
    f"within_15_percent={metadata['within_tolerance']} "
    f"max_error={metadata['max_relative_error']:.1%}"
)
for row in rows:
    if row["type"] != "summary":
        continue
    mode = "live-I/O" if row["external_io"] else "inline"
    print(
        f"N={row['tasks']} {mode}: "
        f"observed={row['mean_observed_batch_ns'] / 1e6:.2f}ms "
        f"predicted={row['mean_predicted_makespan_ns'] / 1e6:.2f}ms "
        f"error={row['mean_relative_error']:.1%}"
    )
PY
