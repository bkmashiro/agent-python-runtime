#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK_DIR=${AGENT_RUNTIME_BUILD_DIR:-"${ROOT_DIR}/build/guest"}
DIST_DIR=${AGENT_RUNTIME_DIST_DIR:-"${ROOT_DIR}/dist"}
DOWNLOAD_DIR="${WORK_DIR}/downloads"
TOOLS_DIR="${WORK_DIR}/tools"
CPYTHON_DIR="${WORK_DIR}/cpython"
ARTIFACT_PROFILE=${AGENT_RUNTIME_ARTIFACT_PROFILE:-base}
PREINITIALIZATION_SPIKE=${AGENT_RUNTIME_PREINITIALIZATION_SPIKE:-0}
case "${ARTIFACT_PROFILE}" in
  base)
    SOURCE_LOCK="${ROOT_DIR}/guest/build/sources.lock.json"
    ARTIFACT_FILENAME="agent-python-runtime.wasm"
    ;;
  numpy-core)
    SOURCE_LOCK="${ROOT_DIR}/guest/build/sources.numpy-core.lock.json"
    ARTIFACT_FILENAME="agent-python-runtime-numpy-core.wasm"
    ;;
  *)
    echo "unsupported artifact profile: ${ARTIFACT_PROFILE}" >&2
    exit 10
    ;;
esac
case "${PREINITIALIZATION_SPIKE}" in
  0|1) ;;
  *)
    echo "AGENT_RUNTIME_PREINITIALIZATION_SPIKE must be 0 or 1" >&2
    exit 12
    ;;
esac
if [[ ${PREINITIALIZATION_SPIKE} == 1 && ${ARTIFACT_PROFILE} != base ]]; then
  echo "preinitialization spike is restricted to the base artifact profile" >&2
  exit 13
fi

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

