#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ARTIFACT=${AGENT_RUNTIME_GUEST:-}
ARTIFACT_SOURCE_COMMIT=${PLM_ARTIFACT_SOURCE_COMMIT:-}
RUNS=${PLM_ECONOMICS_RUNS:-5}
OUTPUT=${1:-"${ROOT_DIR}/docs/evidence/plm-v1-multiread-economics.json"}

if [[ "${OUTPUT}" != /* ]]; then
  OUTPUT="${ROOT_DIR}/${OUTPUT}"
fi
if [[ -z "${ARTIFACT}" || ! -f "${ARTIFACT}" ]]; then
  echo "AGENT_RUNTIME_GUEST must point to the exact CPython/WASI artifact" >&2
  exit 2
fi
if [[ -z "${ARTIFACT_SOURCE_COMMIT}" ]]; then
  echo "PLM_ARTIFACT_SOURCE_COMMIT must identify the artifact source" >&2
  exit 2
fi
if [[ ! "${RUNS}" =~ ^[0-9]+$ ]] || (( RUNS < 3 || RUNS > 20 )); then
  echo "PLM_ECONOMICS_RUNS must be in [3,20]" >&2
  exit 2
fi

mkdir -p "$(dirname "${OUTPUT}")"
cd "${ROOT_DIR}"
PLM_MULTIREAD_ECONOMICS_OUTPUT="${OUTPUT}" \
PLM_ECONOMICS_RUNS="${RUNS}" \
PLM_TARGET_COMMIT="$(git rev-parse HEAD)" \
PLM_ARTIFACT_SOURCE_COMMIT="${ARTIFACT_SOURCE_COMMIT}" \
AGENT_RUNTIME_GUEST="${ARTIFACT}" \
go test ./integration/e2e -run '^TestRealGuestPLMMultireadEconomicsFixture$' -count=1 -v
