#!/usr/bin/env bash
# Build the minimal pysolate Guest from an existing CPython/WASI input set.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BUILD=${PYSOLATE_BUILD_DIR:-"${ROOT}/build/guest"}
INPUTS=${PYSOLATE_BUILD_INPUTS:-"${ROOT}/build/inputs"}
NUMPY_ROOT=${PYSOLATE_NUMPY_NATIVE_ROOT:-"${ROOT}/build/numpy/numpy-native"}
NUMPY_PACKAGE=${PYSOLATE_NUMPY_PACKAGE_ROOT:-"${ROOT}/build/numpy/numpy-package/numpy"}
DIST=${PYSOLATE_DIST_DIR:-"${ROOT}/dist"}
RAW_CORE=${PYSOLATE_RAW_CORE:-"${BUILD}/native/raw-core.wasm"}
RELINK=${PYSOLATE_RELINK:-1}
LOCK="${ROOT}/guest/build/profiles/numpy-core.lock.json"
AGENT_PROFILE="${ROOT}/guest/build/profiles/agent-core.json"
PROFILE="${ROOT}/guest/build/native_package_profile.py"

[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'build-guest.sh requires Linux x86_64' >&2; exit 2; }
[[ -d ${INPUTS} ]] || { echo "missing PYSOLATE_BUILD_INPUTS: ${INPUTS}" >&2; exit 3; }
[[ -n ${NUMPY_PACKAGE} && -d ${NUMPY_PACKAGE} ]] || { echo 'set PYSOLATE_NUMPY_PACKAGE_ROOT to the qualified NumPy package tree' >&2; exit 5; }
[[ ${RELINK} == 0 || ${RELINK} == 1 ]] || { echo 'PYSOLATE_RELINK must be 0 or 1' >&2; exit 8; }

SDK=${PYSOLATE_WASI_SDK:-"${INPUTS}/tools/wasi-sdk"}
PY=${PYSOLATE_CPYTHON_ROOT:-"${INPUTS}/cpython"}
WASI=${PYSOLATE_CPYTHON_WASI:-"${PY}/cross-build/wasm32-wasip1"}
VFS=${PYSOLATE_WASI_VFS:-"${INPUTS}/tools/wasi-vfs-cli/wasi-vfs"}
VFS_LIB=${PYSOLATE_WASI_VFS_LIB:-"${INPUTS}/tools/wasi-vfs-lib/libwasi_vfs.a"}
WASM_TOOLS=${PYSOLATE_WASM_TOOLS:-"${INPUTS}/tools/wasm-tools/wasm-tools"}
HOST_PY=${PYSOLATE_HOST_PYTHON:-"${PY}/cross-build/build/python"}
CLANG=${SDK}/bin/clang
LLVM_AR=${SDK}/bin/llvm-ar

for f in "${VFS}" "${WASM_TOOLS}" "${HOST_PY}"; do
  [[ -e ${f} ]] || { echo "missing build input: ${f}" >&2; exit 6; }