MANIFEST_EXTENSION_ARGS=()
if [[ ${ARTIFACT_PROFILE} == numpy-core ]]; then
  NUMPY_PROFILE_DIR="${WORK_DIR}/numpy-core-profile"
  AGENT_RUNTIME_BUILD_DIR="${WORK_DIR}" \
  AGENT_RUNTIME_VFS_ROOT="${VFS_PYTHON_DIR}" \
  NUMPY_WASI_PROBE_DIR="${NUMPY_PROFILE_DIR}" \
  NUMPY_WASI_FEATURE_PROFILE=core \
  NUMPY_WASI_SOURCE_LOCK="${SOURCE_LOCK}" \
    bash "${ROOT_DIR}/experiments/numpy-wasi/probe.sh"

  PROFILE_OUTPUT_DIR="${NUMPY_PROFILE_DIR}/extension-profile"
  PROFILE_SELECTION="${PROFILE_OUTPUT_DIR}/selection-report.json"
  mapfile -t NUMPY_EXTENSION_ARCHIVES <"${PROFILE_OUTPUT_DIR}/extension-archives.txt"
  mapfile -t NUMPY_STATIC_INPUTS <"${PROFILE_OUTPUT_DIR}/static-inputs.txt"
  if [[ ${#NUMPY_EXTENSION_ARCHIVES[@]} -ne 2 ]]; then
    echo "numpy-core profile must select exactly two extension archives" >&2
    exit 11
  fi
  install -m 0644 "${PROFILE_SELECTION}" "${DIST_DIR}/extension-selection.json"
  touch -d "@${SOURCE_DATE_EPOCH}" "${DIST_DIR}/extension-selection.json"
  MANIFEST_EXTENSION_ARGS=(--extension-selection "${DIST_DIR}/extension-selection.json")
  VFS_PYTHON_DIR="${NUMPY_PROFILE_DIR}/vfs-python"

  "${CLANG}" --target=wasm32-wasip1 --sysroot="${SYSROOT}" -O2 \
    -DAGENT_RUNTIME_EXTENSION_PROFILE=1 \
    -I"${ROOT_DIR}/guest/include" \
    -I"${CPYTHON_DIR}/Include" \
    -I"${WASI_BUILD_DIR}" \
    -I"${PROFILE_OUTPUT_DIR}" \
    -c "${ROOT_DIR}/guest/src/runtime.c" -o "${RUNTIME_OBJECT}"

  "${WASI_SDK_PATH}/bin/clang++" --target=wasm32-wasip1 --sysroot="${SYSROOT}" \
    -fno-exceptions \
    -mexec-model=reactor \
    "${RUNTIME_OBJECT}" \
    -Wl,--whole-archive "${NUMPY_EXTENSION_ARCHIVES[@]}" -Wl,--no-whole-archive \
    "${NUMPY_STATIC_INPUTS[@]}" \
    "${PYTHON_LIB}" "${MPDEC_LIB}" "${HACL_LIBS[@]}" "${EXPAT_LIB}" "${WASI_VFS_LIB}" \
    -ldl -lwasi-emulated-getpid -lwasi-emulated-signal -lwasi-emulated-process-clocks \
    -lpthread -lm -lc-printscan-long-double \
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
fi

if [[ ${PREINITIALIZATION_SPIKE} == 1 ]]; then
  PREINITIALIZATION_SPIKE_DIR="${WORK_DIR}/preinitialize-spike"
  PREINITIALIZATION_INPUT_DIR="${PREINITIALIZATION_SPIKE_DIR}/input"
  PREINITIALIZATION_RUNTIME_OBJECT="${PREINITIALIZATION_SPIKE_DIR}/runtime.o"
  PREINITIALIZATION_RAW_GUEST="${PREINITIALIZATION_SPIKE_DIR}/agent-python-runtime.raw.wasm"
  PREINITIALIZATION_INPUT_GUEST="${PREINITIALIZATION_INPUT_DIR}/agent-python-runtime.wasm"
  mkdir -p "${PREINITIALIZATION_INPUT_DIR}"

  "${CLANG}" --target=wasm32-wasip1 --sysroot="${SYSROOT}" -O2 \
    -DAGENT_RUNTIME_PREINITIALIZATION_SPIKE=1 \
    -I"${ROOT_DIR}/guest/include" \
    -I"${CPYTHON_DIR}/Include" \
    -I"${WASI_BUILD_DIR}" \
    -c "${ROOT_DIR}/guest/src/runtime.c" -o "${PREINITIALIZATION_RUNTIME_OBJECT}"

  "${CLANG}" --target=wasm32-wasip1 --sysroot="${SYSROOT}" -O2 \
    -mexec-model=reactor \
    "${PREINITIALIZATION_RUNTIME_OBJECT}" "${PYTHON_LIB}" \
    "${MPDEC_LIB}" "${HACL_LIBS[@]}" "${EXPAT_LIB}" "${WASI_VFS_LIB}" \
    -ldl -lwasi-emulated-getpid -lwasi-emulated-signal -lwasi-emulated-process-clocks \
    -lpthread -lm \
    -Wl,--export=runtime_init \
    -Wl,--export=runtime_prepare \
    -Wl,--export=runtime_preinitialize \
    -Wl,--export=runtime_preinitialized_initialize \
    -Wl,--export=alloc \
    -Wl,--export=dealloc \
    -Wl,--export=execute \
    -Wl,--export-memory \
    -Wl,--initial-memory=134217728 \
    -Wl,--max-memory=536870912 \
    -Wl,-z,stack-size=16777216 \
    -Wl,--strip-all \
    -o "${PREINITIALIZATION_RAW_GUEST}"

  "${WASI_VFS}" pack "${PREINITIALIZATION_RAW_GUEST}" \
    --dir "${VFS_PYTHON_DIR}::/usr/lib/python3.14" \
    -o "${PREINITIALIZATION_INPUT_GUEST}"
  "${WASM_TOOLS}" validate "${PREINITIALIZATION_INPUT_GUEST}"
fi

"${WASI_VFS}" pack "${RAW_GUEST}" \
  --dir "${VFS_PYTHON_DIR}::/usr/lib/python3.14" \
  -o "${FINAL_GUEST}"

"${WASM_TOOLS}" validate "${FINAL_GUEST}"
"${WASM_TOOLS}" print "${FINAL_GUEST}" > "${DIST_DIR}/agent-python-runtime.wat"
python3 "${ROOT_DIR}/guest/build/write-manifest.py" \
  --artifact "${FINAL_GUEST}" \
  --wat "${DIST_DIR}/agent-python-runtime.wat" \
  --source-lock "${SOURCE_LOCK}" \
  --artifact-profile "${ARTIFACT_PROFILE}" \
  "${MANIFEST_EXTENSION_ARGS[@]}" \
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
(
  cd "${ROOT_DIR}"
  go run ./cmd/validate-json-schema \
    "${DOWNLOAD_DIR}/spdx-2.3-schema.json" \
    "${DIST_DIR}/sbom.spdx.json"
)
rm "${DIST_DIR}/agent-python-runtime.wat"
(
  cd "${DIST_DIR}"
  SUM_FILES=(
    "${ARTIFACT_FILENAME}"
    manifest.json
    sbom.spdx.json
    THIRD_PARTY_NOTICES.md
  )
  if [[ ${ARTIFACT_PROFILE} == numpy-core ]]; then
    SUM_FILES+=(extension-selection.json)
  fi
  sha256sum "${SUM_FILES[@]}" > SHA256SUMS
)

printf 'guest artifact: %s\n' "${FINAL_GUEST}"
printf 'artifact sha256: '
sha256sum "${FINAL_GUEST}" | cut -d' ' -f1
