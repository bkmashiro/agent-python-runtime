#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: run-linux-evaluation-suite.sh \
  --output ABSOLUTE_DIR --source-commit HEX40 --source-tree HEX40 --source-epoch SECONDS \
  [--runs 5] [--fanout 4] [--build-cache-root ABSOLUTE_DIR]

Run the PLM, prepared-family COW, and producer-sharing evidence lanes on one
Linux/x86-64 host. The source tree and output directory must be private to this
run. Existing output is never overwritten.
EOF
}

output=""
source_commit=""
source_tree=""
source_epoch=""
runs=5
fanout=4
build_cache_root=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output=${2:-}; shift 2 ;;
    --source-commit) source_commit=${2:-}; shift 2 ;;
    --source-tree) source_tree=${2:-}; shift 2 ;;
    --source-epoch) source_epoch=${2:-}; shift 2 ;;
    --runs) runs=${2:-}; shift 2 ;;
    --fanout) fanout=${2:-}; shift 2 ;;
    --build-cache-root) build_cache_root=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "evaluation suite requires Linux x86_64" >&2
  exit 3
fi
if [[ $output != /* || ! $source_commit =~ ^[0-9a-f]{40}$ ||
  ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid output or source identity" >&2
  exit 4
fi
if [[ ! $runs =~ ^[0-9]+$ ]] || (( runs < 3 || runs > 20 )); then
  echo "--runs must be in [3,20]" >&2
  exit 4
fi
if [[ ! $fanout =~ ^[0-9]+$ ]] || (( fanout < 1 || fanout > 8 )); then
  echo "--fanout must be in [1,8]" >&2
  exit 4
fi
if [[ -e $output ]]; then
  if [[ ! -d $output || -L $output ]] || ! python3 - "$output" <<'PY'
import pathlib, sys
raise SystemExit(0 if not any(pathlib.Path(sys.argv[1]).iterdir()) else 1)
PY
  then
    echo "output must be absent or an empty real directory" >&2
    exit 5
  fi
else
  mkdir -p "$output"
fi
if [[ -n $build_cache_root && $build_cache_root != /* ]]; then
  echo "--build-cache-root must be absolute" >&2
  exit 5
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
export SOURCE_DATE_EPOCH="$source_epoch"
export GITHUB_SHA="$source_commit"
export AGENT_RUNTIME_SOURCE_TREE="$source_tree"
if [[ -n $build_cache_root ]]; then
  mkdir -p "$build_cache_root"
  export AGENT_RUNTIME_BUILD_CACHE_ROOT="$build_cache_root"
  export AGENT_RUNTIME_BUILD_CACHE_MODE=auto
fi

mkdir -p "$output/artifacts" "$output/plm" "$output/prepared-family" "$output/producer/private"
python3 - "$output/platform.json" <<'PY'
import json, os, pathlib, platform, socket, subprocess, sys

def command(*args):
    return subprocess.check_output(args, text=True).strip()

os_release = {}
for line in pathlib.Path('/etc/os-release').read_text().splitlines():
    if '=' in line:
        key, value = line.split('=', 1)
        os_release[key] = value.strip().strip('"')
cpu_model = ''
for line in pathlib.Path('/proc/cpuinfo').read_text().splitlines():
    if line.lower().startswith('model name'):
        cpu_model = line.split(':', 1)[1].strip()
        break
memory_bytes = 0
for line in pathlib.Path('/proc/meminfo').read_text().splitlines():
    if line.startswith('MemTotal:'):
        memory_bytes = int(line.split()[1]) * 1024
        break
payload = {
    'schema_version': 'pysolate.platform.v1',
    'hostname': socket.gethostname(),
    'os': os_release.get('PRETTY_NAME', platform.system()),
    'kernel': platform.release(),
    'architecture': platform.machine(),
    'cpu_model': cpu_model,
    'logical_cpus': os.cpu_count(),
    'memory_bytes': memory_bytes,
    'go_version': command('go', 'version'),
    'python_version': command('python3', '--version'),
    'pss_method': '/proc/self/smaps_rollup',
}
pathlib.Path(sys.argv[1]).write_text(json.dumps(payload, indent=2, sort_keys=True) + '\n')
PY

printf '==> build base Guest\n'
AGENT_RUNTIME_ARTIFACT_PROFILE=base ./guest/build/build-guest.sh
cp dist/agent-python-runtime.wasm "$output/artifacts/base.wasm"

printf '==> PLM one-read and four-read economics\n'
PLM_TARGET_COMMIT="$source_commit" \
AGENT_RUNTIME_GUEST="$output/artifacts/base.wasm" \
PLM_ECONOMICS_RUNS="$runs" \
  scripts/plm-economics-gate.sh "$output/plm/one-read.json"
PLM_TARGET_COMMIT="$source_commit" \
PLM_ARTIFACT_SOURCE_COMMIT="$source_commit" \
AGENT_RUNTIME_GUEST="$output/artifacts/base.wasm" \
PLM_ECONOMICS_RUNS="$runs" \
  scripts/plm-multiread-economics-gate.sh "$output/plm/four-read.json"

printf '==> producer-sharing campaign\n'
go run ./research/workflowbench/cmd/transparent-campaign \
  --artifact "$output/artifacts/base.wasm" \
  --artifact-source-commit "$source_commit" \
  --campaign-source-commit "$source_commit" \
  --output "$output/producer/private" \
  --repetitions "$runs"
artifact_sha=$(python3 - "$output/producer/private/summary.json" <<'PY'
import json, pathlib, sys
print(json.loads(pathlib.Path(sys.argv[1]).read_text())['artifact_sha256'])
PY
)
go run ./research/workflowbench/cmd/project-transparent-campaign \
  --input "$output/producer/private" \
  --expected-artifact "$artifact_sha" \
  --expected-campaign-commit "$source_commit" \
  --json-output "$output/producer/public.json" \
  --svg-output "$output/producer/physical-executions.svg" \
  --flow-svg-output "$output/producer/arrival-flow.svg" \
  --markdown-output "$output/producer/results.md"

printf '==> build numpy-core Guest\n'
AGENT_RUNTIME_ARTIFACT_PROFILE=numpy-core ./guest/build/build-guest.sh
cp dist/agent-python-runtime-numpy-core.wasm "$output/artifacts/numpy-core.wasm"

printf '==> prepared-family private-copy/private-COW economics\n'
AGENT_RUNTIME_GUEST="$output/artifacts/numpy-core.wasm" \
PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT="$source_commit" \
PYSOLATE_PREPARED_FAMILY_SOURCE_TREE="$source_tree" \
PYSOLATE_PREPARED_FAMILY_ECONOMICS_RUNS="$runs" \
PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT="$fanout" \
  scripts/prepared-family-economics-gate.sh "$output/prepared-family/economics.json"

python3 - "$output" "$source_commit" "$source_tree" "$source_epoch" "$runs" "$fanout" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
source_commit, source_tree, source_epoch = sys.argv[2:5]
runs, fanout = map(int, sys.argv[5:7])

def sha(path):
    return 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()

def load(relative):
    return json.loads((root / relative).read_text())

one = load('plm/one-read.json')
four = load('plm/four-read.json')
family = load('prepared-family/economics.json')
producer = load('producer/public.json')
platform = load('platform.json')
expected = {
    'one': 'pysolate.plm-economics.v1',
    'four': 'pysolate.plm-multiread-economics.v1',
    'family': 'pysolate.prepared-family-economics.v1',
    'producer': 'pysolate.transparent-campaign-public-projection.v1',
}
actual = {
    'one': one.get('schema_version'), 'four': four.get('schema_version'),
    'family': family.get('schema_version'), 'producer': producer.get('schema_version'),
}
if actual != expected:
    raise SystemExit(f'evidence schema drift: {actual}')
manifest = {
    'schema_version': 'pysolate.linux-evaluation-suite.v1',
    'source_commit': source_commit,
    'source_tree': source_tree,
    'source_epoch': int(source_epoch),
    'platform': platform,
    'parameters': {'runs_per_arm': runs, 'prepared_family_fanout': fanout},
    'artifacts': {
        'base': {'sha256': sha(root / 'artifacts/base.wasm')},
        'numpy_core': {'sha256': sha(root / 'artifacts/numpy-core.wasm')},
    },
    'evidence': {
        'plm_one_read': {'path': 'plm/one-read.json', 'sha256': sha(root / 'plm/one-read.json')},
        'plm_four_read': {'path': 'plm/four-read.json', 'sha256': sha(root / 'plm/four-read.json')},
        'prepared_family': {'path': 'prepared-family/economics.json', 'sha256': sha(root / 'prepared-family/economics.json')},
        'producer_public': {'path': 'producer/public.json', 'sha256': sha(root / 'producer/public.json')},
        'producer_private_summary': {'path': 'producer/private/summary.json', 'sha256': sha(root / 'producer/private/summary.json')},
    },
}
(root / 'suite-manifest.json').write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
PY

rm -f "$output/artifacts/base.wasm" "$output/artifacts/numpy-core.wasm"
rmdir "$output/artifacts"
printf 'suite_manifest=%s\n' "$output/suite-manifest.json"
