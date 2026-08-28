#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/run-linux-evaluation-sweeps.sh \
  --output ABSOLUTE_DIR --source-commit HEX40 --source-tree HEX40 --source-epoch SECONDS \
  --host-id gpu31|gpu32|gpu33|gpu34|gpu35 --order-offset N \
  --plm-crossover-runs N --cow-fanout-runs N [--build-cache-root ABSOLUTE_DIR]

Run the two source-bound economics gates once on one Linux/x86_64 workstation.
The base and numpy-core Guest artifacts are built once and retained in the
per-host bundle. Existing output is never overwritten.
EOF
}

output=""
source_commit=""
source_tree=""
source_epoch=""
host_id=""
order_offset=""
plm_crossover_runs=""
cow_fanout_runs=""
build_cache_root=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output=${2:-}; shift 2 ;;
    --source-commit) source_commit=${2:-}; shift 2 ;;
    --source-tree) source_tree=${2:-}; shift 2 ;;
    --source-epoch) source_epoch=${2:-}; shift 2 ;;
    --host-id) host_id=${2:-}; shift 2 ;;
    --order-offset) order_offset=${2:-}; shift 2 ;;
    --plm-crossover-runs|--plm-runs) plm_crossover_runs=${2:-}; shift 2 ;;
    --cow-fanout-runs|--cow-runs) cow_fanout_runs=${2:-}; shift 2 ;;
    --build-cache-root) build_cache_root=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "evaluation sweeps require Linux x86_64" >&2
  exit 3
