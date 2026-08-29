#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ARTIFACT=${AGENT_RUNTIME_GUEST:-}
SOURCE_COMMIT=${PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT:-}
SOURCE_TREE=${PYSOLATE_PREPARED_FAMILY_SOURCE_TREE:-}
RUNS=${PYSOLATE_PREPARED_FAMILY_ECONOMICS_RUNS:-5}
FANOUT=${PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT:-4}
GO_TEST_TIMEOUT=${PYSOLATE_PREPARED_FAMILY_GO_TEST_TIMEOUT:-10m}
OUTPUT=${1:-"${ROOT_DIR}/docs/evidence/prepared-family-economics-linux.json"}

if [[ "${OUTPUT}" != /* ]]; then
  OUTPUT="${ROOT_DIR}/${OUTPUT}"
fi
if [[ -z "${ARTIFACT}" || ! -f "${ARTIFACT}" ]]; then
  echo "AGENT_RUNTIME_GUEST must point to the exact numpy-core CPython/WASI artifact" >&2
  exit 2
fi
if [[ ! "${SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ || ! "${SOURCE_TREE}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT and SOURCE_TREE must be full Git object IDs" >&2
  exit 2
fi
if [[ ! "${RUNS}" =~ ^[0-9]+$ ]] || (( RUNS < 3 || RUNS > 20 )); then
  echo "PYSOLATE_PREPARED_FAMILY_ECONOMICS_RUNS must be in [3,20]" >&2
  exit 2
fi
if [[ ! "${FANOUT}" =~ ^[0-9]+$ ]] || (( FANOUT < 1 || FANOUT > 8 )); then
  echo "PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT must be in [1,8]" >&2
  exit 2
fi
if [[ ! "${GO_TEST_TIMEOUT}" =~ ^[1-9][0-9]*(s|m|h)$ ]]; then
  echo "PYSOLATE_PREPARED_FAMILY_GO_TEST_TIMEOUT must be a bounded Go duration" >&2
  exit 2
fi
if [[ $(uname -s) != Linux ]]; then
  echo "prepared-family economics requires Linux" >&2
  exit 2
fi

mkdir -p "$(dirname "${OUTPUT}")"
cd "${ROOT_DIR}"
PYSOLATE_PREPARED_FAMILY_ECONOMICS_OUTPUT="${OUTPUT}" \
PYSOLATE_PREPARED_FAMILY_ECONOMICS_RUNS="${RUNS}" \
PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT="${FANOUT}" \
PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT="${SOURCE_COMMIT}" \
PYSOLATE_PREPARED_FAMILY_SOURCE_TREE="${SOURCE_TREE}" \
AGENT_RUNTIME_GUEST="${ARTIFACT}" \
go test ./runtime/engine/wazero -timeout="${GO_TEST_TIMEOUT}" -run '^TestPreparedFamilyEconomicsFixture$' -count=1 -v
