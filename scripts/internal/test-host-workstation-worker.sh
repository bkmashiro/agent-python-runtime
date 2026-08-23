#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 6 ]]; then
  echo "usage: test-host-workstation-worker.sh STAGE OUTPUT SOURCE_COMMIT SOURCE_TREE SOURCE_EPOCH SUITE" >&2
  exit 2
fi
stage=$1
output=$2
source_commit=$3
source_tree=$4
source_epoch=$5
suite=$6

if [[ $(hostname) != gpu31.doc.ic.ac.uk || $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "worker requires gpu31 Linux x86_64" >&2
  exit 3
fi
if [[ ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid source identity" >&2
  exit 4
fi
case "$suite" in baseline|prepared-family) ;; *) echo "invalid suite" >&2; exit 5 ;; esac

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

temporary=$(mktemp -d /tmp/ys25-pysolate-host-test.XXXXXXXX)
repository="$temporary/agent-python-runtime"
cleanup() {
  chmod -R u+w "$temporary" 2>/dev/null || true
  rm -rf "$temporary"
}
trap cleanup EXIT
mkdir -p "$repository" "$output" "$approved_root/go/pkg/mod" "$approved_root/go-build-cache" "$approved_root/config"
cp -a "$stage"/. "$repository"/
cd "$repository"

export GOROOT="/vol/bitbucket/ys25/pysolate/toolchains/go"
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
  printf 'source_commit=%s\nsource_tree=%s\nsuite=%s\n' "$source_commit" "$source_tree" "$suite"
  "$GOROOT/bin/go" version
  case "$suite" in
    baseline)
      "$GOROOT/bin/go" test ./runtime/prepareddataset ./runtime/preparedregion ./runtime/workspace ./runtime/subagent -count=1
      "$GOROOT/bin/go" vet ./runtime/prepareddataset ./runtime/preparedregion ./runtime/workspace ./runtime/subagent
      ;;
    prepared-family)
      AGENT_RUNTIME_BUILD_CACHE_ROOT="$approved_root/cache/guest-layers" \
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
    "schema_version": "pysolate.workstation-host-test.v1",
    "source_commit": source_commit,
    "source_tree": source_tree,
    "builder": socket.gethostname(),
    "target": "linux/amd64",
    "suite": suite,
    "passed": passed == "true",
    "go_version": go_version,
    "duration_millis": int(duration_millis),
}
pathlib.Path(output).write_text(json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n")
PY
(
  cd "$output"
  evidence=(RESULT.READY test.log)
  if [[ -f acceptance-report.json ]]; then evidence+=(acceptance-report.json); fi
  sha256sum "${evidence[@]}" > SHA256SUMS
)
exit "$test_status"
