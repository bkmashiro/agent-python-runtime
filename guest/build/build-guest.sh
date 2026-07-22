#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK_DIR=${AGENT_RUNTIME_BUILD_DIR:-"${ROOT_DIR}/build/guest"}
DIST_DIR=${AGENT_RUNTIME_DIST_DIR:-"${ROOT_DIR}/dist"}
DOWNLOAD_DIR="${WORK_DIR}/downloads"
TOOLS_DIR="${WORK_DIR}/tools"
CPYTHON_DIR="${WORK_DIR}/cpython"

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "build-guest.sh currently requires Linux x86_64" >&2
  exit 2
fi

rm -rf "${WORK_DIR}" "${DIST_DIR}"
mkdir -p "${DOWNLOAD_DIR}" "${TOOLS_DIR}" "${DIST_DIR}"

fetch() {
  python3 "${ROOT_DIR}/tools/fetch_locked_source.py" "$1" "${DOWNLOAD_DIR}/$2"
}

fetch cpython-source cpython.tgz
fetch wasi-sdk-linux-x86_64 wasi-sdk.tar.gz
fetch wasm-tools-linux-x86_64 wasm-tools.tar.gz
fetch wasmtime-linux-x86_64 wasmtime.tar.xz
fetch wasi-vfs-cli-linux-x86_64 wasi-vfs-cli.zip
fetch wasi-vfs-static-library wasi-vfs-lib.zip
fetch wasi-vfs-linked-storage-source wasi-vfs-linked-storage.c
fetch spdx-2.3-json-schema spdx-2.3-schema.json

mkdir -p "${CPYTHON_DIR}" "${TOOLS_DIR}/wasi-sdk" "${TOOLS_DIR}/wasm-tools" \
  "${TOOLS_DIR}/wasmtime" "${TOOLS_DIR}/wasi-vfs-cli" "${TOOLS_DIR}/wasi-vfs-lib"
tar xzf "${DOWNLOAD_DIR}/cpython.tgz" -C "${CPYTHON_DIR}" --strip-components=1
tar xzf "${DOWNLOAD_DIR}/wasi-sdk.tar.gz" -C "${TOOLS_DIR}/wasi-sdk" --strip-components=1
tar xzf "${DOWNLOAD_DIR}/wasm-tools.tar.gz" -C "${TOOLS_DIR}/wasm-tools" --strip-components=1
tar xJf "${DOWNLOAD_DIR}/wasmtime.tar.xz" -C "${TOOLS_DIR}/wasmtime" --strip-components=1
unzip -q "${DOWNLOAD_DIR}/wasi-vfs-cli.zip" -d "${TOOLS_DIR}/wasi-vfs-cli"
unzip -q "${DOWNLOAD_DIR}/wasi-vfs-lib.zip" -d "${TOOLS_DIR}/wasi-vfs-lib"

WASI_SDK_PATH="${TOOLS_DIR}/wasi-sdk"
WASMTIME="${TOOLS_DIR}/wasmtime/wasmtime"
WASM_TOOLS=$(find "${TOOLS_DIR}/wasm-tools" -type f -name wasm-tools -perm -u+x | head -n 1)
WASI_VFS=$(find "${TOOLS_DIR}/wasi-vfs-cli" -type f -name wasi-vfs -perm -u+x | head -n 1)
WASI_VFS_LIB=$(find "${TOOLS_DIR}/wasi-vfs-lib" -type f -name '*.a' | head -n 1)

for required in "${WASI_SDK_PATH}/bin/clang" "${WASMTIME}" "${WASM_TOOLS}" \
  "${WASI_VFS}" "${WASI_VFS_LIB}"; do
  if [[ ! -e ${required} ]]; then
    echo "missing extracted build input: ${required}" >&2
    exit 3
  fi
