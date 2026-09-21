#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(CDPATH='' cd -- "$DEMO_DIR/.." && pwd)"
GUEST="${PYSOLATE_GUEST:-$REPO_ROOT/dist/pysolate.wasm}"

cd "$REPO_ROOT"

banner() {
  printf '\n\033[1;36m==> %s\033[0m\n' "$1"
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

require_guest() {
  if [[ ! -f "$GUEST" ]]; then
    printf 'Guest artifact not found: %s\n' "$GUEST" >&2
    printf 'Set PYSOLATE_GUEST=/path/to/pysolate.wasm if it lives elsewhere.\n' >&2
    exit 1
  fi
}

pretty_json() {
  python3 -m json.tool
}

require_command go
require_command python3
require_guest
