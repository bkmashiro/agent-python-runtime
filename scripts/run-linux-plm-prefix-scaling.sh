#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' 'usage: run-linux-plm-prefix-scaling.sh --output ABSOLUTE_DIR --source-commit HEX40 --source-tree HEX40 --source-epoch SECONDS --host-id gpu31|gpu32|gpu33|gpu34|gpu35 [--runs 1..10] [--calls 1,2,4,8] [--windows-ms 0,100,200,400]'
}

output=""
source_commit=""
source_tree=""
source_epoch=""
host_id=""
runs=10
calls_csv=1,2,4,8
windows_csv=0,100,200,400
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output=${2:-}; shift 2 ;;
    --source-commit) source_commit=${2:-}; shift 2 ;;
    --source-tree) source_tree=${2:-}; shift 2 ;;
    --source-epoch) source_epoch=${2:-}; shift 2 ;;
    --host-id) host_id=${2:-}; shift 2 ;;
    --runs) runs=${2:-}; shift 2 ;;
    --calls) calls_csv=${2:-}; shift 2 ;;
    --windows-ms) windows_csv=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if [[ $output != /* || ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  usage >&2
  exit 2
fi
if [[ ! $host_id =~ ^gpu3[1-5]$ || ! $runs =~ ^[0-9]+$ || $runs -lt 1 || $runs -gt 10 ]]; then
  usage >&2
  exit 2
fi
if [[ -z ${AGENT_RUNTIME_GUEST:-} || ! -f $AGENT_RUNTIME_GUEST ]]; then
  echo 'AGENT_RUNTIME_GUEST must point to the exact base Guest artifact' >&2
  exit 2
fi

IFS=, read -r -a calls_values <<<"$calls_csv"
IFS=, read -r -a window_values <<<"$windows_csv"
for calls in "${calls_values[@]}"; do
  case "$calls" in 1|2|4|8) ;; *) echo "invalid call count: $calls" >&2; exit 2 ;; esac
done
for window in "${window_values[@]}"; do
  case "$window" in 0|100|200|400) ;; *) echo "invalid source window: $window" >&2; exit 2 ;; esac
done

mkdir -p "$output/raw" "$output/analysis"
python3 - "$output/platform.json" "$source_commit" "$source_tree" "$source_epoch" "$host_id" "$runs" "$calls_csv" "$windows_csv" <<'PY'
import json, os, pathlib, platform, socket, sys
path, commit, tree, epoch, host_id, runs, calls, windows = sys.argv[1:]
cpu_model = "unknown"
memory_bytes = 0
try:
    for line in pathlib.Path('/proc/cpuinfo').read_text().splitlines():
        if line.startswith('model name'):
            cpu_model = line.split(':', 1)[1].strip()
            break
except OSError:
    pass
try:
    for line in pathlib.Path('/proc/meminfo').read_text().splitlines():
        if line.startswith('MemTotal:'):
            memory_bytes = int(line.split()[1]) * 1024
            break
except OSError:
    pass
payload = {
    'schema_version': 'pysolate.plm-prefix-source-scaling.platform.v1',
    'source_commit': commit,
    'source_tree': tree,
    'source_tree_state': 'clean',
    'source_epoch': int(epoch),
    'host_id': host_id,
    'hostname': socket.gethostname(),
    'os': platform.system(),
    'kernel': platform.release(),
    'architecture': platform.machine(),
    'logical_cpus': os.cpu_count() or 1,
    'cpu_model': cpu_model,
    'memory_bytes': memory_bytes,
    'load_1m_before': os.getloadavg()[0],
    'runs_per_arm': int(runs),
    'call_counts': [int(value) for value in calls.split(',')],
    'source_windows_ms': [int(value) for value in windows.split(',')],
}
pathlib.Path(path).write_text(json.dumps(payload, indent=2, sort_keys=True) + '\n')
PY

for calls in "${calls_values[@]}"; do
  for window in "${window_values[@]}"; do
    cell="calls-${calls}-window-${window}ms"
    order_offset=$(( (calls + window / 100) % 2 ))
    printf '==> %s runs=%s order_offset=%s\n' "$cell" "$runs" "$order_offset"
    PYSOLATE_PLM_PREFIX_SCALING_OUTPUT="$output/raw/$cell.json" \
      PYSOLATE_PLM_PREFIX_SCALING_CALLS="$calls" \
      PYSOLATE_PLM_PREFIX_SCALING_WINDOW_MS="$window" \
      PYSOLATE_PLM_PREFIX_SCALING_RUNS="$runs" \
      PYSOLATE_PLM_PREFIX_SCALING_ORDER_OFFSET="$order_offset" \
      PYSOLATE_EXPERIMENT_SOURCE_COMMIT="$source_commit" \
      PYSOLATE_EXPERIMENT_SOURCE_TREE="$source_tree" \
      PYSOLATE_EXPERIMENT_SOURCE_STATE=clean \
      EVALUATION_HOST_ID="$host_id" \
      go test ./integration/e2e -run '^TestPLMPrefixSourceScalingFixture$' -count=1 -timeout=30m
    test -s "$output/raw/$cell.json"
  done
done

if [[ $calls_csv == 1,2,4,8 && $windows_csv == 0,100,200,400 ]]; then
  python3 scripts/analyze-plm-prefix-scaling.py \
    --raw-dir "$output/raw" \
    --output "$output/analysis/summary.json" \
    --markdown-output "$output/analysis/results.md" \
    --expected-runs "$runs"
fi

python3 - "$output" "$source_commit" "$source_tree" "$host_id" "$runs" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
commit, tree, host, runs = sys.argv[2:]
def digest(path):
    return 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()
raw = sorted(root.joinpath('raw').glob('*.json'))
payload = {
    'schema_version': 'pysolate.plm-prefix-source-scaling.campaign.v1',
    'source_commit': commit,
    'source_tree': tree,
    'source_tree_state': 'clean',
    'host_id': host,
    'runs_per_arm': int(runs),
    'raw_cell_count': len(raw),
    'raw': [{'path': str(path.relative_to(root)), 'sha256': digest(path)} for path in raw],
    'platform': {'path': 'platform.json', 'sha256': digest(root / 'platform.json')},
}
summary = root / 'analysis/summary.json'
if summary.exists():
    payload['summary'] = {'path': str(summary.relative_to(root)), 'sha256': digest(summary)}
(root / 'campaign-manifest.json').write_text(json.dumps(payload, indent=2, sort_keys=True) + '\n')
PY
printf 'campaign_root=%s\n' "$output"