done
if [[ ${RELINK} == 1 ]]; then
  [[ -n ${NUMPY_ROOT} && -d ${NUMPY_ROOT} ]] || { echo 'set PYSOLATE_NUMPY_NATIVE_ROOT to the qualified NumPy archive root' >&2; exit 4; }
  for f in "${CLANG}" "${LLVM_AR}" "${VFS_LIB}" "${PY}/Include/Python.h" \
    "${WASI}/libpython3.14.a" "${WASI}/Modules/_decimal/libmpdec/libmpdec.a" \
    "${WASI}/Modules/expat/libexpat.a"; do
    [[ -e ${f} ]] || { echo "missing native build input: ${f}" >&2; exit 6; }
  done
  mapfile -t HACL < <(printf '%s\n' "${WASI}"/Modules/_hacl/*.a)
  [[ -f ${HACL[0]:-} ]] || { echo "missing HACL archives under ${WASI}/Modules/_hacl" >&2; exit 7; }
fi
python3 "${PROFILE}" validate-lock --lock "${LOCK}"

rm -rf "${BUILD}/vfs"
mkdir -p "${BUILD}/vfs/site-packages" "${DIST}"
if [[ ${RELINK} == 1 ]]; then
  rm -rf "${BUILD}/link" "${BUILD}/native"
  mkdir -p "${BUILD}/link" "${BUILD}/native"
  python3 "${PROFILE}" registration-header --lock "${LOCK}" --output "${BUILD}/link/builtin-registry.h"
  mapfile -t NUMPY_ARCHIVES < <(python3 "${PROFILE}" archive-paths --lock "${LOCK}" --archive-root "${NUMPY_ROOT}")
  mapfile -t NUMPY_SYMBOLS < <(python3 "${PROFILE}" init-symbols --lock "${LOCK}")
  NUMPY_FORCE=()
  for symbol in "${NUMPY_SYMBOLS[@]}"; do NUMPY_FORCE+=("-Wl,-u,${symbol}"); done
  mapfile -t NUMPY_LIBS < <(python3 "${PROFILE}" link-libraries --lock "${LOCK}")

  "${CLANG}" --target=wasm32-wasip1 --sysroot="${SDK}/share/wasi-sysroot" -O2 \
    -I"${PY}/Include" -I"${WASI}" -I"${BUILD}/link" -DPYSOLATE_NUMPY \
    -c "${ROOT}/guest/runtime.c" -o "${BUILD}/link/runtime.o"
  "${CLANG}" --target=wasm32-wasip1 --sysroot="${SDK}/share/wasi-sysroot" -O2 \
    -mexec-model=reactor "${BUILD}/link/runtime.o" "${NUMPY_FORCE[@]}" \
    "${NUMPY_ARCHIVES[@]}" "${NUMPY_LIBS[@]}" "${WASI}/libpython3.14.a" \
    "${WASI}/Modules/_decimal/libmpdec/libmpdec.a" "${HACL[@]}" \
    "${WASI}/Modules/expat/libexpat.a" "${VFS_LIB}" \
    -ldl -lwasi-emulated-getpid -lwasi-emulated-signal -lwasi-emulated-process-clocks \
    -lpthread -lm -Wl,--export=init -Wl,--export=prefix_begin \
    -Wl,--export=prefix_feed -Wl,--export=alloc -Wl,--export=release \
    -Wl,--export=execute -Wl,--export-memory -Wl,--initial-memory=134217728 \
    -Wl,--max-memory=536870912 -Wl,-z,stack-size=16777216 -Wl,--strip-all \
    -o "${RAW_CORE}"
else
  [[ -f ${RAW_CORE} ]] || { echo "missing reusable raw core: ${RAW_CORE}" >&2; exit 9; }
fi

python3 - "${PY}/Lib" "${BUILD}/vfs" "${ROOT}/guest/bootstrap.py" "${ROOT}/guest/plm.py" "${ROOT}/guest/prefix.py" "${ROOT}/guest/pysolate.py" "${NUMPY_PACKAGE}" <<'PY'
from pathlib import Path
import shutil, sys, os
lib, out, bootstrap, plm, prefix, pysolate, numpy = map(Path, sys.argv[1:])
def copy_tree(src, dst):
    for p in sorted(src.rglob('*')):
        rel=p.relative_to(src); q=dst/rel
        if any(part in {'test', 'tests', '__pycache__'} for part in rel.parts): continue
        if p.is_dir(): q.mkdir(parents=True, exist_ok=True)
        elif p.is_file() and not p.name.endswith(('.pyc', '.pyo')):
            q.parent.mkdir(parents=True, exist_ok=True); shutil.copyfile(p,q); os.utime(q,(0,0))
copy_tree(lib, out)
for src, name in ((bootstrap,'pysolate_bootstrap.py'),(plm,'plm.py'),(prefix,'prefix.py'),(pysolate,'pysolate.py')):
    shutil.copyfile(src, out/name); os.utime(out/name,(0,0))
copy_tree(numpy, out/'site-packages/numpy')
PY
"${HOST_PY}" -S "${ROOT}/tools/precompile-stdlib.py" "${BUILD}/vfs"
"${VFS}" pack "${RAW_CORE}" --dir "${BUILD}/vfs::/usr/lib/python3.14" -o "${DIST}/pysolate.wasm"
"${WASM_TOOLS}" validate "${DIST}/pysolate.wasm"
python3 "${ROOT}/tools/write-artifact-manifest.py" \
  --profile "${AGENT_PROFILE}" --build-inputs "${ROOT}/tools/build-inputs.lock.json" \
  --native-lock "${LOCK}" --raw-core "${RAW_CORE}" --package-tree "${BUILD}/vfs" \
  --artifact "${DIST}/pysolate.wasm" --output "${DIST}/pysolate.manifest.json"
printf 'Built %s\nsha256: ' "${DIST}/pysolate.wasm"
sha256sum "${DIST}/pysolate.wasm" | cut -d' ' -f1
