#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/test-host-workstation.sh \
  --suite baseline|prepared-family|evaluation|evaluation-sweeps|plm-fixed-cost|source-stream-timing|thesis-experiments \
  --target gpu31|gpu32|gpu33|gpu34|gpu35 --output ABSOLUTE_DIR [--gateway shell2|shell3] \
  [--order-offset N] [--plm-crossover-runs N] [--cow-fanout-runs N] \
  [--build-cache-root ABSOLUTE_REMOTE_DIR]

Stage the exact clean HEAD on one allowlisted workstation, run one bounded suite,
retrieve its evidence bundle, verify it locally, and clean only this run's remote paths.
EOF
}

suite=""
gateway=shell2
target=""
output=""
order_offset=0
plm_crossover_runs=5
cow_fanout_runs=5
build_cache_root=/vol/bitbucket/ys25/pysolate/cache/guest-layers
while [[ $# -gt 0 ]]; do
  case "$1" in
    --suite) suite=${2:-}; shift 2 ;;
    --gateway) gateway=${2:-}; shift 2 ;;
    --target) target=${2:-}; shift 2 ;;
    --output) output=${2:-}; shift 2 ;;
    --order-offset) order_offset=${2:-}; shift 2 ;;
    --plm-crossover-runs|--plm-runs) plm_crossover_runs=${2:-}; shift 2 ;;
    --cow-fanout-runs|--cow-runs) cow_fanout_runs=${2:-}; shift 2 ;;
    --build-cache-root) build_cache_root=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
case "$suite" in baseline|prepared-family|evaluation|evaluation-sweeps|plm-fixed-cost|source-stream-timing|thesis-experiments) ;; *) usage >&2; exit 2 ;; esac
case "$gateway" in shell2|shell3) ;; *) usage >&2; exit 2 ;; esac
case "$target" in gpu31|gpu32|gpu33|gpu34|gpu35) ;; *) echo "--target must be gpu31..gpu35" >&2; exit 2 ;; esac
if [[ -z $output || $output != /* ]]; then
  echo "--output must be an absolute path" >&2
  exit 2
fi
if [[ ! $order_offset =~ ^[0-9]+$ || ! $plm_crossover_runs =~ ^[0-9]+$ || ! $cow_fanout_runs =~ ^[0-9]+$ ||
  $plm_crossover_runs -lt 3 || $plm_crossover_runs -gt 20 || $cow_fanout_runs -lt 3 || $cow_fanout_runs -gt 20 ]]; then
  echo "invalid sweep repetitions or order offset" >&2
  exit 2
fi
if [[ $build_cache_root != /vol/bitbucket/ys25/pysolate/* ]]; then
  echo "--build-cache-root must stay under the approved remote root" >&2
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
if [[ -z $build_cache_root ]]; then
  build_cache_root="$shared/cache/guest-layers"
fi
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
# Arguments are fixed paths, Git identities, bounded integers and allowlisted enums.
# shellcheck disable=SC2029
ssh "$gateway" ssh "$target" bash "$stage/scripts/internal/test-host-workstation-worker.sh" \
  "$stage" "$remote_output" "$source_commit" "$source_tree" "$source_epoch" "$suite" "$target" \
  "$order_offset" "$plm_crossover_runs" "$cow_fanout_runs" "$build_cache_root"
worker_status=$?
set -e
mkdir -p "$output"
ssh "$gateway" tar cf - -C "$remote_output" . | tar xf - -C "$output"
if [[ $worker_status -ne 0 ]]; then
  echo "workstation Host test failed; evidence retained at $output" >&2
  exit "$worker_status"
fi
python3 scripts/verify-workstation-host-test.py "$output" --source-commit "$source_commit" --source-tree "$source_tree" --suite "$suite" --target "$target"
cleanup_remote
trap - EXIT
printf 'evidence_root=%s\n' "$output"
