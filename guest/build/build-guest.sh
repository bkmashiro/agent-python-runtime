#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK_DIR=${AGENT_RUNTIME_BUILD_DIR:-"${ROOT_DIR}/build/guest"}
DIST_DIR=${AGENT_RUNTIME_DIST_DIR:-"${ROOT_DIR}/dist"}
DOWNLOAD_DIR="${WORK_DIR}/downloads"
TOOLS_DIR="${WORK_DIR}/tools"
CPYTHON_DIR="${WORK_DIR}/cpython"
SOURCE_LOCK="${ROOT_DIR}/guest/build/sources.lock.json"
PACKAGE_PROFILE_TOOL="${ROOT_DIR}/guest/build/package_profile.py"
BUILD_CACHE_ROOT=${AGENT_RUNTIME_BUILD_CACHE_ROOT:-}
BUILD_CACHE_MODE=${AGENT_RUNTIME_BUILD_CACHE_MODE:-off}
SOURCE_TREE_ID=${AGENT_RUNTIME_SOURCE_TREE:-}
ARTIFACT_PROFILE=${AGENT_RUNTIME_ARTIFACT_PROFILE:-base}
ARTIFACT_FILENAME=$(python3 "${PACKAGE_PROFILE_TOOL}" field --profile "${ARTIFACT_PROFILE}" --name artifact_filename)
PACKAGE_PROFILE_KIND=$(python3 "${PACKAGE_PROFILE_TOOL}" field --profile "${ARTIFACT_PROFILE}" --name kind)
PACKAGE_PROFILE_LOCK_NAME=$(python3 "${PACKAGE_PROFILE_TOOL}" field --profile "${ARTIFACT_PROFILE}" --name lock)
PACKAGE_PROFILE_RECIPE=$(python3 "${PACKAGE_PROFILE_TOOL}" field --profile "${ARTIFACT_PROFILE}" --name recipe)
PACKAGE_PROFILE_LOCK=""
EXTENSIONS_LOCK=""
EXTENSION_PATCH=""
if [[ -n ${PACKAGE_PROFILE_LOCK_NAME} ]]; then
  PACKAGE_PROFILE_LOCK="${ROOT_DIR}/guest/build/profiles/${PACKAGE_PROFILE_LOCK_NAME}"
  EXTENSIONS_LOCK="${PACKAGE_PROFILE_LOCK}"
fi
if [[ ${PACKAGE_PROFILE_RECIPE} == attrs-770-v1 ]]; then
  EXTENSION_PATCH=${AGENT_RUNTIME_EXTENSION_PATCH:-}
  if [[ -z ${EXTENSION_PATCH} || ! -f ${EXTENSION_PATCH} ]]; then
    echo "attrs-770 profile requires AGENT_RUNTIME_EXTENSION_PATCH" >&2
    exit 1
  fi
fi
INITIAL_MEMORY_BYTES=134217728
MAX_MEMORY_BYTES=536870912
if [[ -z ${AGENT_RUNTIME_MEMORY_MODEL+x} ]]; then
  AGENT_RUNTIME_MEMORY_MODEL="growable"
fi
case "${AGENT_RUNTIME_MEMORY_MODEL}" in
  growable)
    ;;
  cow-fixed)
    MAX_MEMORY_BYTES="${INITIAL_MEMORY_BYTES}"
    ;;
  *)
    echo "AGENT_RUNTIME_MEMORY_MODEL must be growable or cow-fixed" >&2
    exit 11
    ;;
esac
MEMORY_INITIAL_PAGES=$((INITIAL_MEMORY_BYTES / 65536))
MEMORY_MAXIMUM_PAGES=$((MAX_MEMORY_BYTES / 65536))

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "build-guest.sh currently requires Linux x86_64" >&2
  exit 2
fi
if [[ ${PACKAGE_PROFILE_RECIPE} == attrs-770-v1 ]]; then
  python3 "${ROOT_DIR}/guest/build/extension_profile.py" verify-patch \
    --lock "${PACKAGE_PROFILE_LOCK}" --patch "${EXTENSION_PATCH}"
fi
case "${BUILD_CACHE_MODE}" in
  off|auto|refresh) ;;
  *)
    echo "AGENT_RUNTIME_BUILD_CACHE_MODE must be off, auto, or refresh" >&2
    exit 19
    ;;
