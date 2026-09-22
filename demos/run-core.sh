#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"

for demo in \
  "$DEMO_DIR/02-namespaced-tools.sh" \
  "$DEMO_DIR/17-workspace-continuation.sh" \
  "$DEMO_DIR/04-hot-service.sh"; do
  "$demo"
done

PYSOLATE_REPLAY_CONFORMANCE=0 "$DEMO_DIR/15-deterministic-replay.sh"
