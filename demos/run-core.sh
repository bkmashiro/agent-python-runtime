#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"

printf '\nPysolate core path\n'
printf '%s\n' 'Watch Pysolate move from one approved Tool, to file continuity, to a hot HTTP service, safe early reads, and deterministic replay.'

printf '\n[1/5] Approved Host Tool in ordinary Python\n'
"$DEMO_DIR/02-namespaced-tools.sh"

printf '\n[2/5] Host-owned workspace across disposable Guests\n'
"$DEMO_DIR/17-workspace-continuation.sh"

printf '\n[3/5] Hot local HTTP service and workspace lifecycle\n'
"$DEMO_DIR/04-hot-service.sh"

printf '\n[4/5] Overlap approved independent reads\n'
"$DEMO_DIR/16-fullcode-overlap.sh"

printf '\n[5/5] Deterministic Tool-outcome replay\n'
PYSOLATE_REPLAY_CONFORMANCE=0 "$DEMO_DIR/15-deterministic-replay.sh"
