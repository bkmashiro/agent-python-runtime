#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/build-guest-workstation.sh --output ABSOLUTE_DIR [--cache-mode auto|refresh|off]
       [--artifact-profile base|attrs-770] [--extension-patch ABSOLUTE_FILE]
       [--gateway shell2|shell3]

Build the exact clean HEAD on gpu31 via an explicit shell gateway, retrieve a complete evidence bundle,
verify it locally, and clean only this run's remote staging/output directories.
The source/toolchain keyed cache remains under the approved shared cache root.
EOF
}

cache_mode=auto
artifact_profile=base
extension_patch=""
gateway=shell2
output=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --cache-mode) cache_mode=${2:-}; shift 2 ;;
    --artifact-profile) artifact_profile=${2:-}; shift 2 ;;
    --extension-patch) extension_patch=${2:-}; shift 2 ;;
    --gateway) gateway=${2:-}; shift 2 ;;
    --output) output=${2:-}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
case "$cache_mode" in auto|refresh|off) ;; *) usage >&2; exit 2 ;; esac
case "$gateway" in shell2|shell3) ;; *) usage >&2; exit 2 ;; esac
case "$artifact_profile" in
  base)
    if [[ -n $extension_patch ]]; then usage >&2; exit 2; fi
    ;;
  attrs-770)
    if [[ $extension_patch != /* || ! -f $extension_patch || -L $extension_patch ]]; then usage >&2; exit 2; fi
    ;;
  *) usage >&2; exit 2 ;;
esac
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
shared=/vol/bitbucket/ys25/pysolate
stage=""
remote_output=""
cache_root="$shared/cache/guest-layers"

cleanup_remote() {
  [[ -z ${stage:-} ]] || ssh "$gateway" rm -rf -- "$stage" >/dev/null 2>&1 || true
  [[ -z ${remote_output:-} ]] || ssh "$gateway" rm -rf -- "$remote_output" >/dev/null 2>&1 || true
}
trap cleanup_remote EXIT
ssh "$gateway" mkdir -p "$shared/stage" "$shared/artifacts" "$shared/cache"
stage=$(ssh "$gateway" mktemp -d "$shared/stage/workstation-${source_commit:0:12}.XXXXXXXX")
remote_output=$(ssh "$gateway" mktemp -d "$shared/artifacts/workstation-${source_commit:0:12}.XXXXXXXX")
git archive --format=tar HEAD | ssh "$gateway" tar xf - -C "$stage"
remote_patch=""
if [[ $artifact_profile == attrs-770 ]]; then
  ssh "$gateway" mkdir -p "$stage/private"
  scp -q "$extension_patch" "$gateway:$stage/private/extension.patch"
  ssh "$gateway" chmod 0700 "$stage/private"
  ssh "$gateway" chmod 0600 "$stage/private/extension.patch"
  remote_patch="$stage/private/extension.patch"
fi
set +e
# All remote arguments are generated from fixed prefixes, Git hex identities, and an enum.
# shellcheck disable=SC2029
ssh "$gateway" ssh gpu31 env AGENT_RUNTIME_ARTIFACT_PROFILE="$artifact_profile" AGENT_RUNTIME_EXTENSION_PATCH="$remote_patch" \
  bash "$stage/scripts/internal/build-guest-workstation-worker.sh" \
  "$stage" "$remote_output" "$cache_root" "$source_commit" "$source_tree" "$source_epoch" "$cache_mode"
worker_status=$?
set -e
mkdir -p "$output"
ssh "$gateway" tar cf - -C "$remote_output" . | tar xf - -C "$output"
if [[ $worker_status -ne 0 ]]; then
  echo "workstation Guest build failed; evidence retained at $output" >&2
  exit "$worker_status"
fi
python3 scripts/verify-workstation-build.py "$output" --source-commit "$source_commit" --source-tree "$source_tree" \
  --artifact-profile "$artifact_profile"
cleanup_remote
trap - EXIT
printf 'evidence_root=%s\n' "$output"
