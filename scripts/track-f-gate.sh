#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

usage() {
  cat <<'EOF'
usage: scripts/track-f-gate.sh MODE

modes:
  focused       Track F Go package tests
  full          full Go tests/build, focused race, and vet
  guest         Guest Python checks and real-Guest semantic/reuse E2E
  lab           Lab projection tests and production web build
  release-check diff/signature/upstream/clean-tree verification
EOF
}

list_modes() {
  printf '%s\n' focused full guest lab release-check
}

mode="${1:-}"
case "$mode" in
  --list)
    list_modes
    ;;
  focused)
    go test -count=1 \
      ./runtime/semantic \
      ./runtime/semanticreuse \
      ./runtime/agentfunction \
      ./research/effectgraph \
      ./research/effectgraph/cmd/effectgraph-census \
      ./research/regioncensus \
      ./research/labview
    ;;
  full)
    go test ./... -count=1
    go build ./...
    go test -race -count=1 \
      ./runtime/semantic \
      ./runtime/semanticreuse \
      ./runtime/agentfunction \
      ./research/effectgraph \
      ./research/effectgraph/cmd/effectgraph-census \
      ./research/regioncensus \
      ./research/labview \
      ./integration/e2e
    go vet ./...
    ;;
  guest)
    artifact="${AGENT_RUNTIME_GUEST:-}"
    if [[ -z "$artifact" || ! -f "$artifact" ]]; then
      echo "AGENT_RUNTIME_GUEST must name an existing Guest artifact" >&2
      exit 2
    fi
    PYTHONPATH=guest/bootstrap python3 -m unittest discover -s guest/tests -p 'test_*.py'
    python3 -m compileall -q guest/bootstrap/agent_runtime
    AGENT_RUNTIME_GUEST="$artifact" go test ./integration/e2e \
      -run 'TestRealGuest(SemanticReuse|SemanticPreDispatch|SharedLegality|SemanticAnalysis|SemanticOverlay)' \
      -count=1 -v
    ;;
  lab)
    go test ./research/labview -count=1
    npm --prefix apps/lab-web test -- --run
    npm --prefix apps/lab-web run build
    ;;
  release-check)
    git diff --check
    git diff --cached --check
    go run ./research/effectgraph/cmd/effectgraph-census -verify-bundle
    if [[ -n "$(git status --porcelain)" ]]; then
      echo "worktree is not clean" >&2
      exit 3
    fi
    upstream="$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}')"
    if [[ "$(git rev-parse HEAD)" != "$(git rev-parse "$upstream")" ]]; then
      echo "HEAD does not match $upstream" >&2
      exit 4
    fi
    if [[ "$(git log -1 --format='%G?')" != "G" ]]; then
      echo "HEAD does not have a good signature" >&2
      exit 5
    fi
    printf 'HEAD=%s\nUPSTREAM=%s\nSTATUS=clean\n' \
      "$(git rev-parse HEAD)" "$(git rev-parse "$upstream")"
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
