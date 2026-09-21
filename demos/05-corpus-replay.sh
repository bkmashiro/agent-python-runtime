#!/usr/bin/env bash

set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
GUEST="${PYSOLATE_GUEST:-$ROOT/dist/pysolate.wasm}"

printf '\n== 05: deterministic corpus replay ==\n'
printf 'Frozen source + exact namespaced Host-tool fixture, replayed twice on one prepared Runner.\n\n'

cd "$ROOT"
go run ./cmd/pysolate-corpus \
  -guest "$GUEST" \
  -corpus examples/corpus/demo.jsonl \
  -prepared copy \
  -iterations 2