done
chmod +x "${WASMTIME}" "${WASM_TOOLS}" "${WASI_VFS}"
export WASI_SDK_PATH WASMTIME
export PATH="$(dirname "${WASMTIME}"):${PATH}"
if [[ -z ${SOURCE_DATE_EPOCH:-} ]]; then
  SOURCE_DATE_EPOCH=$(python3 "${ROOT_DIR}/tools/source_date_epoch.py" HEAD)
fi
if [[ ! ${SOURCE_DATE_EPOCH} =~ ^[1-9][0-9]*$ ]]; then
  echo "SOURCE_DATE_EPOCH must be a positive integer" >&2
  exit 6
fi
export SOURCE_DATE_EPOCH

(
  cd "${CPYTHON_DIR}"
  python3 Tools/wasm/wasi build --wasi-sdk "${WASI_SDK_PATH}"
)

WASI_BUILD_DIR="${CPYTHON_DIR}/cross-build/wasm32-wasip1"
PYTHON_LIB=$(find "${WASI_BUILD_DIR}" -maxdepth 2 -type f -name 'libpython3.14.a' | head -n 1)
if [[ -z ${PYTHON_LIB} ]]; then
  echo "CPython WASI static library was not produced" >&2
  exit 4
fi
MPDEC_LIB="${WASI_BUILD_DIR}/Modules/_decimal/libmpdec/libmpdec.a"
EXPAT_LIB="${WASI_BUILD_DIR}/Modules/expat/libexpat.a"
HACL_LIBS=("${WASI_BUILD_DIR}"/Modules/_hacl/libHacl_*.a)
for required in "${MPDEC_LIB}" "${EXPAT_LIB}" "${HACL_LIBS[@]}"; do
  if [[ ! -f ${required} ]]; then
    echo "missing CPython static dependency: ${required}" >&2
    exit 5
  fi
done

CLANG="${WASI_SDK_PATH}/bin/clang"
LLVM_AR="${WASI_SDK_PATH}/bin/llvm-ar"
LLVM_NM="${WASI_SDK_PATH}/bin/llvm-nm"
SYSROOT="${WASI_SDK_PATH}/share/wasi-sysroot"
PATCHED_WASI_VFS_SOURCE="${WORK_DIR}/wasi-vfs-linked-storage.c"
python3 "${ROOT_DIR}/tools/patch_wasi_vfs_storage.py" \
  "${DOWNLOAD_DIR}/wasi-vfs-linked-storage.c" "${PATCHED_WASI_VFS_SOURCE}"
