#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 7 ]]; then
  echo "usage: build-guest-workstation-worker.sh STAGE OUTPUT CACHE SOURCE_COMMIT SOURCE_TREE SOURCE_EPOCH CACHE_MODE" >&2
  exit 2
fi
stage=$1
output=$2
cache_root=$3
source_commit=$4
source_tree=$5
source_epoch=$6
cache_mode=$7

if [[ $(hostname) != gpu31.doc.ic.ac.uk || $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "worker requires gpu31 Linux x86_64" >&2
  exit 3
fi
for value in "$stage" "$output" "$cache_root"; do
  if [[ $value != /vol/bitbucket/ys25/pysolate/* ]]; then
    echo "worker path escaped the approved shared root" >&2
    exit 4
  fi
done
if [[ ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid source identity" >&2
  exit 5
fi
case "$cache_mode" in off|auto|refresh) ;; *) exit 6 ;; esac

temporary=$(mktemp -d /tmp/ys25-pysolate-workstation-build.XXXXXXXX)
repository="$temporary/agent-python-runtime"
cleanup() {
  chmod -R u+w "$temporary" 2>/dev/null || true
  rm -rf "$temporary"
}
trap cleanup EXIT
mkdir -p "$repository" "$output"
cp -a "$stage"/. "$repository"/
cd "$repository"

started_ns=$(date +%s%N)
set +e
AGENT_RUNTIME_BUILD_CACHE_ROOT="$cache_root" \
AGENT_RUNTIME_BUILD_CACHE_MODE="$cache_mode" \
GITHUB_SHA="$source_commit" \
SOURCE_DATE_EPOCH="$source_epoch" \
./guest/build/build-guest.sh >"$output/build.log" 2>&1
build_status=$?
set -e
finished_ns=$(date +%s%N)
if [[ $build_status -ne 0 ]]; then
  printf 'build failed with status %d\n' "$build_status" >&2
  exit "$build_status"
fi

rm -rf "$output/dist"
cp -a dist "$output/dist"
build_millis=$(( (finished_ns - started_ns) / 1000000 ))
python3 - "$output/RESULT.READY" "$source_commit" "$source_tree" "$build_millis" "$cache_mode" "$output/dist/build-cache.json" <<'PY'
import json
import pathlib
import socket
import sys

output, source_commit, source_tree, build_millis, requested_mode, cache_path = sys.argv[1:]
cache = json.loads(pathlib.Path(cache_path).read_text())
payload = {
    "schema_version": "pysolate.workstation-guest-build.v0",
    "source_commit": source_commit,
    "source_tree": source_tree,
    "builder": socket.gethostname(),
    "target": "wasm32-wasip1",
    "requested_cache_mode": requested_mode,
    "cache_key": cache["cache_key"],
    "cache_disposition": cache["disposition"],
    "cache_layer_sha256": cache["layer_sha256"],
    "build_millis": int(build_millis),
}
pathlib.Path(output).write_text(json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n")
PY
(
  cd "$output"
  mapfile -t files < <(find dist -type f -print | LC_ALL=C sort)
  sha256sum RESULT.READY build.log "${files[@]}" > SHA256SUMS
)
printf 'artifact_sha256=sha256:%s\n' "$(sha256sum "$output/dist/agent-python-runtime.wasm" | cut -d' ' -f1)"
printf 'cache_disposition=%s\n' "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["disposition"])' "$output/dist/build-cache.json")"
printf 'build_millis=%s\n' "$build_millis"
