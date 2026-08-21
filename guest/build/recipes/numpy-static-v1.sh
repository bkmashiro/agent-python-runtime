#!/usr/bin/env bash
set -euo pipefail

: "${ROOT_DIR:?}"
: "${WORK_DIR:?}"
: "${DOWNLOAD_DIR:?}"
: "${CPYTHON_DIR:?}"
: "${WASI_SDK_PATH:?}"
: "${WASI_BUILD_DIR:?}"
: "${SOURCE_DATE_EPOCH:?}"
: "${PACKAGE_PROFILE_LOCK:?}"

HOST_PYTHON="${CPYTHON_DIR}/cross-build/build/python"
NUMPY_SOURCE_DIR="${WORK_DIR}/numpy-source"
CYTHON_SOURCE_DIR="${WORK_DIR}/cython-source"
BUILD_VENV="${WORK_DIR}/numpy-build-venv"
BUILD_ROOT="${WORK_DIR}/numpy-build"
PACKAGE_PARENT="${WORK_DIR}/numpy-package"
PACKAGE_ROOT="${PACKAGE_PARENT}/numpy"
ARCHIVE_ROOT="${WORK_DIR}/numpy-native"
GENERATED_DIR="${WORK_DIR}/generated"
SELECTION_OUTPUT="${WORK_DIR}/extension-profile.json"

if [[ ! -x ${HOST_PYTHON} ]]; then
  echo "CPython host-build interpreter is missing: ${HOST_PYTHON}" >&2
  exit 31
fi
rm -rf "${NUMPY_SOURCE_DIR}" "${CYTHON_SOURCE_DIR}" "${BUILD_VENV}" "${BUILD_ROOT}" "${PACKAGE_PARENT}" "${ARCHIVE_ROOT}"
export HOME="${WORK_DIR}/build-home"
export PIP_CACHE_DIR="${WORK_DIR}/pip-cache"
export TMPDIR="${WORK_DIR}/tmp"
mkdir -p "${NUMPY_SOURCE_DIR}" "${CYTHON_SOURCE_DIR}" "${BUILD_ROOT}" "${PACKAGE_PARENT}" "${ARCHIVE_ROOT}" "${GENERATED_DIR}" "${HOME}" "${PIP_CACHE_DIR}" "${TMPDIR}"
tar -xzf "${DOWNLOAD_DIR}/numpy-source.tar.gz" -C "${NUMPY_SOURCE_DIR}" --strip-components=1
tar -xzf "${DOWNLOAD_DIR}/cython-source.tar.gz" -C "${CYTHON_SOURCE_DIR}" --strip-components=1

"${HOST_PYTHON}" -m venv "${BUILD_VENV}"
"${BUILD_VENV}/bin/python" -m pip install --disable-pip-version-check --no-cache-dir --no-index --no-deps \
  "${DOWNLOAD_DIR}/setuptools-71.1.0-py3-none-any.whl"
"${BUILD_VENV}/bin/python" -m pip install --disable-pip-version-check --no-cache-dir --no-index --no-build-isolation --no-deps \
  "${DOWNLOAD_DIR}/cython-source.tar.gz"

FAKE_CXX="${BUILD_ROOT}/fake_cxx.sh"
cat > "${FAKE_CXX}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
objects=()
i=1
while [[ $i -le $# ]]; do
  arg="${!i}"
  case "${arg}" in
    -o) i=$((i + 1)); output="${!i}" ;;
    *.o) objects+=("${arg}") ;;
  esac
  i=$((i + 1))
done
if [[ -z ${output} || ( ${output} != *.so && ${output} != *.so.* ) ]]; then
  exec "${REAL_CXX}" "$@"