mapfile -t WASI_VFS_STORAGE_MEMBERS < <("${LLVM_AR}" t "${WASI_VFS_LIB}" | grep 'linked_storage\.o$')
if [[ ${#WASI_VFS_STORAGE_MEMBERS[@]} -ne 1 ]]; then
  echo "expected exactly one linked_storage.o archive member" >&2
  exit 7
fi
WASI_VFS_STORAGE_MEMBER=${WASI_VFS_STORAGE_MEMBERS[0]}
WASI_VFS_STORAGE_OBJECT_DIR="${WORK_DIR}/wasi-vfs-object"
mkdir -p "${WASI_VFS_STORAGE_OBJECT_DIR}"
WASI_VFS_STORAGE_OBJECT="${WASI_VFS_STORAGE_OBJECT_DIR}/${WASI_VFS_STORAGE_MEMBER}"
"${CLANG}" --target=wasm32-wasip1 --sysroot="${SYSROOT}" -O2 -DNDEBUG \
  -c "${PATCHED_WASI_VFS_SOURCE}" -o "${WASI_VFS_STORAGE_OBJECT}"
"${LLVM_AR}" d "${WASI_VFS_LIB}" "${WASI_VFS_STORAGE_MEMBER}"
(
  cd "${WASI_VFS_STORAGE_OBJECT_DIR}"
  "${LLVM_AR}" rcsD "${WASI_VFS_LIB}" "${WASI_VFS_STORAGE_MEMBER}"
)
WASI_VFS_STORAGE_SYMBOLS=$("${LLVM_NM}" -A "${WASI_VFS_LIB}" | grep -c ' T wasi_vfs_embed_linked_storage_new$' || true)
if [[ ${WASI_VFS_STORAGE_SYMBOLS} -ne 1 ]]; then
  echo "patched wasi-vfs archive has ${WASI_VFS_STORAGE_SYMBOLS} linked-storage definitions" >&2
  exit 8
fi
RUNTIME_OBJECT="${WORK_DIR}/runtime.o"
RAW_GUEST="${WORK_DIR}/agent-python-runtime.raw.wasm"
FINAL_GUEST="${DIST_DIR}/agent-python-runtime.wasm"

"${CLANG}" --target=wasm32-wasip1 --sysroot="${SYSROOT}" -O2 \
  -I"${ROOT_DIR}/guest/include" \
  -I"${CPYTHON_DIR}/Include" \
  -I"${WASI_BUILD_DIR}" \
  -c "${ROOT_DIR}/guest/src/runtime.c" -o "${RUNTIME_OBJECT}"

"${CLANG}" --target=wasm32-wasip1 --sysroot="${SYSROOT}" -O2 \
  -mexec-model=reactor \
  "${RUNTIME_OBJECT}" "${PYTHON_LIB}" \
  "${MPDEC_LIB}" "${HACL_LIBS[@]}" "${EXPAT_LIB}" "${WASI_VFS_LIB}" \
  -ldl -lwasi-emulated-getpid -lwasi-emulated-signal -lwasi-emulated-process-clocks \
  -lpthread -lm \
  -Wl,--export=runtime_init \
  -Wl,--export=runtime_prepare \
  -Wl,--export=alloc \
  -Wl,--export=dealloc \
  -Wl,--export=execute \
  -Wl,--export-memory \
  -Wl,--initial-memory=134217728 \
  -Wl,--max-memory=536870912 \
  -Wl,-z,stack-size=16777216 \
  -Wl,--strip-all \
  -o "${RAW_GUEST}"

VFS_PYTHON_DIR=${AGENT_RUNTIME_VFS_ROOT:-"${WORK_DIR}/vfs-python"}
rm -rf "${VFS_PYTHON_DIR}"
mkdir -p "${VFS_PYTHON_DIR}/site-packages"
python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
  "${CPYTHON_DIR}/Lib" "${VFS_PYTHON_DIR}" \
  --epoch "${SOURCE_DATE_EPOCH}"
python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
  "${ROOT_DIR}/guest/bootstrap/agent_runtime" \
  "${VFS_PYTHON_DIR}/site-packages/agent_runtime" \
  --epoch "${SOURCE_DATE_EPOCH}"

REPRO_VFS_MANIFEST=
if [[ -n ${AGENT_RUNTIME_REPRO_EVIDENCE_DIR:-} ]]; then
  REPRO_VFS_MANIFEST="${WORK_DIR}/vfs-manifest.prepack.json"
  python3 "${ROOT_DIR}/tools/write_guest_stage_evidence.py" manifest \
    --vfs-root "${VFS_PYTHON_DIR}" \
    --output "${REPRO_VFS_MANIFEST}"
fi

pack_guest() {
  local output=$1
  "${WASI_VFS}" pack "${RAW_GUEST}" \
    --dir "${VFS_PYTHON_DIR}::/usr/lib/python3.14" \
    -o "${output}"
}

REPEAT_PACKED_GUEST=
pack_guest "${FINAL_GUEST}"
if [[ -n ${AGENT_RUNTIME_REPRO_EVIDENCE_DIR:-} ]]; then
  REPEAT_PACKED_GUEST="${WORK_DIR}/agent-python-runtime.pack-b.wasm"
  pack_guest "${REPEAT_PACKED_GUEST}"
  "${WASM_TOOLS}" validate "${REPEAT_PACKED_GUEST}"
fi

"${WASM_TOOLS}" validate "${FINAL_GUEST}"
"${WASM_TOOLS}" print "${FINAL_GUEST}" > "${DIST_DIR}/agent-python-runtime.wat"
python3 "${ROOT_DIR}/guest/build/write-manifest.py" \
  --artifact "${FINAL_GUEST}" \
  --wat "${DIST_DIR}/agent-python-runtime.wat" \
  --source-lock "${ROOT_DIR}/guest/build/sources.lock.json" \
  --output "${DIST_DIR}/manifest.json"
python3 "${ROOT_DIR}/guest/build/write-supply-chain.py" \
  --artifact "${FINAL_GUEST}" \
  --manifest "${DIST_DIR}/manifest.json" \
  --source-lock "${ROOT_DIR}/guest/build/sources.lock.json" \
  --vfs-root "${VFS_PYTHON_DIR}" \
  --sbom "${DIST_DIR}/sbom.spdx.json" \
  --notices "${DIST_DIR}/THIRD_PARTY_NOTICES.md"
python3 "${ROOT_DIR}/guest/build/write-supply-chain.py" \
  --artifact "${FINAL_GUEST}" \
  --manifest "${DIST_DIR}/manifest.json" \
  --source-lock "${ROOT_DIR}/guest/build/sources.lock.json" \
  --vfs-root "${VFS_PYTHON_DIR}" \
  --sbom "${DIST_DIR}/sbom.spdx.json" \
  --notices "${DIST_DIR}/THIRD_PARTY_NOTICES.md" \
  --verify
(
  cd "${ROOT_DIR}"
  go run ./cmd/validate-json-schema \
    "${DOWNLOAD_DIR}/spdx-2.3-schema.json" \
    "${DIST_DIR}/sbom.spdx.json"
)
rm "${DIST_DIR}/agent-python-runtime.wat"
(
  cd "${DIST_DIR}"
  sha256sum \
    agent-python-runtime.wasm \
    manifest.json \
    sbom.spdx.json \
    THIRD_PARTY_NOTICES.md > SHA256SUMS
)

if [[ -n ${AGENT_RUNTIME_REPRO_EVIDENCE_DIR:-} ]]; then
  REPRO_REPOSITORY_COMMIT=${GITHUB_SHA:-}
  if [[ -z ${REPRO_REPOSITORY_COMMIT} ]]; then
    REPRO_REPOSITORY_COMMIT=$(git -C "${ROOT_DIR}" rev-parse HEAD)
  fi
  python3 "${ROOT_DIR}/tools/write_guest_stage_evidence.py" evidence \
    --evidence-dir "${AGENT_RUNTIME_REPRO_EVIDENCE_DIR}" \
    --raw-wasm "${RAW_GUEST}" \
    --final-wasm "${FINAL_GUEST}" \
    --repeat-packed-wasm "${REPEAT_PACKED_GUEST}" \
    --patched-wasi-vfs-archive "${WASI_VFS_LIB}" \
    --linked-storage-object "${WASI_VFS_STORAGE_OBJECT}" \
    --wasi-vfs-cli "${WASI_VFS}" \
    --source-lock "${ROOT_DIR}/guest/build/sources.lock.json" \
    --vfs-manifest "${REPRO_VFS_MANIFEST}" \
    --repository-commit "${REPRO_REPOSITORY_COMMIT}" \
    --source-date-epoch "${SOURCE_DATE_EPOCH}" \
    --run-id "${GITHUB_RUN_ID:-local}" \
    --run-attempt "${GITHUB_RUN_ATTEMPT:-local}" \
    --job "${GITHUB_JOB:-local}" \
    --replica "${AGENT_RUNTIME_REPRO_REPLICA:-local}" \
    --runner-os "${RUNNER_OS:-$(uname -s)}" \
    --runner-arch "${RUNNER_ARCH:-$(uname -m)}" \
    --build-dir "${WORK_DIR}" \
    --dist-dir "${DIST_DIR}" \
    --configured-vfs-root "${VFS_PYTHON_DIR}"
fi

printf 'guest artifact: %s\n' "${FINAL_GUEST}"
printf 'artifact sha256: '
sha256sum "${FINAL_GUEST}" | cut -d' ' -f1