esac
if [[ ${BUILD_CACHE_MODE} != off && ( -z ${BUILD_CACHE_ROOT} || ${BUILD_CACHE_ROOT} != /* ) ]]; then
  echo "AGENT_RUNTIME_BUILD_CACHE_ROOT must be an absolute path when cache is enabled" >&2
  exit 20
fi

rm -rf "${WORK_DIR}" "${DIST_DIR}"
mkdir -p "${DOWNLOAD_DIR}" "${TOOLS_DIR}" "${DIST_DIR}"

if [[ -z ${SOURCE_DATE_EPOCH:-} ]]; then
  SOURCE_DATE_EPOCH=$(python3 "${ROOT_DIR}/tools/source_date_epoch.py" --repository "${ROOT_DIR}" HEAD)
fi
if [[ -z ${GITHUB_SHA:-} ]]; then
  GITHUB_SHA=$(git -C "${ROOT_DIR}" rev-parse HEAD)
fi
export GITHUB_SHA
if [[ ! ${SOURCE_DATE_EPOCH} =~ ^[1-9][0-9]*$ ]]; then
  echo "SOURCE_DATE_EPOCH must be a positive integer" >&2
  exit 6
fi
export SOURCE_DATE_EPOCH

fetch() {
  local source_id=$1
  local filename=$2
  local lock=${3:-${SOURCE_LOCK}}
  python3 "${ROOT_DIR}/tools/fetch_locked_source.py" "${source_id}" "${DOWNLOAD_DIR}/${filename}" \
    --lock "${lock}"
}

# BEGIN CPYTHON CACHE RECIPE
cache_entry_is_regular() {
  local entry=$1
  shift
  [[ -d ${entry} && ! -L ${entry} ]] || return 1
  local name
  for name in "$@"; do
    [[ -f ${entry}/${name} && ! -L ${entry}/${name} ]] || return 1
  done
}

BUILD_CACHE_KEY=$(python3 "${ROOT_DIR}/guest/build/cache_identity.py" --repository "${ROOT_DIR}")
BUILD_CACHE_STATUS=off
BUILD_CACHE_LAYER_SHA256=""
BUILD_CACHE_ENTRY=""
if [[ ${BUILD_CACHE_MODE} != off ]]; then
  if [[ -L ${BUILD_CACHE_ROOT} ]]; then
    echo "Guest build cache root must be a real directory" >&2
    exit 21
  fi
  mkdir -p "${BUILD_CACHE_ROOT}"
  if [[ ! -d ${BUILD_CACHE_ROOT} ]]; then
    echo "Guest build cache root must be a real directory" >&2
    exit 21
  fi
  chmod 0700 "${BUILD_CACHE_ROOT}"
  BUILD_CACHE_ENTRY="${BUILD_CACHE_ROOT}/${BUILD_CACHE_KEY#sha256:}"
  exec 9>"${BUILD_CACHE_ROOT}/.publish.lock"
  flock 9
  if [[ ${BUILD_CACHE_MODE} == refresh ]]; then
    rm -rf "${BUILD_CACHE_ENTRY}"
  fi
  if cache_entry_is_regular "${BUILD_CACHE_ENTRY}" RESULT.READY layer.tar SHA256SUMS &&
    grep -Fxq "cache_key=${BUILD_CACHE_KEY}" "${BUILD_CACHE_ENTRY}/RESULT.READY" &&
    (cd "${BUILD_CACHE_ENTRY}" && sha256sum -c SHA256SUMS >/dev/null) &&
    python3 "${ROOT_DIR}/guest/build/validate_cache_layer.py" "${BUILD_CACHE_ENTRY}/layer.tar"; then
    tar xf "${BUILD_CACHE_ENTRY}/layer.tar" -C "${WORK_DIR}"
    BUILD_CACHE_LAYER_SHA256="sha256:$(sha256sum "${BUILD_CACHE_ENTRY}/layer.tar" | cut -d' ' -f1)"
    BUILD_CACHE_STATUS=hit
  else
    BUILD_CACHE_STATUS=miss
  fi
fi

if [[ ${BUILD_CACHE_STATUS} != hit ]]; then
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
fi

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
WASMTIME_DIR=$(dirname "${WASMTIME}")
export PATH="${WASMTIME_DIR}:${PATH}"

if [[ ${BUILD_CACHE_STATUS} != hit ]]; then
  (
    cd "${CPYTHON_DIR}"
    python3 Tools/wasm/wasi build --wasi-sdk "${WASI_SDK_PATH}"
  )
fi

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

if [[ ${BUILD_CACHE_MODE} != off && ${BUILD_CACHE_STATUS} == miss ]]; then
  BUILD_CACHE_TEMP=$(mktemp -d "${BUILD_CACHE_ROOT}/.publish.XXXXXXXX")
  trap 'rm -rf "${BUILD_CACHE_TEMP:-}"' EXIT
  tar cf "${BUILD_CACHE_TEMP}/layer.tar" -C "${WORK_DIR}" downloads tools cpython
  python3 "${ROOT_DIR}/guest/build/validate_cache_layer.py" "${BUILD_CACHE_TEMP}/layer.tar"
  (
    cd "${BUILD_CACHE_TEMP}"
    sha256sum layer.tar > SHA256SUMS
  )
  BUILD_CACHE_LAYER_SHA256="sha256:$(sha256sum "${BUILD_CACHE_TEMP}/layer.tar" | cut -d' ' -f1)"
  printf 'cache_key=%s\nlayer_sha256=%s\n' "${BUILD_CACHE_KEY}" "${BUILD_CACHE_LAYER_SHA256}" > "${BUILD_CACHE_TEMP}/RESULT.READY"
  rm -rf "${BUILD_CACHE_ENTRY}"
  mv "${BUILD_CACHE_TEMP}" "${BUILD_CACHE_ENTRY}"
  BUILD_CACHE_TEMP=""
  trap - EXIT
fi
if [[ ${BUILD_CACHE_MODE} != off ]]; then
  python3 "${ROOT_DIR}/guest/build/cache_maintenance.py" "${BUILD_CACHE_ROOT}" \
    --protect "${BUILD_CACHE_KEY#sha256:}" --keep 2
  flock -u 9
  exec 9>&-
fi
# END CPYTHON CACHE RECIPE

write_build_cache_evidence() {
  python3 - "${DIST_DIR}/build-cache.json" "${BUILD_CACHE_KEY}" "${BUILD_CACHE_STATUS}" "${BUILD_CACHE_LAYER_SHA256}" "${FINAL_CACHE_KEY:-}" "${FINAL_CACHE_STATUS:-off}" <<'PY'
import json
import pathlib
import sys

output, key, disposition, layer_sha256, final_key, final_disposition = sys.argv[1:]
if disposition not in {"off", "hit", "miss"} or final_disposition not in {"off", "hit", "miss"}:
    raise SystemExit("invalid build cache disposition")
if disposition != "off" and not layer_sha256.startswith("sha256:"):
    raise SystemExit("cached build must bind its layer digest")
if final_disposition != "off" and not final_key.startswith("sha256:"):
    raise SystemExit("final cache must bind its exact identity")
payload = {
    "schema_version": "pysolate.guest-build-cache-evidence.v1",
    "cache_key": key,
    "disposition": disposition,
    "layer_sha256": layer_sha256 or None,
    "final_cache_key": final_key or None,
    "final_cache_disposition": final_disposition,
}
pathlib.Path(output).write_text(json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n")
PY
}

write_dist_checksums() {
  (
    cd "${DIST_DIR}"
    local sum_files=(
      "${ARTIFACT_FILENAME}"
      manifest.json
      import-inventory.json
      import-qualification.json
      build-cache.json
      sbom.spdx.json
      THIRD_PARTY_NOTICES.md
    )
    sha256sum "${sum_files[@]}" > SHA256SUMS
  )
}

PROBE_RUNNER=${AGENT_RUNTIME_PROBE_RUNNER:-"${WORK_DIR}/apyrun-probe"}
FINAL_CACHE_ELIGIBLE=1
if [[ -n ${AGENT_RUNTIME_PROBE_RUNNER:-} ]]; then
  FINAL_CACHE_ELIGIBLE=0
else
  (
    cd "${ROOT_DIR}"
    go build -trimpath -buildvcs=false -o "${PROBE_RUNNER}" ./cmd/apyrun
  )
fi
if [[ ! -x ${PROBE_RUNNER} ]]; then
  echo "probe runner is not executable: ${PROBE_RUNNER}" >&2
  exit 10
fi
PROBE_RUNNER_SHA256="sha256:$(sha256sum "${PROBE_RUNNER}" | cut -d' ' -f1)"
EFFECTIVE_SOURCE_LOCK="${WORK_DIR}/effective-sources.lock.json"
if [[ ${PACKAGE_PROFILE_RECIPE} == attrs-770-v1 ]]; then
  python3 "${ROOT_DIR}/guest/build/extension_profile.py" effective-source-lock \
    --lock "${PACKAGE_PROFILE_LOCK}" --source-lock "${SOURCE_LOCK}" --output "${EFFECTIVE_SOURCE_LOCK}"
else
  cp "${SOURCE_LOCK}" "${EFFECTIVE_SOURCE_LOCK}"
fi

FINAL_CACHE_STATUS=off
FINAL_CACHE_KEY=""
FINAL_CACHE_ENTRY=""
FINAL_CACHE_TMP=""
FINAL_CACHE_LOCKED=0
EXTENSIONS_LOCK_SHA256=""
if [[ -n ${EXTENSIONS_LOCK:-} ]]; then
  EXTENSIONS_LOCK_SHA256="sha256:$(sha256sum "${EXTENSIONS_LOCK}" | cut -d' ' -f1)"
fi
if [[ -n ${BUILD_CACHE_ROOT} && ${BUILD_CACHE_MODE} != off && ${FINAL_CACHE_ELIGIBLE} -eq 1 &&
  ${SOURCE_TREE_ID} =~ ^[0-9a-f]{40}$ && ${GITHUB_SHA} =~ ^[0-9a-f]{40}$ ]]; then
  FINAL_CACHE_KEY=$(python3 "${ROOT_DIR}/guest/build/cache_identity.py" --repository "${ROOT_DIR}" --final \
    --layer-key "${BUILD_CACHE_KEY}" --source-commit "${GITHUB_SHA}" --source-tree "${SOURCE_TREE_ID}" \
    --source-epoch "${SOURCE_DATE_EPOCH}" --probe-runner-sha256 "${PROBE_RUNNER_SHA256}" \
    --artifact-profile "${ARTIFACT_PROFILE}" --artifact-filename "${ARTIFACT_FILENAME}" \
    --extensions-lock-sha256 "${EXTENSIONS_LOCK_SHA256}" --initial-memory-bytes "${INITIAL_MEMORY_BYTES}" \
    --max-memory-bytes "${MAX_MEMORY_BYTES}")
  FINAL_CACHE_ROOT="${BUILD_CACHE_ROOT}/final"
  FINAL_CACHE_ENTRY="${FINAL_CACHE_ROOT}/${FINAL_CACHE_KEY#sha256:}"
  if [[ -L ${FINAL_CACHE_ROOT} ]]; then
    echo "final cache root must be a real directory" >&2
    exit 18
  fi
  mkdir -p "${FINAL_CACHE_ROOT}"
  if [[ ! -d ${FINAL_CACHE_ROOT} ]]; then
    echo "final cache root must be a real directory" >&2
    exit 18
  fi
  chmod 0700 "${FINAL_CACHE_ROOT}"
  exec 8>"${FINAL_CACHE_ROOT}/.lock"
  flock 8
  FINAL_CACHE_LOCKED=1
  cleanup_final_cache() {
    [[ -z ${FINAL_CACHE_TMP:-} ]] || rm -rf "${FINAL_CACHE_TMP}"
    if [[ ${FINAL_CACHE_LOCKED:-0} -eq 1 ]]; then
      flock -u 8 || true
      exec 8>&- || true
    fi
  }
  trap cleanup_final_cache EXIT
  if [[ ${BUILD_CACHE_MODE} == refresh ]]; then
    rm -rf "${FINAL_CACHE_ENTRY}"
  fi
  if cache_entry_is_regular "${FINAL_CACHE_ENTRY}" RESULT.READY dist.tar SHA256SUMS &&
    grep -Fxq "final_cache_key=${FINAL_CACHE_KEY}" "${FINAL_CACHE_ENTRY}/RESULT.READY" &&
    grep -Fxq "source_tree=${SOURCE_TREE_ID}" "${FINAL_CACHE_ENTRY}/RESULT.READY" &&
    grep -Fxq "source_commit=${GITHUB_SHA}" "${FINAL_CACHE_ENTRY}/RESULT.READY" &&
    grep -Fxq "probe_runner_sha256=${PROBE_RUNNER_SHA256}" "${FINAL_CACHE_ENTRY}/RESULT.READY" &&
    (cd "${FINAL_CACHE_ENTRY}" && sha256sum -c SHA256SUMS >/dev/null) &&
    python3 "${ROOT_DIR}/guest/build/validate_cache_layer.py" "${FINAL_CACHE_ENTRY}/dist.tar" --root dist --regular-only; then
    rm -rf "${DIST_DIR}"
    tar -xf "${FINAL_CACHE_ENTRY}/dist.tar" -C "${ROOT_DIR}"
    restored_artifact_sha256="sha256:$(sha256sum "${DIST_DIR}/${ARTIFACT_FILENAME}" | cut -d' ' -f1)"
    if grep -Fxq "artifact_sha256=${restored_artifact_sha256}" "${FINAL_CACHE_ENTRY}/RESULT.READY" &&
      python3 "${ROOT_DIR}/guest/build/verify-artifact.py" \
        "${DIST_DIR}/${ARTIFACT_FILENAME}" "${DIST_DIR}/manifest.json" &&
      python3 "${ROOT_DIR}/guest/build/write-supply-chain.py" \
        --artifact "${DIST_DIR}/${ARTIFACT_FILENAME}" --manifest "${DIST_DIR}/manifest.json" \
        --source-lock "${EFFECTIVE_SOURCE_LOCK}" --sbom "${DIST_DIR}/sbom.spdx.json" \
        --notices "${DIST_DIR}/THIRD_PARTY_NOTICES.md" --verify; then
      FINAL_CACHE_STATUS=hit
      write_build_cache_evidence
      write_dist_checksums
      python3 "${ROOT_DIR}/guest/build/cache_maintenance.py" "${FINAL_CACHE_ROOT}" --protect "${FINAL_CACHE_KEY#sha256:}" --keep 2
      flock -u 8
      exec 8>&-
      FINAL_CACHE_LOCKED=0
      trap - EXIT
      printf 'guest artifact: %s\n' "${DIST_DIR}/${ARTIFACT_FILENAME}"
      printf 'artifact sha256: %s\n' "${restored_artifact_sha256#sha256:}"
      exit 0
    fi
    rm -rf "${DIST_DIR}" "${FINAL_CACHE_ENTRY}"
  fi
  FINAL_CACHE_STATUS=miss
fi

EXTENSION_SELECTION="${WORK_DIR}/extension-profile.json"
EXTENSION_SOURCE_LOCK="${WORK_DIR}/extension-sources.lock.json"
ATTRS_SOURCE_DIR="${WORK_DIR}/attrs-source"
if [[ ${PACKAGE_PROFILE_RECIPE} == attrs-770-v1 ]]; then
  python3 "${ROOT_DIR}/guest/build/extension_profile.py" source-lock \
    --lock "${PACKAGE_PROFILE_LOCK}" --output "${EXTENSION_SOURCE_LOCK}"
  fetch attrs-source attrs-source.tar.gz "${EXTENSION_SOURCE_LOCK}"
  rm -rf "${ATTRS_SOURCE_DIR}"
  mkdir -p "${ATTRS_SOURCE_DIR}"
  tar -xzf "${DOWNLOAD_DIR}/attrs-source.tar.gz" -C "${ATTRS_SOURCE_DIR}" --strip-components=1
  (
    cd "${WORK_DIR}"
    git apply --check --directory=attrs-source "${EXTENSION_PATCH}"
    git apply --directory=attrs-source "${EXTENSION_PATCH}"
  )
  python3 "${ROOT_DIR}/guest/build/extension_profile.py" prepare \
    --lock "${PACKAGE_PROFILE_LOCK}" --package-root "${ATTRS_SOURCE_DIR}/src/attr" \
    --source-lock "${SOURCE_LOCK}" --selection-output "${EXTENSION_SELECTION}" \
    --effective-source-lock-output "${EFFECTIVE_SOURCE_LOCK}"
fi

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
  -ffile-prefix-map="${ROOT_DIR}"=. -fdebug-prefix-map="${ROOT_DIR}"=. \
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
  -ffile-prefix-map="${ROOT_DIR}"=. -fdebug-prefix-map="${ROOT_DIR}"=. \
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
if [[ ${PACKAGE_PROFILE_RECIPE} == attrs-770-v1 ]]; then
  python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
    "${ATTRS_SOURCE_DIR}/src/attr" \
    "${VFS_PYTHON_DIR}/site-packages/attr" \
    --epoch "${SOURCE_DATE_EPOCH}"
  python3 "${ROOT_DIR}/guest/build/extension_profile.py" verify-tree \
    --lock "${PACKAGE_PROFILE_LOCK}" --package-root "${VFS_PYTHON_DIR}/site-packages/attr"
fi

pack_guest() {
  local output=$1
  "${WASI_VFS}" pack "${RAW_GUEST}" \
    --dir "${VFS_PYTHON_DIR}::/usr/lib/python3.14" \
    -o "${output}"
}

pack_guest "${FINAL_GUEST}"

"${WASM_TOOLS}" validate "${FINAL_GUEST}"
"${WASM_TOOLS}" print "${FINAL_GUEST}" > "${DIST_DIR}/agent-python-runtime.wat"
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
MANIFEST_EXTENSION_ARGS=()
if [[ ${PACKAGE_PROFILE_RECIPE} == attrs-770-v1 ]]; then
  MANIFEST_EXTENSION_ARGS=(--extension-selection "${EXTENSION_SELECTION}")
fi
python3 "${ROOT_DIR}/guest/build/write-manifest.py" \
  --artifact "${FINAL_GUEST}" \
  --wat "${DIST_DIR}/agent-python-runtime.wat" \
  --source-lock "${EFFECTIVE_SOURCE_LOCK}" \
  --artifact-profile "${ARTIFACT_PROFILE}" \
  "${MANIFEST_EXTENSION_ARGS[@]}" \
  --import-inventory "${IMPORT_INVENTORY}" \
  --import-qualification "${IMPORT_QUALIFICATION}" \
  --memory-initial-pages "${MEMORY_INITIAL_PAGES}" \
  --memory-maximum-pages "${MEMORY_MAXIMUM_PAGES}" \
  --output "${DIST_DIR}/manifest.json"
python3 "${ROOT_DIR}/guest/build/verify-artifact.py" \
  "${FINAL_GUEST}" "${DIST_DIR}/manifest.json" \
  "${MANIFEST_EXTENSION_ARGS[@]}"

python3 "${ROOT_DIR}/guest/build/write-supply-chain.py" \
  --artifact "${FINAL_GUEST}" \
  --manifest "${DIST_DIR}/manifest.json" \
  --source-lock "${EFFECTIVE_SOURCE_LOCK}" \
  --vfs-root "${VFS_PYTHON_DIR}" \
  --sbom "${DIST_DIR}/sbom.spdx.json" \
  --notices "${DIST_DIR}/THIRD_PARTY_NOTICES.md"
python3 "${ROOT_DIR}/guest/build/write-supply-chain.py" \
  --artifact "${FINAL_GUEST}" \
  --manifest "${DIST_DIR}/manifest.json" \
  --source-lock "${EFFECTIVE_SOURCE_LOCK}" \
  --vfs-root "${VFS_PYTHON_DIR}" \
  --sbom "${DIST_DIR}/sbom.spdx.json" \
  --notices "${DIST_DIR}/THIRD_PARTY_NOTICES.md" \
  --verify
rm "${DIST_DIR}/agent-python-runtime.wat"
write_build_cache_evidence
write_dist_checksums

if [[ ${FINAL_CACHE_STATUS} == miss ]]; then
  FINAL_CACHE_TMP=$(mktemp -d "${FINAL_CACHE_ROOT}/.tmp.${FINAL_CACHE_KEY#sha256:}.XXXXXXXX")
  tar -cf "${FINAL_CACHE_TMP}/dist.tar" -C "${ROOT_DIR}" dist
  python3 "${ROOT_DIR}/guest/build/validate_cache_layer.py" "${FINAL_CACHE_TMP}/dist.tar" --root dist --regular-only
  (
    cd "${FINAL_CACHE_TMP}"
    sha256sum dist.tar > SHA256SUMS
  )
  {
    printf 'final_cache_key=%s\n' "${FINAL_CACHE_KEY}"
    printf 'source_tree=%s\n' "${SOURCE_TREE_ID}"
    printf 'source_commit=%s\n' "${GITHUB_SHA}"
    printf 'probe_runner_sha256=%s\n' "${PROBE_RUNNER_SHA256}"
    printf 'artifact_sha256=sha256:%s\n' "$(sha256sum "${FINAL_GUEST}" | cut -d' ' -f1)"
  } > "${FINAL_CACHE_TMP}/RESULT.READY"
  rm -rf "${FINAL_CACHE_ENTRY}"
  mv "${FINAL_CACHE_TMP}" "${FINAL_CACHE_ENTRY}"
  FINAL_CACHE_TMP=""
  python3 "${ROOT_DIR}/guest/build/cache_maintenance.py" "${FINAL_CACHE_ROOT}" --protect "${FINAL_CACHE_KEY#sha256:}" --keep 2
  flock -u 8
  exec 8>&-
  FINAL_CACHE_LOCKED=0
  trap - EXIT
fi

printf 'guest artifact: %s\n' "${FINAL_GUEST}"
printf 'artifact sha256: '
sha256sum "${FINAL_GUEST}" | cut -d' ' -f1
