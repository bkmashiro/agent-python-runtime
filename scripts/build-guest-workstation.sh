#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/build-guest-workstation.sh --output ABSOLUTE_DIR [--cache-mode auto|refresh|off]

Build the exact clean HEAD on gpu31 via shell2, retrieve a complete evidence bundle,
verify it locally, and clean only this run's remote staging/output directories.
The source/toolchain keyed cache remains under the approved shared cache root.
EOF
}

cache_mode=auto
output=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --cache-mode) cache_mode=${2:-}; shift 2 ;;
    --output) output=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
case "$cache_mode" in auto|refresh|off) ;; *) usage >&2; exit 2 ;; esac
if [[ -z $output || $output != /* ]]; then
  echo "--output must be an absolute path" >&2
  exit 2
fi
if [[ -e $output && ( ! -d $output || -L $output || -n $(find "$output" -mindepth 1 -maxdepth 1 -print -quit) ) ]]; then
  echo "output must be absent or an empty real directory" >&2
  exit 3
fi

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
if [[ -n $(git status --porcelain) ]]; then
  echo "workstation build requires a clean tree so git archive and source identity agree" >&2
  exit 4
fi
source_commit=$(git rev-parse HEAD)
source_tree=$(git rev-parse 'HEAD^{tree}')
source_epoch=$(git show -s --format=%ct HEAD)
run_id="workstation-${source_commit:0:12}-$$"
shared=/vol/bitbucket/ys25/pysolate
stage="$shared/stage/$run_id"
remote_output="$shared/artifacts/$run_id"
cache_root="$shared/cache/guest-layers"

cleanup_remote() {
  ssh shell2 rm -rf -- "$stage" "$remote_output" >/dev/null 2>&1 || true
}
trap cleanup_remote EXIT
ssh shell2 mkdir -p "$stage" "$remote_output"
git archive --format=tar HEAD | ssh shell2 tar xf - -C "$stage"
set +e
# All remote arguments are generated from fixed prefixes, Git hex identities, and an enum.
# shellcheck disable=SC2029
ssh shell2 ssh gpu31 bash "$stage/scripts/internal/build-guest-workstation-worker.sh" \
  "$stage" "$remote_output" "$cache_root" "$source_commit" "$source_tree" "$source_epoch" "$cache_mode"
worker_status=$?
set -e
mkdir -p "$output"
ssh shell2 tar cf - -C "$remote_output" . | tar xf - -C "$output"
if [[ $worker_status -ne 0 ]]; then
  echo "workstation Guest build failed; evidence retained at $output" >&2
  exit "$worker_status"
fi
python3 scripts/verify-workstation-build.py "$output" --source-commit "$source_commit"
cleanup_remote
trap - EXIT
printf 'evidence_root=%s\n' "$output"
