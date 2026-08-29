#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' 'usage: scripts/run-linux-plm-fixed-cost.sh --output ABSOLUTE_DIR --source-commit HEX40 --source-tree HEX40 --source-epoch SECONDS --host-id gpu31|gpu32|gpu33|gpu34|gpu35 --order-offset N --runs N [--build-cache-root ABSOLUTE_DIR]'
}

output=""
source_commit=""
source_tree=""
source_epoch=""
host_id=""
order_offset=""
runs=""
build_cache_root=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output=${2:-}; shift 2 ;;
    --source-commit) source_commit=${2:-}; shift 2 ;;
    --source-tree) source_tree=${2:-}; shift 2 ;;
    --source-epoch) source_epoch=${2:-}; shift 2 ;;
    --host-id) host_id=${2:-}; shift 2 ;;
    --order-offset) order_offset=${2:-}; shift 2 ;;
    --runs) runs=${2:-}; shift 2 ;;
    --build-cache-root) build_cache_root=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "PLM fixed-cost evaluation requires Linux x86_64" >&2
  exit 3
fi
if [[ $output != /* || ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid output or source identity" >&2
  exit 4
fi
if [[ ! $host_id =~ ^gpu3[1-5]$ || ! $order_offset =~ ^[0-9]+$ || ! $runs =~ ^[0-9]+$ ]] || (( runs < 3 || runs > 20 )); then
  echo "invalid host, order offset or runs" >&2
  exit 4
fi
if [[ -n $build_cache_root && $build_cache_root != /* ]]; then
  echo "--build-cache-root must be absolute" >&2
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

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
scripts/verify-source-identity.sh "$source_commit" "$source_tree" "$source_epoch"
export SOURCE_DATE_EPOCH="$source_epoch"
export GITHUB_SHA="$source_commit"
export AGENT_RUNTIME_SOURCE_TREE="$source_tree"
export EVALUATION_HOST_ID="$host_id"
export EVALUATION_ORDER_OFFSET="$order_offset"
if [[ -n $build_cache_root ]]; then
  mkdir -p "$build_cache_root"
  export AGENT_RUNTIME_BUILD_CACHE_ROOT="$build_cache_root"
  export AGENT_RUNTIME_BUILD_CACHE_MODE=auto
fi

mkdir -p "$output/artifacts"
AGENT_RUNTIME_ARTIFACT_PROFILE=base ./guest/build/build-guest.sh
cp dist/agent-python-runtime.wasm "$output/artifacts/base.wasm"

python3 - "$output/platform.json" "$source_commit" "$source_tree" "$source_epoch" "$host_id" "$order_offset" "$runs" <<'PY'
import json, os, pathlib, platform, socket, sys
path, commit, tree, epoch, host_id, offset, runs = sys.argv[1:]
logical_cpus = os.cpu_count() or 1
payload = {
    "schema_version": "pysolate.plm-fixed-cost-platform.v1",
    "hostname": socket.gethostname(),
    "architecture": platform.machine(),
    "kernel": platform.release(),
    "logical_cpus": logical_cpus,
    "load_1m": os.getloadavg()[0],
    "source_commit": commit,
    "source_tree": tree,
    "source_epoch": int(epoch),
    "host_id": host_id,
    "order_offset": int(offset),
    "zero_read_runs": int(runs),
}
pathlib.Path(path).write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
PY

PLM_TARGET_COMMIT="$source_commit" \
PLM_SOURCE_TREE="$source_tree" \
PLM_ARTIFACT_SOURCE_COMMIT="$source_commit" \
AGENT_RUNTIME_GUEST="$output/artifacts/base.wasm" \
PLM_CROSSOVER_RUNS=3 \
PLM_CROSSOVER_ZERO_READ_RUNS="$runs" \
PLM_CROSSOVER_ZERO_READ=1 \
PLM_CROSSOVER_ZERO_ONLY=1 \
EVALUATION_HOST_ID="$host_id" \
EVALUATION_ORDER_OFFSET="$order_offset" \
PLM_CROSSOVER_GO_TEST_TIMEOUT=1h \
  scripts/plm-crossover-economics-gate.sh "$output/plm-fixed-cost.json"

python3 - "$output" "$source_commit" "$source_tree" "$source_epoch" "$host_id" "$order_offset" "$runs" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
commit, tree, epoch, host_id, offset, runs_text = sys.argv[2:]
runs = int(runs_text)
evidence_path = root / "plm-fixed-cost.json"
artifact_path = root / "artifacts/base.wasm"
platform_path = root / "platform.json"
evidence = json.loads(evidence_path.read_text())
if evidence.get("schema_version") != "pysolate.plm-crossover-economics.v1":
    raise SystemExit("PLM evidence schema drift")
for key, expected in {
    "target_commit": commit,
    "source_tree": tree,
    "artifact_source_commit": commit,
    "evaluation_host_id": host_id,
    "evaluation_order_offset": int(offset),
    "zero_read_runs": runs,
    "zero_read": True,
    "zero_only": True,
    "read_counts": [],
    "delays_ms": [],
}.items():
    if evidence.get(key) != expected:
        raise SystemExit(f"PLM fixed-cost field drift: {key}")
profiles = evidence.get("profiles")
if not isinstance(profiles, list) or [p.get("name") for p in profiles] != ["cold_end_to_end", "engine_precompiled"]:
    raise SystemExit("PLM fixed-cost profiles drift")
source_hashes = set()
for profile in profiles:
    samples = profile.get("samples")
    comparisons = profile.get("comparisons")
    if not isinstance(samples, list) or len(samples) != 2 * runs or not isinstance(comparisons, list) or len(comparisons) != 1:
        raise SystemExit("PLM fixed-cost sample count drift")
    observed = {(sample.get("pair_iteration"), sample.get("mode")) for sample in samples}
    expected = {(pair, mode) for pair in range(runs) for mode in ("baseline", "plm")}
    if observed != expected:
        raise SystemExit("PLM fixed-cost pair identity drift")
    for sample in samples:
        if sample.get("profile") != profile["name"] or sample.get("read_count") != 0 or sample.get("delay_ms") != 0:
            raise SystemExit("PLM fixed-cost cell drift")
        if sample.get("provider_starts") != 0 or sample.get("provider_max_concurrency") != 0 or sample.get("call_count") != 0 or sample.get("result") != [750]:
            raise SystemExit("PLM fixed-cost semantic drift")
        source_hashes.add(sample.get("source_sha256"))
if len(source_hashes) != 1 or evidence.get("source_sha256") not in source_hashes:
    raise SystemExit("PLM fixed-cost source digest drift")
def sha(path):
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return "sha256:" + digest.hexdigest()
if evidence.get("artifact_sha256") != sha(artifact_path):
    raise SystemExit("PLM fixed-cost artifact digest drift")
manifest = {
    "schema_version": "pysolate.plm-fixed-cost-host.v1",
    "source": {"commit": commit, "tree": tree, "epoch": int(epoch)},
    "host_id": host_id,
    "order_offset": int(offset),
    "zero_read_runs": runs,
    "profiles": ["cold_end_to_end", "engine_precompiled"],
    "evidence_sha256": sha(evidence_path),
    "artifact_sha256": sha(artifact_path),
    "platform_sha256": sha(platform_path),
}
(root / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
PY
(
  cd "$output"
  find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
)
printf 'fixed_cost_manifest=%s\n' "$output/manifest.json"
