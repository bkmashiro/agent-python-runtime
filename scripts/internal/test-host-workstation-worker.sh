#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 11 ]]; then
  echo "usage: test-host-workstation-worker.sh STAGE OUTPUT SOURCE_COMMIT SOURCE_TREE SOURCE_EPOCH SUITE TARGET ORDER_OFFSET PLM_RUNS COW_RUNS BUILD_CACHE_ROOT" >&2
  exit 2
fi
stage=$1
output=$2
source_commit=$3
source_tree=$4
source_epoch=$5
suite=$6
target=$7
order_offset=$8
plm_crossover_runs=$9
cow_fanout_runs=${10}
build_cache_root=${11}

case "$target" in gpu31|gpu32|gpu33|gpu34|gpu35) ;; *) echo "invalid target" >&2; exit 3 ;; esac
expected_hostname="${target}.doc.ic.ac.uk"
if [[ $(hostname) != "$expected_hostname" || $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "worker requires expected hostname $expected_hostname on Linux x86_64" >&2
  exit 3
fi
if [[ ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid source identity" >&2
  exit 4
fi
case "$suite" in baseline|prepared-family|evaluation|evaluation-sweeps|plm-fixed-cost|thesis-experiments) ;; *) echo "invalid suite" >&2; exit 5 ;; esac
if [[ ! $order_offset =~ ^[0-9]+$ || ! $plm_crossover_runs =~ ^[0-9]+$ || ! $cow_fanout_runs =~ ^[0-9]+$ ||
  $plm_crossover_runs -lt 3 || $plm_crossover_runs -gt 20 || $cow_fanout_runs -lt 3 || $cow_fanout_runs -gt 20 ]]; then
  echo "invalid sweep parameters" >&2
  exit 5
fi

approved_root=$(realpath -e /vol/bitbucket/ys25/pysolate)
stage_real=$(realpath -e "$stage")
output_real=$(realpath -e "$output")
stage_name=$(basename "$stage_real")
output_name=$(basename "$output_real")
if [[ $approved_root != /vol/bitbucket/ys25/pysolate || $stage_real != "$stage" || $output_real != "$output" ||
  $(dirname "$stage_real") != "$approved_root/stage" || $(dirname "$output_real") != "$approved_root/artifacts" ||
  ! $stage_name =~ ^hosttest-${source_commit:0:12}\.[A-Za-z0-9]{8}$ ||
  ! $output_name =~ ^hosttest-${source_commit:0:12}\.[A-Za-z0-9]{8}$ || -L $stage || -L $output ]]; then
  echo "worker path escaped the approved shared root" >&2
  exit 6
fi
case "$build_cache_root" in "$approved_root"/*) ;; *) echo "build cache escaped approved root" >&2; exit 6 ;; esac

temporary=$(mktemp -d /tmp/ys25-pysolate-host-test.XXXXXXXX)
repository="$temporary/agent-python-runtime"
cleanup() {
  chmod -R u+w "$temporary" 2>/dev/null || true
  rm -rf "$temporary"
}
trap cleanup EXIT
mkdir -p "$repository" "$output" "$approved_root/go/pkg/mod" "$approved_root/go-build-cache" "$approved_root/config" "$build_cache_root"
cp -a "$stage"/. "$repository"/
cd "$repository"

export GOROOT="$approved_root/toolchains/go"
export PATH="$GOROOT/bin:/usr/bin:/bin"
export GOPATH="$approved_root/go"
export GOMODCACHE="$approved_root/go/pkg/mod"
export GOCACHE="$approved_root/go-build-cache"
export XDG_CONFIG_HOME="$approved_root/config"
export GOTELEMETRY=off
export SOURCE_DATE_EPOCH="$source_epoch"

started_ns=$(date +%s%N)
set +e
{
  printf 'source_commit=%s\nsource_tree=%s\nsuite=%s\ntarget=%s\n' "$source_commit" "$source_tree" "$suite" "$target"
  "$GOROOT/bin/go" version
  case "$suite" in
    baseline)
      "$GOROOT/bin/go" test ./runtime/prepareddataset ./runtime/preparedregion ./runtime/workspace ./runtime/subagent -count=1
      "$GOROOT/bin/go" vet ./runtime/prepareddataset ./runtime/preparedregion ./runtime/workspace ./runtime/subagent
      ;;
    prepared-family)
      AGENT_RUNTIME_BUILD_CACHE_ROOT="$build_cache_root" \
      AGENT_RUNTIME_BUILD_CACHE_MODE=auto \
      AGENT_RUNTIME_ARTIFACT_PROFILE=numpy-core \
      GITHUB_SHA="$source_commit" \
      AGENT_RUNTIME_SOURCE_TREE="$source_tree" \
      ./guest/build/build-guest.sh
      export AGENT_RUNTIME_GUEST="$repository/dist/agent-python-runtime-numpy-core.wasm"
      export PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT="$source_commit"
      export PYSOLATE_PREPARED_FAMILY_SOURCE_TREE="$source_tree"
      export PYSOLATE_PREPARED_FAMILY_REPORT="$output/acceptance-report.json"
      "$GOROOT/bin/go" test ./runtime/engine/... ./runtime/numpycodec ./runtime/workspace ./runtime/subagent -count=1
      "$GOROOT/bin/go" test -race ./runtime/engine/... ./runtime/workspace ./runtime/subagent -count=1
      "$GOROOT/bin/go" vet ./runtime/engine/... ./runtime/numpycodec ./runtime/workspace ./runtime/subagent
      ;;
    evaluation)
      ./scripts/run-linux-evaluation-suite.sh \
        --output "$output/evaluation" \
        --source-commit "$source_commit" \
        --source-tree "$source_tree" \
        --source-epoch "$source_epoch" \
        --runs 5 \
        --fanout 4 \
        --build-cache-root "$build_cache_root"
      ;;
    evaluation-sweeps)
      ./scripts/run-linux-evaluation-sweeps.sh \
        --output "$output/evaluation-sweeps" \
        --source-commit "$source_commit" \
        --source-tree "$source_tree" \
        --source-epoch "$source_epoch" \
        --host-id "$target" \
        --order-offset "$order_offset" \
        --plm-crossover-runs "$plm_crossover_runs" \
        --cow-fanout-runs "$cow_fanout_runs" \
        --build-cache-root "$build_cache_root"
      ;;
    plm-fixed-cost)
      ./scripts/run-linux-plm-fixed-cost.sh \
        --output "$output/plm-fixed-cost" \
        --source-commit "$source_commit" \
        --source-tree "$source_tree" \
        --source-epoch "$source_epoch" \
        --host-id "$target" \
        --order-offset "$order_offset" \
        --runs "$plm_crossover_runs" \
        --build-cache-root "$build_cache_root"
      ;;
    thesis-experiments)
      mkdir -p "$output/thesis-experiments"
      AGENT_RUNTIME_BUILD_CACHE_ROOT="$build_cache_root" AGENT_RUNTIME_BUILD_CACHE_MODE=auto AGENT_RUNTIME_ARTIFACT_PROFILE=base \
        GITHUB_SHA="$source_commit" AGENT_RUNTIME_SOURCE_TREE="$source_tree" ./guest/build/build-guest.sh
      AGENT_RUNTIME_GUEST="$repository/dist/agent-python-runtime.wasm" \
        PYSOLATE_PLM_PREFIX_EAGER_OUTPUT="$output/thesis-experiments/plm-prefix-eager.json" \
        PYSOLATE_PLM_PREFIX_EAGER_RUNS="$plm_crossover_runs" PYSOLATE_PLM_PREFIX_EAGER_ORDER_OFFSET="$(( order_offset % 3 ))" \
        PYSOLATE_EXPERIMENT_SOURCE_COMMIT="$source_commit" PYSOLATE_EXPERIMENT_SOURCE_TREE="$source_tree" EVALUATION_HOST_ID="$target" \
        "$GOROOT/bin/go" test ./integration/e2e -run '^TestPLMPrefixEagerEconomicsFixture$' -count=1 -timeout=30m
      test -s "$output/thesis-experiments/plm-prefix-eager.json"
      AGENT_RUNTIME_BUILD_CACHE_ROOT="$build_cache_root" AGENT_RUNTIME_BUILD_CACHE_MODE=auto AGENT_RUNTIME_ARTIFACT_PROFILE=numpy-core \
        GITHUB_SHA="$source_commit" AGENT_RUNTIME_SOURCE_TREE="$source_tree" ./guest/build/build-guest.sh
      AGENT_RUNTIME_GUEST="$repository/dist/agent-python-runtime-numpy-core.wasm" \
        PYSOLATE_COW_MUTATION_DENSITY_OUTPUT="$output/thesis-experiments/cow-mutation-density.json" \
        PYSOLATE_COW_MUTATION_DENSITY_RUNS="$cow_fanout_runs" PYSOLATE_COW_MUTATION_DENSITY_ORDER_OFFSET="$(( order_offset % 2 ))" \
        PYSOLATE_EXPERIMENT_SOURCE_COMMIT="$source_commit" PYSOLATE_EXPERIMENT_SOURCE_TREE="$source_tree" EVALUATION_HOST_ID="$target" \
        "$GOROOT/bin/go" test ./runtime/engine/wazero -run '^TestCOWMutationDensityEconomicsFixture$' -count=1 -timeout=2h
      test -s "$output/thesis-experiments/cow-mutation-density.json"
      ;;
  esac
} >"$output/test.log" 2>&1
test_status=$?
set -e
finished_ns=$(date +%s%N)
duration_millis=$(( (finished_ns - started_ns) / 1000000 ))
passed=false
if [[ $test_status -eq 0 ]]; then passed=true; fi

python3 - "$output/RESULT.READY" "$source_commit" "$source_tree" "$suite" "$passed" "$duration_millis" "$GOROOT/bin/go" <<'PY'
import json
import pathlib
import socket
import subprocess
import sys

output, source_commit, source_tree, suite, passed, duration_millis, go = sys.argv[1:]
go_version = subprocess.check_output([go, "env", "GOVERSION"], text=True).strip()
payload = {
    "schema_version": "pysolate.workstation-host-test.v2",
    "source_commit": source_commit,
    "source_tree": source_tree,
    "builder": socket.gethostname(),
    "target": "linux/amd64",
    "suite": suite,
    "passed": passed == "true",
    "go_version": go_version,
    "duration_millis": int(duration_millis),
    "acceptance_report": suite == "prepared-family",
}
pathlib.Path(output).write_text(json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n")
PY
(
  cd "$output"
  evidence=(RESULT.READY test.log)
  if [[ -f acceptance-report.json ]]; then evidence+=(acceptance-report.json); fi
  evidence_dir=""
  if [[ $suite == evaluation ]]; then evidence_dir=evaluation; fi
  if [[ $suite == evaluation-sweeps ]]; then evidence_dir=evaluation-sweeps; fi
  if [[ $suite == plm-fixed-cost ]]; then evidence_dir=plm-fixed-cost; fi
  if [[ $suite == thesis-experiments ]]; then evidence_dir=thesis-experiments; fi
  if [[ -n $evidence_dir ]]; then
    mapfile -d '' nested_files < <(python3 - "$evidence_dir" <<'PY'
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
for path in sorted(p for p in root.rglob("*") if p.is_file()):
    sys.stdout.buffer.write(path.as_posix().encode() + b"\0")
PY
    )
    evidence+=("${nested_files[@]}")
  fi
  sha256sum "${evidence[@]}" > SHA256SUMS
)
exit "$test_status"
