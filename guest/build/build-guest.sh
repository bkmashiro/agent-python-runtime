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
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-1784692800}

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
SYSROOT="${WASI_SDK_PATH}/share/wasi-sysroot"
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

"${WASI_VFS}" pack "${RAW_GUEST}" \
  --dir "${CPYTHON_DIR}/Lib::/usr/lib/python3.14" \
  --dir "${ROOT_DIR}/guest/bootstrap/agent_runtime::/usr/lib/python3.14/site-packages/agent_runtime" \
  -o "${FINAL_GUEST}"

"${WASM_TOOLS}" validate "${FINAL_GUEST}"
"${WASM_TOOLS}" print "${FINAL_GUEST}" > "${DIST_DIR}/agent-python-runtime.wat"
python3 "${ROOT_DIR}/guest/build/write-manifest.py" \
  --artifact "${FINAL_GUEST}" \
  --wat "${DIST_DIR}/agent-python-runtime.wat" \
  --source-lock "${ROOT_DIR}/guest/build/sources.lock.json" \
  --output "${DIST_DIR}/manifest.json"
rm "${DIST_DIR}/agent-python-runtime.wat"
(
  cd "${DIST_DIR}"
  sha256sum agent-python-runtime.wasm manifest.json > SHA256SUMS
)
cp "${ROOT_DIR}/NOTICE.md" "${DIST_DIR}/THIRD_PARTY_NOTICES.md"

printf 'guest artifact: %s\n' "${FINAL_GUEST}"
printf 'artifact sha256: '
sha256sum "${FINAL_GUEST}" | cut -d' ' -f1
