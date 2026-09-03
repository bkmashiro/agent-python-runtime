#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' 'usage: run-linux-gitchameleon-numpy-derived.sh --output ABSOLUTE_DIR --source-commit HEX40 --source-tree HEX40 --source-epoch SECONDS --host-id gpu31|gpu32|gpu33|gpu34|gpu35 [--runs 1..10] [--order-offset 0|1]'
}

output=""
source_commit=""
source_tree=""
source_epoch=""
host_id=""
runs=10
order_offset=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output=${2:-}; shift 2 ;;
    --source-commit) source_commit=${2:-}; shift 2 ;;
    --source-tree) source_tree=${2:-}; shift 2 ;;
    --source-epoch) source_epoch=${2:-}; shift 2 ;;
    --host-id) host_id=${2:-}; shift 2 ;;
    --runs) runs=${2:-}; shift 2 ;;
    --order-offset) order_offset=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if [[ $output != /* || ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  usage >&2
  exit 2
fi
if [[ ! $host_id =~ ^gpu3[1-5]$ || ! $runs =~ ^[0-9]+$ || $runs -lt 1 || $runs -gt 10 || ! $order_offset =~ ^[01]$ ]]; then
  usage >&2
  exit 2
fi
if [[ -z ${AGENT_RUNTIME_GUEST:-} || ! -f $AGENT_RUNTIME_GUEST ]]; then
  echo 'AGENT_RUNTIME_GUEST must point to the exact numpy-core Guest artifact' >&2
  exit 2
fi
manifest=integration/e2e/testdata/gitchameleon_numpy_subset_v1.json
if [[ ! -f $manifest ]]; then
  echo "missing checked-in NumPy-derived manifest: $manifest" >&2
  exit 2
fi

mkdir -p "$output/raw" "$output/analysis" "$output/input"
cp "$manifest" "$output/input/gitchameleon_numpy_subset_v1.json"
python3 - "$output/platform.json" "$source_commit" "$source_tree" "$source_epoch" "$host_id" "$runs" "$order_offset" "$AGENT_RUNTIME_GUEST" <<'PY'
import hashlib, json, os, pathlib, platform, socket, sys
path, commit, tree, epoch, host_id, runs, order_offset, artifact = sys.argv[1:]
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
artifact_path = pathlib.Path(artifact)
payload = {
    'schema_version': 'pysolate.gitchameleon-numpy-derived-plm.platform.v1',
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
    'order_offset': int(order_offset),
    'artifact_sha256': 'sha256:' + hashlib.sha256(artifact_path.read_bytes()).hexdigest(),
}
pathlib.Path(path).write_text(json.dumps(payload, indent=2, sort_keys=True) + '\n')
PY

PYSOLATE_GITCHAMELEON_NUMPY_OUTPUT_DIR="$output/raw" \
  PYSOLATE_GITCHAMELEON_NUMPY_RUNS="$runs" \
  PYSOLATE_GITCHAMELEON_NUMPY_ORDER_OFFSET="$order_offset" \
  PYSOLATE_EXPERIMENT_SOURCE_COMMIT="$source_commit" \
  PYSOLATE_EXPERIMENT_SOURCE_TREE="$source_tree" \
  PYSOLATE_EXPERIMENT_SOURCE_STATE=clean \
  EVALUATION_HOST_ID="$host_id" \
  go test ./integration/e2e -run '^TestGitChameleonNumPyDerivedPLMFixture$' -count=1 -timeout=3h

python3 - "$output/raw" <<'PY'
import pathlib, sys
paths = list(pathlib.Path(sys.argv[1]).glob('*.json'))
if len(paths) != 60:
    raise SystemExit(f'expected 60 raw cell files, found {len(paths)}')
PY
python3 scripts/analyze-gitchameleon-numpy-derived.py \
  --raw-dir "$output/raw" \
  --manifest "$output/input/gitchameleon_numpy_subset_v1.json" \
  --output "$output/analysis/summary.json" \
  --markdown-output "$output/analysis/results.md" \
  --expected-runs "$runs"

python3 - "$output" "$source_commit" "$source_tree" "$host_id" "$runs" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
commit, tree, host, runs = sys.argv[2:]
def digest(path):
    return 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()
raw = sorted(root.joinpath('raw').glob('*.json'))
summary_path = root / 'analysis/summary.json'
input_path = root / 'input/gitchameleon_numpy_subset_v1.json'
payload = {
    'schema_version': 'pysolate.gitchameleon-numpy-derived-plm.campaign.v1',
    'source_commit': commit,
    'source_tree': tree,
    'source_tree_state': 'clean',
    'host_id': host,
    'runs_per_arm': int(runs),
    'task_count': 15,
    'rate_count': 4,
    'raw_cell_count': len(raw),
    'sample_count': 15 * 4 * int(runs) * 2,
    'paired_comparison_count': 15 * 4 * int(runs),
    'raw': [{'path': str(path.relative_to(root)), 'sha256': digest(path)} for path in raw],
    'platform': {'path': 'platform.json', 'sha256': digest(root / 'platform.json')},
    'input_manifest': {'path': str(input_path.relative_to(root)), 'sha256': digest(input_path)},
    'summary': {'path': str(summary_path.relative_to(root)), 'sha256': digest(summary_path)},
}
(root / 'campaign-manifest.json').write_text(json.dumps(payload, indent=2, sort_keys=True) + '\n')
PY
printf 'campaign_root=%s\n' "$output"
