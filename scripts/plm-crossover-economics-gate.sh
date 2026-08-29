#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUTPUT_JSON=${1:-}
ARTIFACT=${AGENT_RUNTIME_GUEST:-}
RUNS=${PLM_CROSSOVER_RUNS:-5}
READ_COUNTS=${PLM_CROSSOVER_READ_COUNTS:-1,2,4,8}
DELAYS_MS=${PLM_CROSSOVER_DELAYS_MS:-25,75,200}
ZERO_READ=${PLM_CROSSOVER_ZERO_READ:-1}
TARGET_COMMIT=${PLM_TARGET_COMMIT:-}
SOURCE_TREE=${PLM_SOURCE_TREE:-}
ARTIFACT_SOURCE_COMMIT=${PLM_ARTIFACT_SOURCE_COMMIT:-}
HOST_ID=${EVALUATION_HOST_ID:-}
ORDER_OFFSET=${EVALUATION_ORDER_OFFSET:-0}
GO_TEST_TIMEOUT=${PLM_CROSSOVER_GO_TEST_TIMEOUT:-4h}

if [[ $# -ne 1 ]]; then
  echo "usage: $0 OUTPUT_JSON" >&2
  exit 2
fi
if [[ "${OUTPUT_JSON}" != /* ]]; then
  OUTPUT_JSON="${ROOT_DIR}/${OUTPUT_JSON}"
fi
if [[ -z "${ARTIFACT}" || ! -f "${ARTIFACT}" ]]; then
  echo "AGENT_RUNTIME_GUEST must point to the exact CPython/WASI artifact" >&2
  exit 2
fi
if [[ ! "${RUNS}" =~ ^[0-9]+$ ]] || (( RUNS < 3 || RUNS > 20 )); then
  echo "PLM_CROSSOVER_RUNS must be in [3,20]" >&2
  exit 2
fi
if [[ -z "${TARGET_COMMIT}" ]]; then
  TARGET_COMMIT=$(git -C "${ROOT_DIR}" rev-parse HEAD)
fi
if [[ -z "${SOURCE_TREE}" ]]; then
  SOURCE_TREE=$(git -C "${ROOT_DIR}" rev-parse 'HEAD^{tree}')
fi
if [[ ! "${TARGET_COMMIT}" =~ ^[0-9a-f]{40}$ || ! "${SOURCE_TREE}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "PLM_TARGET_COMMIT and PLM_SOURCE_TREE must be full Git identities" >&2
  exit 2
fi
if [[ ! "${ARTIFACT_SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "PLM_ARTIFACT_SOURCE_COMMIT must be a full Git commit" >&2
  exit 2
fi
if [[ -z "${HOST_ID}" ]]; then
  echo "EVALUATION_HOST_ID must identify the evaluation host" >&2
  exit 2
fi
if [[ ! "${ORDER_OFFSET}" =~ ^[0-9]+$ ]]; then
  echo "EVALUATION_ORDER_OFFSET must be a non-negative integer" >&2
  exit 2
fi
if [[ ! "${GO_TEST_TIMEOUT}" =~ ^[1-9][0-9]*(s|m|h)$ ]]; then
  echo "PLM_CROSSOVER_GO_TEST_TIMEOUT must be a bounded Go duration" >&2
  exit 2
fi

mkdir -p "$(dirname "${OUTPUT_JSON}")"
cd "${ROOT_DIR}"
PLM_CROSSOVER_OUTPUT="${OUTPUT_JSON}" \
PLM_CROSSOVER_RUNS="${RUNS}" \
PLM_CROSSOVER_READ_COUNTS="${READ_COUNTS}" \
PLM_CROSSOVER_DELAYS_MS="${DELAYS_MS}" \
PLM_CROSSOVER_ZERO_READ="${ZERO_READ}" \
PLM_TARGET_COMMIT="${TARGET_COMMIT}" \
PLM_SOURCE_TREE="${SOURCE_TREE}" \
PLM_ARTIFACT_SOURCE_COMMIT="${ARTIFACT_SOURCE_COMMIT}" \
EVALUATION_HOST_ID="${HOST_ID}" \
EVALUATION_ORDER_OFFSET="${ORDER_OFFSET}" \
AGENT_RUNTIME_GUEST="${ARTIFACT}" \
go test ./integration/e2e -timeout="${GO_TEST_TIMEOUT}" -run '^TestRealGuestPLMCrossoverEconomicsFixture$' -count=1 -v
