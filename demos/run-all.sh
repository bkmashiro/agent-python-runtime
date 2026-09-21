#!/usr/bin/env bash

set -euo pipefail

DEMO_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"

for demo in \
  "$DEMO_DIR/01-basic-python.sh" \
  "$DEMO_DIR/02-namespaced-tools.sh" \
  "$DEMO_DIR/03-workspace-edit.sh" \
  "$DEMO_DIR/04-hot-service.sh" \
  "$DEMO_DIR/05-corpus-replay.sh"; do
  "$demo"
done
