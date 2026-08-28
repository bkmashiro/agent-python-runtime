#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
MODE=${1:-focused}
PYTHON_BIN=${PYTHON_BIN:-python3}
cd "${ROOT_DIR}"

case "${MODE}" in
  focused)
    go test ./runtime/capability ./runtime/semantic ./runtime/sourcepatch ./runtime/passplugin ./runtime/engine/wazero -count=1
    PYTHONPATH=guest/bootstrap "${PYTHON_BIN}" -m unittest \
      guest.tests.test_source_pass \
      guest.tests.test_bootstrap \
      guest.tests.test_source_contract \
      guest.tests.test_artifact_contract
    ;;
  race)
    go test -race ./runtime/capability ./runtime/semantic ./runtime/sourcepatch ./runtime/passplugin ./runtime/engine/wazero -count=1
    ;;
  full)
    git diff --check
    go test ./... -count=1
    go vet ./...
    PYTHONPATH=guest/bootstrap "${PYTHON_BIN}" -m unittest discover -s guest/tests -p 'test_*.py'
    "${PYTHON_BIN}" -m unittest discover -s scripts/tests -p 'test_*.py'
    ;;
  guest)
    if [[ -z ${AGENT_RUNTIME_GUEST:-} || ! -s ${AGENT_RUNTIME_GUEST} ]]; then
      echo "guest mode requires AGENT_RUNTIME_GUEST to name an existing non-empty artifact" >&2
      exit 2
    fi
    go test ./integration/e2e -run 'TestRealGuestUnifiedSplitPhase' -count=1 -v
    ;;
  *)
    echo "usage: $0 {focused|race|full|guest}" >&2
    exit 2
    ;;
esac
