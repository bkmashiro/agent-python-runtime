#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 || ! $1 =~ ^[0-9a-f]{40}$ || ! $2 =~ ^[0-9a-f]{40}$ || ! $3 =~ ^[1-9][0-9]*$ ]]; then
  echo "usage: verify-source-identity.sh COMMIT TREE EPOCH" >&2
  exit 2
fi
expected_commit=$1
expected_tree=$2
expected_epoch=$3
root=$(pwd -P)

if git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if [[ $(git -C "$root" rev-parse --show-toplevel) != "$root" || -n $(git -C "$root" status --porcelain) ]]; then
    echo "source worktree must be clean and rooted at the evaluation directory" >&2
    exit 3
  fi
  actual_commit=$(git -C "$root" rev-parse HEAD)
  actual_tree=$(git -C "$root" rev-parse 'HEAD^{tree}')
  actual_epoch=$(git -C "$root" show -s --format=%ct HEAD)
  if [[ $actual_commit != "$expected_commit" ]]; then
    echo "source commit mismatch: got $actual_commit want $expected_commit" >&2
    exit 4
  fi
  if [[ $actual_epoch != "$expected_epoch" ]]; then
    echo "source epoch mismatch: got $actual_epoch want $expected_epoch" >&2
    exit 4
  fi
else
  temporary=$(mktemp -d)
  trap 'rm -rf "$temporary"' EXIT
  git --git-dir="$temporary/repository.git" init --bare -q
  GIT_DIR="$temporary/repository.git" GIT_WORK_TREE="$root" GIT_INDEX_FILE="$temporary/index" git add -A
  actual_tree=$(GIT_DIR="$temporary/repository.git" GIT_WORK_TREE="$root" GIT_INDEX_FILE="$temporary/index" git write-tree)
fi

if [[ $actual_tree != "$expected_tree" ]]; then
  echo "source tree mismatch: got $actual_tree want $expected_tree" >&2
  exit 4
fi
