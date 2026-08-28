#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUTPUT_JSON=${1:-}
ARTIFACT=${AGENT_RUNTIME_GUEST:-}
SOURCE_COMMIT=${PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT:-}
SOURCE_TREE=${PYSOLATE_PREPARED_FAMILY_SOURCE_TREE:-}
HOST_ID=${EVALUATION_HOST_ID:-}
RUNS=${COW_FANOUT_RUNS:-5}
FANOUTS_RAW=${COW_FANOUTS:-1,2,4,8}
ORDER_OFFSET=${COW_ORDER_OFFSET:-0}

if [[ -z "${OUTPUT_JSON}" ]]; then
  echo "usage: cow-fanout-economics-gate.sh OUTPUT_JSON" >&2
  exit 2
fi
if [[ "${OUTPUT_JSON}" != /* ]]; then
  OUTPUT_JSON="${ROOT_DIR}/${OUTPUT_JSON}"
fi
if [[ -z "${ARTIFACT}" || ! -f "${ARTIFACT}" ]]; then
  echo "AGENT_RUNTIME_GUEST must point to the exact numpy-core CPython/WASI artifact" >&2
  exit 2
fi
if [[ ! "${SOURCE_COMMIT}" =~ ^[0-9a-f]{40}$ || ! "${SOURCE_TREE}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT and PYSOLATE_PREPARED_FAMILY_SOURCE_TREE must be full Git object IDs" >&2
  exit 2
fi
if [[ -z "${HOST_ID}" || "${HOST_ID}" == *$'\n'* ]]; then
  echo "EVALUATION_HOST_ID must identify the evaluation host" >&2
  exit 2
fi
if [[ ! "${RUNS}" =~ ^[0-9]+$ ]] || (( RUNS < 3 || RUNS > 20 )); then
  echo "COW_FANOUT_RUNS must be in [3,20]" >&2
  exit 2
fi
if [[ ! "${ORDER_OFFSET}" =~ ^[01]$ ]]; then
  echo "COW_ORDER_OFFSET must be 0 or 1" >&2
  exit 2
fi
if [[ $(uname -s) != Linux ]]; then
  echo "COW fan-out economics requires Linux" >&2
  exit 2
fi

FANOUTS_NORMALIZED=${FANOUTS_RAW//,/ }
read -r -a FANOUTS <<< "${FANOUTS_NORMALIZED}"
if (( ${#FANOUTS[@]} == 0 )); then
  echo "COW_FANOUTS must not be empty" >&2
  exit 2
fi
previous=0
validated_fanouts=()
for fanout in "${FANOUTS[@]}"; do
  if [[ ! "${fanout}" =~ ^[0-9]+$ ]] || (( fanout < 1 || fanout > 8 )); then
    echo "COW_FANOUTS entries must be in [1,8]" >&2
    exit 2
  fi
  if (( ${#validated_fanouts[@]} > 0 && fanout <= previous )); then
    if (( fanout == previous )); then
      echo "COW_FANOUTS must not contain duplicate fanouts" >&2
    else
      echo "COW_FANOUTS must be strictly ordered" >&2
    fi
    exit 2
  fi
  validated_fanouts+=("${fanout}")
  previous=${fanout}
done

mkdir -p "$(dirname "${OUTPUT_JSON}")"
cd "${ROOT_DIR}"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/cow-fanout-economics.XXXXXX")
trap 'rm -rf "${work_dir}"' EXIT

raw_inputs=()
for fanout in "${FANOUTS[@]}"; do
  raw_output="${work_dir}/${fanout}.json"
  PYSOLATE_PREPARED_FAMILY_ECONOMICS_OUTPUT="${raw_output}" \
  PYSOLATE_PREPARED_FAMILY_ECONOMICS_RUNS="${RUNS}" \
  PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT="${fanout}" \
  PYSOLATE_PREPARED_FAMILY_ECONOMICS_ORDER_OFFSET="${ORDER_OFFSET}" \
  PYSOLATE_PREPARED_FAMILY_SOURCE_COMMIT="${SOURCE_COMMIT}" \
  PYSOLATE_PREPARED_FAMILY_SOURCE_TREE="${SOURCE_TREE}" \
  AGENT_RUNTIME_GUEST="${ARTIFACT}" \
    scripts/prepared-family-economics-gate.sh "${raw_output}"
  raw_inputs+=("${raw_output}")
done

project_args=(
  --runs "${RUNS}"
  --host-id "${HOST_ID}"
  --source-commit "${SOURCE_COMMIT}"
  --source-tree "${SOURCE_TREE}"
  --artifact "${ARTIFACT}"
  --output "${OUTPUT_JSON}"
  --fanouts "${FANOUTS[@]}"
)
for raw_input in "${raw_inputs[@]}"; do
  project_args+=(--input "${raw_input}")
done
python3 scripts/project-cow-fanout-economics.py "${project_args[@]}"
