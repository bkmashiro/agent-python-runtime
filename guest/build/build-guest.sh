#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK_DIR=${AGENT_RUNTIME_BUILD_DIR:-"${ROOT_DIR}/build/guest"}
DIST_DIR=${AGENT_RUNTIME_DIST_DIR:-"${ROOT_DIR}/dist"}
DOWNLOAD_DIR="${WORK_DIR}/downloads"
TOOLS_DIR="${WORK_DIR}/tools"
CPYTHON_DIR="${WORK_DIR}/cpython"
SOURCE_LOCK="${ROOT_DIR}/guest/build/sources.lock.json"
ARTIFACT_PROFILE=base
ARTIFACT_FILENAME="agent-python-runtime.wasm"
INITIAL_MEMORY_BYTES=134217728
MAX_MEMORY_BYTES=536870912
MEMORY_INITIAL_PAGES=$((INITIAL_MEMORY_BYTES / 65536))
MEMORY_MAXIMUM_PAGES=$((MAX_MEMORY_BYTES / 65536))

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "build-guest.sh currently requires Linux x86_64" >&2
  exit 2
fi

rm -rf "${WORK_DIR}" "${DIST_DIR}"
mkdir -p "${DOWNLOAD_DIR}" "${TOOLS_DIR}" "${DIST_DIR}"

fetch() {
  python3 "${ROOT_DIR}/tools/fetch_locked_source.py" "$1" "${DOWNLOAD_DIR}/$2" \
    --lock "${SOURCE_LOCK}"
}

fetch cpython-source cpython.tgz
fetch wasi-sdk-linux-x86_64 wasi-sdk.tar.gz
fetch wasm-tools-linux-x86_64 wasm-tools.tar.gz
fetch wasmtime-linux-x86_64 wasmtime.tar.xz
fetch wasi-vfs-cli-linux-x86_64 wasi-vfs-cli.zip
fetch wasi-vfs-static-library wasi-vfs-lib.zip
fetch wasi-vfs-linked-storage-source wasi-vfs-linked-storage.c

mkdir -p "${CPYTHON_DIR}" "${TOOLS_DIR}/wasi-sdk" "${TOOLS_DIR}/wasm-tools" \
  "${TOOLS_DIR}/wasmtime" "${TOOLS_DIR}/wasi-vfs-cli" "${TOOLS_DIR}/wasi-vfs-lib"
tar xzf "${DOWNLOAD_DIR}/cpython.tgz" -C "${CPYTHON_DIR}" --strip-components=1
CPYTHON_WASI_CONFIG_SITE="${CPYTHON_DIR}/Tools/wasm/wasi/config.site-wasm32-wasi"
python3 "${ROOT_DIR}/tools/patch_cpython_wasi_timer_config.py" \
  "${CPYTHON_WASI_CONFIG_SITE}" "${CPYTHON_WASI_CONFIG_SITE}.patched"
mv "${CPYTHON_WASI_CONFIG_SITE}.patched" "${CPYTHON_WASI_CONFIG_SITE}"
python3 "${ROOT_DIR}/tools/patch_cpython_import_gate.py" \
  "${CPYTHON_DIR}/Python/import.c"
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
WASI_PYCONFIG="${WASI_BUILD_DIR}/pyconfig.h"
if grep -q '^#define HAVE_CLOCK_NANOSLEEP 1$' "${WASI_PYCONFIG}"; then
  echo "CPython WASI build unexpectedly enabled absolute clock_nanosleep" >&2
  exit 17
fi
if ! grep -q '^#define HAVE_NANOSLEEP 1$' "${WASI_PYCONFIG}"; then
  echo "CPython WASI build did not enable relative nanosleep" >&2
  exit 18
fi
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
FINAL_GUEST="${DIST_DIR}/${ARTIFACT_FILENAME}"

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
  -Wl,--export=runtime_validate_source \
  -Wl,--export=runtime_prepare \
  -Wl,--export=alloc \
  -Wl,--export=dealloc \
  -Wl,--export=execute \
  -Wl,--export-memory \
  -Wl,--initial-memory="${INITIAL_MEMORY_BYTES}" \
  -Wl,--max-memory="${MAX_MEMORY_BYTES}" \
  -Wl,-z,stack-size=16777216 \
  -Wl,--strip-all \
  -o "${RAW_GUEST}"

VFS_PYTHON_DIR=${AGENT_RUNTIME_VFS_ROOT:-"${WORK_DIR}/vfs-python"}
rm -rf "${VFS_PYTHON_DIR}"
mkdir -p "${VFS_PYTHON_DIR}/site-packages"
python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
  "${CPYTHON_DIR}/Lib" "${VFS_PYTHON_DIR}" \
  --epoch "${SOURCE_DATE_EPOCH}"
