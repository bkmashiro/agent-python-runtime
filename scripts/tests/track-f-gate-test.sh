#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
gate="$repo_root/scripts/track-f-gate.sh"

expected=$'focused\nfull\nguest\nlab\nrelease-check'
actual="$($gate --list)"
if [[ "$actual" != "$expected" ]]; then
  printf 'unexpected modes:\n%s\n' "$actual" >&2
  exit 1
fi

if "$gate" unknown >/dev/null 2>&1; then
  echo "unknown mode unexpectedly succeeded" >&2
  exit 1
fi

if AGENT_RUNTIME_GUEST= "$gate" guest >/dev/null 2>&1; then
  echo "guest mode unexpectedly accepted an empty artifact" >&2
  exit 1
fi

printf 'PASS\n'
