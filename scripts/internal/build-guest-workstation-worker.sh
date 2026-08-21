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
if [[ ! $source_commit =~ ^[0-9a-f]{40}$ || ! $source_tree =~ ^[0-9a-f]{40}$ || ! $source_epoch =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid source identity" >&2
  exit 5
fi
approved_root=$(realpath -e /vol/bitbucket/ys25/pysolate)
stage_real=$(realpath -e "$stage")
output_real=$(realpath -e "$output")
cache_parent=$(dirname "$cache_root")
cache_parent_real=$(realpath -e "$cache_parent")
stage_name=$(basename "$stage_real")
output_name=$(basename "$output_real")
if [[ $approved_root != /vol/bitbucket/ys25/pysolate || $stage_real != "$stage" || $output_real != "$output" ||
  $cache_parent_real != "$approved_root/cache" || $cache_root != "$approved_root/cache/guest-layers" ||
  $(dirname "$stage_real") != "$approved_root/stage" || $(dirname "$output_real") != "$approved_root/artifacts" ||
  ! $stage_name =~ ^workstation-${source_commit:0:12}\.[A-Za-z0-9]{8}$ ||
  ! $output_name =~ ^workstation-${source_commit:0:12}\.[A-Za-z0-9]{8}$ ||
  -L $stage || -L $output ]]; then
  echo "worker path escaped the approved shared root" >&2
  exit 4
fi
shared_root=$approved_root
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
mkdir -p "$shared_root/go/pkg/mod" "$shared_root/go-build-cache" "$shared_root/config"
set +e
AGENT_RUNTIME_BUILD_CACHE_ROOT="$cache_root" \
AGENT_RUNTIME_BUILD_CACHE_MODE="$cache_mode" \
GITHUB_SHA="$source_commit" \
AGENT_RUNTIME_SOURCE_TREE="$source_tree" \
SOURCE_DATE_EPOCH="$source_epoch" \
GOPATH="$shared_root/go" \
GOMODCACHE="$shared_root/go/pkg/mod" \
GOCACHE="$shared_root/go-build-cache" \
XDG_CONFIG_HOME="$shared_root/config" \
GOTELEMETRY=off \
./guest/build/build-guest.sh >"$output/build.log" 2>&1
build_status=$?
set -e
finished_ns=$(date +%s%N)
if [[ $build_status -ne 0 ]]; then
  rm -rf "$output/failed-debug"
  mkdir -p "$output/failed-debug"
  if [[ -d dist ]]; then
    cp -a dist "$output/failed-debug/dist"
  fi
  if [[ -d build/guest/import-inventory ]]; then
    cp -a build/guest/import-inventory "$output/failed-debug/import-inventory"
  fi
  if [[ -d build/guest/import-qualification ]]; then
    cp -a build/guest/import-qualification "$output/failed-debug/import-qualification"
  fi
  printf '{"schema_version":"pysolate.failed-workstation-build.v1","publishable":false,"build_status":%d}\n' "$build_status" > "$output/failed-debug/FAILURE.json"
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
    "final_cache_key": cache["final_cache_key"],
    "final_cache_disposition": cache["final_cache_disposition"],
    "build_millis": int(build_millis),
}
pathlib.Path(output).write_text(json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n")
PY
(
  cd "$output"
  mapfile -t files < <(find dist -type f -print | LC_ALL=C sort)
  sha256sum RESULT.READY build.log "${files[@]}" > SHA256SUMS
)
artifact_filename=$(python3 -c 'import json,pathlib,sys; name=json.load(open(sys.argv[1]))["artifact"]["filename"]; path=pathlib.PurePosixPath(name); (path.name == name and not path.is_absolute()) or sys.exit("invalid artifact filename"); print(name)' "$output/dist/manifest.json")
printf 'artifact_sha256=sha256:%s\n' "$(sha256sum "$output/dist/$artifact_filename" | cut -d' ' -f1)"
printf 'cache_disposition=%s\n' "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["disposition"])' "$output/dist/build-cache.json")"
printf 'build_millis=%s\n' "$build_millis"