fi
if [[ $output != /* || ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid output or source identity" >&2
  exit 4
fi
if [[ ! $host_id =~ ^gpu3[1-5]$ ]]; then
  echo "--host-id must be gpu31..gpu35" >&2
  exit 4
fi
if [[ ! $order_offset =~ ^[0-9]+$ ]]; then
  echo "--order-offset must be a non-negative integer" >&2
  exit 4
fi
if [[ ! $plm_crossover_runs =~ ^[0-9]+$ ]] || (( plm_crossover_runs < 3 || plm_crossover_runs > 20 )); then
  echo "--plm-crossover-runs must be in [3,20]" >&2
  exit 4
fi
if [[ ! $cow_fanout_runs =~ ^[0-9]+$ ]] || (( cow_fanout_runs < 3 || cow_fanout_runs > 20 )); then
  echo "--cow-fanout-runs must be in [3,20]" >&2
  exit 4
fi
if [[ -n $build_cache_root && $build_cache_root != /* ]]; then
  echo "--build-cache-root must be absolute" >&2
  exit 4
fi
if [[ -e $output ]]; then
  if [[ ! -d $output || -L $output ]] || ! python3 - "$output" <<'PY'
import pathlib
import sys
raise SystemExit(0 if not any(pathlib.Path(sys.argv[1]).iterdir()) else 1)
PY
  then
    echo "output must be absent or an empty real directory" >&2
    exit 5
  fi
else
  mkdir -p "$output"
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
scripts/verify-source-identity.sh "$source_commit" "$source_tree" "$source_epoch"
export SOURCE_DATE_EPOCH="$source_epoch"
export GITHUB_SHA="$source_commit"
export AGENT_RUNTIME_SOURCE_TREE="$source_tree"
export EVALUATION_HOST_ID="$host_id"
export EVALUATION_ORDER_OFFSET="$order_offset"
export PLM_CROSSOVER_RUNS="$plm_crossover_runs"
export COW_FANOUT_RUNS="$cow_fanout_runs"
if [[ -n $build_cache_root ]]; then
  mkdir -p "$build_cache_root"
  export AGENT_RUNTIME_BUILD_CACHE_ROOT="$build_cache_root"
  export AGENT_RUNTIME_BUILD_CACHE_MODE=auto
fi

mkdir -p "$output/artifacts"
AGENT_RUNTIME_ARTIFACT_PROFILE=base ./guest/build/build-guest.sh
cp dist/agent-python-runtime.wasm "$output/artifacts/base.wasm"
AGENT_RUNTIME_ARTIFACT_PROFILE=numpy-core ./guest/build/build-guest.sh
cp dist/agent-python-runtime-numpy-core.wasm "$output/artifacts/numpy-core.wasm"

python3 - "$output/platform.json" "$source_commit" "$source_tree" "$source_epoch" "$host_id" "$order_offset" "$plm_crossover_runs" "$cow_fanout_runs" <<'PY'
import json
import os
import pathlib
import platform
import socket
import sys

path, commit, tree, epoch, host_id, offset, plm_runs, cow_runs = sys.argv[1:]
root = pathlib.Path("/vol/bitbucket/ys25/pysolate")
free_disk = 0
try:
    free_disk = os.statvfs(root).f_bavail * os.statvfs(root).f_frsize
except OSError:
    pass
free_memory = 0
try:
    for line in pathlib.Path("/proc/meminfo").read_text().splitlines():
        if line.startswith("MemAvailable:"):
            free_memory = int(line.split()[1]) * 1024
            break
except OSError:
    pass
logical_cpus = os.cpu_count() or 1
load_1m = os.getloadavg()[0]
payload = {
    "schema_version": "pysolate.platform.v1",
    "hostname": socket.gethostname(),
    "architecture": platform.machine(),
    "kernel": platform.release(),
    "logical_cpus": logical_cpus,
    "load_1m": load_1m,
    "normalized_load": load_1m / logical_cpus,
    "free_disk_bytes": free_disk,
    "free_memory_bytes": free_memory,
    "source_commit": commit,
    "source_tree": tree,
    "source_epoch": int(epoch),
    "host_id": host_id,
    "evaluation_host_id": host_id,
    "order_offset": int(offset),
    "plm_crossover_runs": int(plm_runs),
    "cow_fanout_runs": int(cow_runs),
}
pathlib.Path(path).write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
PY

# Keep both generic and gate-specific names explicit: the gates consume the
# latter while the former makes the artifact boundary inspectable in logs.
PLM_CROSSOVER_SOURCE_COMMIT="$source_commit" \
PLM_CROSSOVER_SOURCE_TREE="$source_tree" \
PLM_CROSSOVER_SOURCE_EPOCH="$source_epoch" \
PLM_TARGET_COMMIT="$source_commit" \
PLM_SOURCE_TREE="$source_tree" \
PLM_ARTIFACT_SOURCE_COMMIT="$source_commit" \
PLM_CROSSOVER_BASE_ARTIFACT="$output/artifacts/base.wasm" \
PLM_CROSSOVER_NUMPY_ARTIFACT="$output/artifacts/numpy-core.wasm" \
BASE_ARTIFACT="$output/artifacts/base.wasm" \
NUMPY_ARTIFACT="$output/artifacts/numpy-core.wasm" \
AGENT_RUNTIME_BASE_GUEST="$output/artifacts/base.wasm" \
AGENT_RUNTIME_NUMPY_GUEST="$output/artifacts/numpy-core.wasm" \
AGENT_RUNTIME_GUEST="$output/artifacts/base.wasm" \
SOURCE_COMMIT="$source_commit" SOURCE_TREE="$source_tree" SOURCE_EPOCH="$source_epoch" \
EVALUATION_HOST_ID="$host_id" EVALUATION_ORDER_OFFSET="$order_offset" \
PLM_CROSSOVER_RUNS="$plm_crossover_runs" COW_FANOUT_RUNS="$cow_fanout_runs" \
  scripts/plm-crossover-economics-gate.sh "$output/plm-crossover.json"

COW_FANOUT_SOURCE_COMMIT="$source_commit" \
COW_FANOUT_SOURCE_TREE="$source_tree" \
COW_FANOUT_SOURCE_EPOCH="$source_epoch" \
PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT="$source_commit" \
PYSOLATE_PREPARED_FAMILY_SOURCE_TREE="$source_tree" \
COW_ORDER_OFFSET="$order_offset" \
COW_FANOUT_BASE_ARTIFACT="$output/artifacts/base.wasm" \
COW_FANOUT_NUMPY_ARTIFACT="$output/artifacts/numpy-core.wasm" \
BASE_ARTIFACT="$output/artifacts/base.wasm" \
NUMPY_ARTIFACT="$output/artifacts/numpy-core.wasm" \
AGENT_RUNTIME_BASE_GUEST="$output/artifacts/base.wasm" \
AGENT_RUNTIME_NUMPY_GUEST="$output/artifacts/numpy-core.wasm" \
AGENT_RUNTIME_GUEST="$output/artifacts/numpy-core.wasm" \
SOURCE_COMMIT="$source_commit" SOURCE_TREE="$source_tree" SOURCE_EPOCH="$source_epoch" \
EVALUATION_HOST_ID="$host_id" EVALUATION_ORDER_OFFSET="$order_offset" \
PLM_CROSSOVER_RUNS="$plm_crossover_runs" COW_FANOUT_RUNS="$cow_fanout_runs" \
  scripts/cow-fanout-economics-gate.sh "$output/cow-fanout.json"

python3 scripts/project-linux-evaluation-sweeps.py \
  --root "$output" \
  --source-commit "$source_commit" \
  --source-tree "$source_tree" \
  --source-epoch "$source_epoch" \
  --host-id "$host_id" \
  --order-offset "$order_offset" \
  --plm-crossover-runs "$plm_crossover_runs" \
  --cow-fanout-runs "$cow_fanout_runs" \
  --output "$output/manifest.json"
test -f "$output/SHA256SUMS"
printf 'sweep_manifest=%s\n' "$output/manifest.json"