fi
archive="${output%%.cpython-*.so}.a"
if [[ ${archive} == "${output}" ]]; then archive="${output%.so}.a"; fi
if [[ ${#objects[@]} -eq 0 ]]; then
  echo "native extension link had no object inputs: ${output}" >&2
  exit 32
fi
"${WASI_SDK_PATH}/bin/llvm-ar" rcsD "${archive}" "${objects[@]}"
: > "${output}"
EOF
chmod 0755 "${FAKE_CXX}"

mapfile -t TARGET_SYSCONFIG_DATA < <(find "${WASI_BUILD_DIR}" -type f -name '_sysconfigdata_*.py' -print)
if [[ ${#TARGET_SYSCONFIG_DATA[@]} -ne 1 ]]; then
  echo "expected one target sysconfigdata file for NumPy build" >&2
  exit 33
fi
SYSCONFIG_NAME=$(basename "${TARGET_SYSCONFIG_DATA[0]}" .py)
SYSCONFIG_DIR=$(dirname "${TARGET_SYSCONFIG_DATA[0]}")
export CC="${WASI_SDK_PATH}/bin/clang"
export AR="${WASI_SDK_PATH}/bin/llvm-ar"
export RANLIB=true
export REAL_CXX="${WASI_SDK_PATH}/bin/clang++"
export CXX="${FAKE_CXX}"
export LDSHARED="${FAKE_CXX}"
export LDCXXSHARED="${FAKE_CXX}"
export PATH="${BUILD_VENV}/bin:${PATH}"
export PYTHONPATH="${CPYTHON_DIR}/Lib:${SYSCONFIG_DIR}:${WASI_BUILD_DIR}"
export CFLAGS="--target=wasm32-wasip1 --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot -I${CPYTHON_DIR}/Include -I${WASI_BUILD_DIR} -D__EMSCRIPTEN__=1 -DNPY_NO_SIGNAL -ffile-prefix-map=${WORK_DIR}=. -fdebug-prefix-map=${WORK_DIR}=."
export CXXFLAGS="${CFLAGS}"
export LDFLAGS="--target=wasm32-wasip1 --sysroot=${WASI_SDK_PATH}/share/wasi-sysroot"
export _PYTHON_SYSCONFIGDATA_NAME="${SYSCONFIG_NAME}"
export NPY_DISABLE_SVML=1
export NPY_BLAS_ORDER=
export NPY_LAPACK_ORDER=
export SOURCE_DATE_EPOCH
export PYTHONHASHSEED=0

(
  cd "${NUMPY_SOURCE_DIR}"
  "${BUILD_VENV}/bin/python" setup.py build --disable-optimization -j 4 --build-base "${BUILD_ROOT}/build"
)
mapfile -t BUILD_LIB_DIRS < <(find "${BUILD_ROOT}/build" -maxdepth 1 -type d -name 'lib.*' -print)
if [[ ${#BUILD_LIB_DIRS[@]} -ne 1 || ! -d ${BUILD_LIB_DIRS[0]}/numpy ]]; then
  echo "NumPy build did not produce one package tree" >&2
  exit 34
fi
python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" "${BUILD_LIB_DIRS[0]}/numpy" "${ARCHIVE_ROOT}/numpy" --epoch "${SOURCE_DATE_EPOCH}"
mkdir -p "${ARCHIVE_ROOT}/numpy/lib"
for support_name in npymath npyrandom; do
  mapfile -t SUPPORT_CANDIDATES < <(find "${BUILD_ROOT}/build" -type f -name "lib${support_name}.a" -print | LC_ALL=C sort)
  if [[ ${#SUPPORT_CANDIDATES[@]} -lt 1 ]]; then
    echo "NumPy build did not produce lib${support_name}.a" >&2
    exit 36
  fi
  expected_support_sha=$(sha256sum "${SUPPORT_CANDIDATES[0]}" | cut -d' ' -f1)
  for candidate in "${SUPPORT_CANDIDATES[@]}"; do
    if [[ $(sha256sum "${candidate}" | cut -d' ' -f1) != "${expected_support_sha}" ]]; then
      echo "NumPy support archive identity is ambiguous: lib${support_name}.a" >&2
      exit 37
    fi
  done
  install -m 0644 "${SUPPORT_CANDIDATES[0]}" "${ARCHIVE_ROOT}/numpy/lib/lib${support_name}.a"
  touch -d "@${SOURCE_DATE_EPOCH}" "${ARCHIVE_ROOT}/numpy/lib/lib${support_name}.a"
done
python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" "${BUILD_LIB_DIRS[0]}/numpy" "${PACKAGE_ROOT}" --epoch "${SOURCE_DATE_EPOCH}"
find "${PACKAGE_ROOT}" -type f \( -name '*.a' -o -name '*.so' -o -name '*.pyc' \) -delete
find "${PACKAGE_ROOT}" -type d -name '__pycache__' -empty -delete

python3 "${ROOT_DIR}/guest/build/native_package_profile.py" prepare \
  --lock "${PACKAGE_PROFILE_LOCK}" --package-root "${PACKAGE_ROOT}" \
  --archive-root "${ARCHIVE_ROOT}" --selection-output "${SELECTION_OUTPUT}"
python3 "${ROOT_DIR}/guest/build/native_package_profile.py" registration-header \
  --lock "${PACKAGE_PROFILE_LOCK}" --output "${GENERATED_DIR}/builtin-registry.h"
