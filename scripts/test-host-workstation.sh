#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/test-host-workstation.sh --suite baseline|prepared-family --output ABSOLUTE_DIR [--gateway shell2|shell3]

Stage the exact clean HEAD on gpu31, run one allowlisted Host-test suite, retrieve a bounded evidence bundle,
verify it locally, and clean only this run's remote staging/output directories.
EOF
}

suite=""
gateway=shell2
output=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --suite) suite=${2:-}; shift 2 ;;
    --gateway) gateway=${2:-}; shift 2 ;;
    --output) output=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
case "$suite" in baseline|prepared-family) ;; *) usage >&2; exit 2 ;; esac
case "$gateway" in shell2|shell3) ;; *) usage >&2; exit 2 ;; esac
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
  echo "workstation Host test requires a clean tree so git archive and source identity agree" >&2
  exit 4
fi
source_commit=$(git rev-parse HEAD)
source_tree=$(git rev-parse 'HEAD^{tree}')
source_epoch=$(git show -s --format=%ct HEAD)
shared=/vol/bitbucket/ys25/pysolate
stage=""
remote_output=""

cleanup_remote() {
  [[ -z ${stage:-} ]] || ssh "$gateway" rm -rf -- "$stage" >/dev/null 2>&1 || true
  [[ -z ${remote_output:-} ]] || ssh "$gateway" rm -rf -- "$remote_output" >/dev/null 2>&1 || true
}
trap cleanup_remote EXIT
ssh "$gateway" mkdir -p "$shared/stage" "$shared/artifacts"
stage=$(ssh "$gateway" mktemp -d "$shared/stage/hosttest-${source_commit:0:12}.XXXXXXXX")
remote_output=$(ssh "$gateway" mktemp -d "$shared/artifacts/hosttest-${source_commit:0:12}.XXXXXXXX")
git archive --format=tar HEAD | ssh "$gateway" tar xf - -C "$stage"
set +e
# Arguments are fixed paths, Git hex identities, a numeric epoch and an enum.
# shellcheck disable=SC2029
ssh "$gateway" ssh gpu31 bash "$stage/scripts/internal/test-host-workstation-worker.sh" \
  "$stage" "$remote_output" "$source_commit" "$source_tree" "$source_epoch" "$suite"
worker_status=$?
set -e
mkdir -p "$output"
ssh "$gateway" tar cf - -C "$remote_output" . | tar xf - -C "$output"
if [[ $worker_status -ne 0 ]]; then
  echo "workstation Host test failed; evidence retained at $output" >&2
  exit "$worker_status"
fi
python3 scripts/verify-workstation-host-test.py "$output" --source-commit "$source_commit" --source-tree "$source_tree" --suite "$suite"
cleanup_remote
trap - EXIT
printf 'evidence_root=%s\n' "$output"