mapfile -t TARGET_SYSCONFIG_DATA < <(find "${WASI_BUILD_DIR}" -type f -name '_sysconfigdata_*.py' -print)
if [[ ${#TARGET_SYSCONFIG_DATA[@]} -ne 1 ]]; then
  echo "expected exactly one target sysconfigdata file, found ${#TARGET_SYSCONFIG_DATA[@]}" >&2
  exit 9
fi
TARGET_SYSCONFIG_DEST="${VFS_PYTHON_DIR}/$(basename "${TARGET_SYSCONFIG_DATA[0]}")"
install -m 0644 "${TARGET_SYSCONFIG_DATA[0]}" "${TARGET_SYSCONFIG_DEST}"
touch -d "@${SOURCE_DATE_EPOCH}" "${TARGET_SYSCONFIG_DEST}"
python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
  "${ROOT_DIR}/guest/bootstrap/agent_runtime" \
  "${VFS_PYTHON_DIR}/site-packages/agent_runtime" \
  --epoch "${SOURCE_DATE_EPOCH}"
python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
  "${ROOT_DIR}/guest/bootstrap/pysolate" \
  "${VFS_PYTHON_DIR}/site-packages/pysolate" \
  --epoch "${SOURCE_DATE_EPOCH}"

pack_guest() {
  local output=$1
  "${WASI_VFS}" pack "${RAW_GUEST}" \
    --dir "${VFS_PYTHON_DIR}::/usr/lib/python3.14" \
    -o "${output}"
}

pack_guest "${FINAL_GUEST}"

"${WASM_TOOLS}" validate "${FINAL_GUEST}"
"${WASM_TOOLS}" print "${FINAL_GUEST}" > "${DIST_DIR}/agent-python-runtime.wat"
PROBE_RUNNER=${AGENT_RUNTIME_PROBE_RUNNER:-"${WORK_DIR}/apyrun-probe"}
if [[ -z ${AGENT_RUNTIME_PROBE_RUNNER:-} ]]; then
  (
    cd "${ROOT_DIR}"
    go build -o "${PROBE_RUNNER}" ./cmd/apyrun
  )
fi
if [[ ! -x ${PROBE_RUNNER} ]]; then
  echo "probe runner is not executable: ${PROBE_RUNNER}" >&2
  exit 10
fi
IMPORT_INVENTORY_REQUEST="${WORK_DIR}/import-inventory-request.json"
IMPORT_INVENTORY_RESPONSE="${WORK_DIR}/import-inventory-response.json"
IMPORT_INVENTORY="${DIST_DIR}/import-inventory.json"
python3 "${ROOT_DIR}/guest/build/import_inventory.py" request \
  --profile "${ARTIFACT_PROFILE}" \
  --output "${IMPORT_INVENTORY_REQUEST}"
(
  cd "${ROOT_DIR}"
  "${PROBE_RUNNER}" -artifact "${FINAL_GUEST}" \
    < "${IMPORT_INVENTORY_REQUEST}" \
    > "${IMPORT_INVENTORY_RESPONSE}"
)
python3 "${ROOT_DIR}/guest/build/import_inventory.py" extract \
  --profile "${ARTIFACT_PROFILE}" \
  --response "${IMPORT_INVENTORY_RESPONSE}" \
  --output "${IMPORT_INVENTORY}"

run_import_qualification() {
  local artifact=$1
  local profile=$2
  local work_prefix=$3
  local output=$4
  local requests="${work_prefix}/requests"
  local responses="${work_prefix}/responses"
  rm -rf "${work_prefix}"
  mkdir -p "${requests}" "${responses}"
  python3 "${ROOT_DIR}/guest/build/import_qualification.py" requests \
    --profile "${profile}" \
    --output-dir "${requests}"
  local request
  local response
  for request in "${requests}"/*.json; do
    response="${responses}/$(basename "${request}")"
    "${PROBE_RUNNER}" -artifact "${artifact}" \
      < "${request}" \
      > "${response}"
  done
  python3 "${ROOT_DIR}/guest/build/import_qualification.py" extract \
    --profile "${profile}" \
    --responses-dir "${responses}" \
    --output "${output}"
}

IMPORT_QUALIFICATION="${DIST_DIR}/import-qualification.json"
run_import_qualification \
  "${FINAL_GUEST}" \
  "${ARTIFACT_PROFILE}" \
  "${WORK_DIR}/import-qualification" \
  "${IMPORT_QUALIFICATION}"
python3 "${ROOT_DIR}/guest/build/write-manifest.py" \
  --artifact "${FINAL_GUEST}" \
  --wat "${DIST_DIR}/agent-python-runtime.wat" \
  --source-lock "${SOURCE_LOCK}" \
  --artifact-profile "${ARTIFACT_PROFILE}" \
  --import-inventory "${IMPORT_INVENTORY}" \
  --import-qualification "${IMPORT_QUALIFICATION}" \
  --memory-initial-pages "${MEMORY_INITIAL_PAGES}" \
  --memory-maximum-pages "${MEMORY_MAXIMUM_PAGES}" \
  --output "${DIST_DIR}/manifest.json"

python3 "${ROOT_DIR}/guest/build/write-supply-chain.py" \
  --artifact "${FINAL_GUEST}" \
  --manifest "${DIST_DIR}/manifest.json" \
  --source-lock "${SOURCE_LOCK}" \
  --vfs-root "${VFS_PYTHON_DIR}" \
  --sbom "${DIST_DIR}/sbom.spdx.json" \
  --notices "${DIST_DIR}/THIRD_PARTY_NOTICES.md"
python3 "${ROOT_DIR}/guest/build/write-supply-chain.py" \
  --artifact "${FINAL_GUEST}" \
  --manifest "${DIST_DIR}/manifest.json" \
  --source-lock "${SOURCE_LOCK}" \
  --vfs-root "${VFS_PYTHON_DIR}" \
  --sbom "${DIST_DIR}/sbom.spdx.json" \
  --notices "${DIST_DIR}/THIRD_PARTY_NOTICES.md" \
  --verify
rm "${DIST_DIR}/agent-python-runtime.wat"
(
  cd "${DIST_DIR}"
  SUM_FILES=(
    "${ARTIFACT_FILENAME}"
    manifest.json
    import-inventory.json
    import-qualification.json
    sbom.spdx.json
    THIRD_PARTY_NOTICES.md
  )
  sha256sum "${SUM_FILES[@]}" > SHA256SUMS
)

printf 'guest artifact: %s\n' "${FINAL_GUEST}"
printf 'artifact sha256: '
sha256sum "${FINAL_GUEST}" | cut -d' ' -f1
