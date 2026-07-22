#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK_DIR=${AGENT_RUNTIME_BUILD_DIR:?AGENT_RUNTIME_BUILD_DIR must point at a completed guest build}
PROBE_DIR=${NUMPY_WASI_PROBE_DIR:-"${RUNNER_TEMP:-/tmp}/numpy-wasi-probe"}
LOCK="${ROOT_DIR}/experiments/numpy-wasi/sources.lock.json"
CPYTHON_DIR="${WORK_DIR}/cpython"
WASI_BUILD_DIR="${CPYTHON_DIR}/cross-build/wasm32-wasip1"
WASI_SDK_PATH="${WORK_DIR}/tools/wasi-sdk"
BASE_VFS_DIR=${AGENT_RUNTIME_VFS_ROOT:-"${WORK_DIR}/vfs-python"}
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(python3 "${ROOT_DIR}/tools/source_date_epoch.py" HEAD)}
mapfile -t WASI_VFS_TOOLS < <(find "${WORK_DIR}/tools/wasi-vfs-cli" -type f -name wasi-vfs -perm -u+x -print)
mapfile -t WASI_VFS_LIBS < <(find "${WORK_DIR}/tools/wasi-vfs-lib" -type f -name '*.a' -print)
if [[ ${#WASI_VFS_TOOLS[@]} -ne 1 || ${#WASI_VFS_LIBS[@]} -ne 1 ]]; then
  echo "expected exactly one wasi-vfs tool and static library" >&2
  exit 9
fi
WASI_VFS=${WASI_VFS_TOOLS[0]}
WASI_VFS_LIB=${WASI_VFS_LIBS[0]}

rm -rf "${PROBE_DIR}"
mkdir -p "${PROBE_DIR}/downloads" "${PROBE_DIR}/numpy" "${PROBE_DIR}/cython" "${PROBE_DIR}/logs"
python3 "${ROOT_DIR}/tools/verify_sources_lock.py" "${LOCK}"
python3 "${ROOT_DIR}/tools/fetch_locked_source.py" numpy-source \
  "${PROBE_DIR}/downloads/numpy.tar.gz" --lock "${LOCK}"
python3 "${ROOT_DIR}/tools/fetch_locked_source.py" cython-host-wheel-linux-x86_64-cp313 \
  "${PROBE_DIR}/downloads/cython.whl" --lock "${LOCK}"
tar xzf "${PROBE_DIR}/downloads/numpy.tar.gz" -C "${PROBE_DIR}/numpy" --strip-components=1
python3 -m zipfile -e "${PROBE_DIR}/downloads/cython.whl" "${PROBE_DIR}/cython"

mapfile -t TARGET_PYTHONS < <(find "${WASI_BUILD_DIR}" -maxdepth 1 -type f -name 'python*.sh' -print)
if [[ ${#TARGET_PYTHONS[@]} -ne 1 ]]; then
  echo "expected exactly one CPython target wrapper, found ${#TARGET_PYTHONS[@]}" >&2
  exit 10
fi
TARGET_PYTHON=${TARGET_PYTHONS[0]}
TARGET_PYTHON_ADAPTER_DIR="${PROBE_DIR}/target-python-adapter"
TARGET_PYTHON_ADAPTER="${TARGET_PYTHON_ADAPTER_DIR}/python"
TARGET_PYTHON_SCRIPT_DIR="${WORK_DIR}/cpython/.numpy-wasi-probe"
TARGET_PYTHON_SCRIPT_GUEST="/.numpy-wasi-probe/python_info.py"
mkdir -p "${TARGET_PYTHON_ADAPTER_DIR}" "${TARGET_PYTHON_SCRIPT_DIR}"
cat >"${TARGET_PYTHON_ADAPTER}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ $# -eq 1 && -f $1 && ${1##*/} == python_info.py ]]; then
  cp -- "$1" "${TARGET_PYTHON_SCRIPT_DIR}/python_info.py"
  exec "${TARGET_PYTHON}" "${TARGET_PYTHON_SCRIPT_GUEST}"
fi
exec "${TARGET_PYTHON}" "$@"
EOF
chmod 0755 "${TARGET_PYTHON_ADAPTER}"
for required in \
  "${TARGET_PYTHON}" \
  "${WASI_SDK_PATH}/bin/clang" \
  "${WASI_SDK_PATH}/bin/clang++" \
  "${WASI_SDK_PATH}/bin/llvm-ar" \
  "${PROBE_DIR}/numpy/vendored-meson/meson/meson.py" \
  "${PROBE_DIR}/cython/cython.py" \
  "${ROOT_DIR}/tools/archive_wasm_extension.py" \
  "${ROOT_DIR}/tools/stage_numpy_wasi_package.py" \
  "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
  "${WASI_VFS}" \
  "${WASI_VFS_LIB}" \
  "${BASE_VFS_DIR}"; do
  if [[ ! -e ${required} ]]; then
    echo "missing probe input: ${required}" >&2
    exit 11
  fi
done

CYTHON_WRAPPER_DIR="${PROBE_DIR}/host-bin"
CYTHON_WRAPPER="${CYTHON_WRAPPER_DIR}/cython"
mkdir -p "${CYTHON_WRAPPER_DIR}"
cat >"${CYTHON_WRAPPER}" <<EOF
#!/usr/bin/env bash
PYTHONPATH="${PROBE_DIR}/cython" exec python3 "${PROBE_DIR}/cython/cython.py" "\$@"
EOF
chmod 0755 "${CYTHON_WRAPPER}"

STATIC_WRAPPER_DIR="${PROBE_DIR}/static-link-wrappers"
STATIC_MANIFEST_DIR="${PROBE_DIR}/static-extension-manifests"
STATIC_C_WRAPPER="${STATIC_WRAPPER_DIR}/clang"
STATIC_CXX_WRAPPER="${STATIC_WRAPPER_DIR}/clang++"
mkdir -p "${STATIC_WRAPPER_DIR}" "${STATIC_MANIFEST_DIR}"
cat >"${STATIC_C_WRAPPER}" <<EOF
#!/usr/bin/env bash
exec python3 "${ROOT_DIR}/tools/archive_wasm_extension.py" \\
  --real-compiler "${WASI_SDK_PATH}/bin/clang" \\
  --archiver "${WASI_SDK_PATH}/bin/llvm-ar" \\
  --manifest-dir "${STATIC_MANIFEST_DIR}" \\
  --build-root "${PROBE_DIR}/build" -- "\$@"
EOF
cat >"${STATIC_CXX_WRAPPER}" <<EOF
#!/usr/bin/env bash
extra_args=()
for arg in "\$@"; do
  case "\${arg}" in
    */numpy/_core/src/multiarray/unique.cpp) extra_args=(-fno-exceptions) ;;
  esac
done
exec python3 "${ROOT_DIR}/tools/archive_wasm_extension.py" \\
  --real-compiler "${WASI_SDK_PATH}/bin/clang++" \\
  --archiver "${WASI_SDK_PATH}/bin/llvm-ar" \\
  --manifest-dir "${STATIC_MANIFEST_DIR}" \\
  --build-root "${PROBE_DIR}/build" -- "\$@" "\${extra_args[@]}"
EOF
chmod 0755 "${STATIC_C_WRAPPER}" "${STATIC_CXX_WRAPPER}"

PKG_CONFIG_BIN=$(command -v pkg-config)
TARGET_PKGCONFIG_DIR="${PROBE_DIR}/target-pkgconfig"
mkdir -p "${TARGET_PKGCONFIG_DIR}"
cat >"${TARGET_PKGCONFIG_DIR}/python3.pc" <<EOF
prefix=${WORK_DIR}/cpython
includedir=\${prefix}/Include
buildincludedir=${WASI_BUILD_DIR}

Name: Python target development headers
Description: CPython 3.14 wasm32-wasip1 compile-only dependency
Version: 3.14.0
Cflags: -I\${includedir} -I\${buildincludedir}
Libs:
EOF
cp "${TARGET_PKGCONFIG_DIR}/python3.pc" "${TARGET_PKGCONFIG_DIR}/python-3.14.pc"

export PROBE_DIR TARGET_PYTHON TARGET_PYTHON_ADAPTER TARGET_PYTHON_SCRIPT_DIR TARGET_PYTHON_SCRIPT_GUEST
python3 - <<'PY'
import os
from pathlib import Path

meson_build = Path(os.environ["PROBE_DIR"]) / "numpy" / "meson.build"
text = meson_build.read_text()
old = "py = import('python').find_installation(pure: false)"
if text.count(old) != 1:
    raise SystemExit("expected exactly one NumPy target-Python Meson seam")
target_python = os.environ["TARGET_PYTHON_ADAPTER"]
new = f"py = import('python').find_installation({target_python!r}, pure: false)"
meson_build.write_text(text.replace(old, new))

core_meson = Path(os.environ["PROBE_DIR"]) / "numpy" / "numpy" / "_core" / "meson.build"
core_text = core_meson.read_text()
anchor = "codegen_env = {"
command_seam = "command: [py, '-m', 'code_generators."
if core_text.count(anchor) != 1:
    raise SystemExit("expected exactly one NumPy codegen environment seam")
if core_text.count(command_seam) != 4:
    raise SystemExit("expected exactly four NumPy host codegen command seams")
core_text = core_text.replace(anchor, "build_py = find_program('python3', native: true)\n\n" + anchor)
core_text = core_text.replace(command_seam, "command: [build_py, '-m', 'code_generators.")
core_meson.write_text(core_text)
PY
python3 "${ROOT_DIR}/tools/patch_numpy_noeh_unique.py" \
  "${PROBE_DIR}/numpy/numpy/_core/src/multiarray/unique.cpp"

CROSS_FILE="${PROBE_DIR}/wasi-cross.ini"
cat >"${CROSS_FILE}" <<EOF
[binaries]
c = '${STATIC_C_WRAPPER}'
cpp = '${STATIC_CXX_WRAPPER}'
ar = '${WASI_SDK_PATH}/bin/llvm-ar'
strip = '${WASI_SDK_PATH}/bin/llvm-strip'
python = '${TARGET_PYTHON_ADAPTER}'
cython = '${CYTHON_WRAPPER}'
pkg-config = '${PKG_CONFIG_BIN}'
cmake = 'false'

[host_machine]
system = 'wasi'
cpu_family = 'wasm32'
cpu = 'wasm32'
endian = 'little'

[properties]
longdouble_format = 'IEEE_QUAD_LE'

[built-in options]
c_args = ['--target=wasm32-wasip1', '--sysroot=${WASI_SDK_PATH}/share/wasi-sysroot']
cpp_args = ['--target=wasm32-wasip1', '--sysroot=${WASI_SDK_PATH}/share/wasi-sysroot']
c_link_args = ['--target=wasm32-wasip1', '--sysroot=${WASI_SDK_PATH}/share/wasi-sysroot']
cpp_link_args = ['--target=wasm32-wasip1', '--sysroot=${WASI_SDK_PATH}/share/wasi-sysroot']
EOF

MESON=(python3 "${PROBE_DIR}/numpy/vendored-meson/meson/meson.py")
set +e
PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="${TARGET_PKGCONFIG_DIR}" PATH="${CYTHON_WRAPPER_DIR}:${PATH}" PYTHONPATH="${PROBE_DIR}/cython" "${MESON[@]}" setup \
  "${PROBE_DIR}/build" "${PROBE_DIR}/numpy" \
  --cross-file "${CROSS_FILE}" \
  -Dallow-noblas=true \
  -Dblas=none \
  -Dlapack=none \
  -Ddisable-threading=true \
  -Ddisable-optimization=true \
  -Ddisable-highway=true \
  -Ddisable-intel-sort=true \
  -Dcpu-baseline=none \
  -Dcpu-dispatch=none \
  -Dpython.bytecompile=-1 \
  >"${PROBE_DIR}/logs/setup.log" 2>&1
SETUP_EXIT=$?
COMPILE_EXIT=-1
INSTALL_EXIT=-1
STAGE_EXIT=-1
LINK_EXIT=-1
PACK_EXIT=-1
LINK_PROBE="${PROBE_DIR}/numpy-core-link-probe.wasm"
IMPORT_PROBE="${PROBE_DIR}/numpy-import-probe.wasm"
NUMPY_INSTALL_ROOT="${PROBE_DIR}/numpy-install"
PROBE_VFS_DIR="${PROBE_DIR}/vfs-python"
PACKAGE_MANIFEST="${PROBE_DIR}/numpy-package-manifest.json"
if [[ ${SETUP_EXIT} -eq 0 ]]; then
  PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="${TARGET_PKGCONFIG_DIR}" PATH="${CYTHON_WRAPPER_DIR}:${PATH}" PYTHONPATH="${PROBE_DIR}/cython" "${MESON[@]}" compile \
    -C "${PROBE_DIR}/build" -j 2 \
    >"${PROBE_DIR}/logs/compile.log" 2>&1
  COMPILE_EXIT=$?
fi
if [[ ${COMPILE_EXIT} -eq 0 ]]; then
  PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="${TARGET_PKGCONFIG_DIR}" PATH="${CYTHON_WRAPPER_DIR}:${PATH}" PYTHONPATH="${PROBE_DIR}/cython" "${MESON[@]}" install --no-rebuild \
    -C "${PROBE_DIR}/build" --destdir "${NUMPY_INSTALL_ROOT}" --tags python-runtime \
    >"${PROBE_DIR}/logs/install.log" 2>&1
  INSTALL_EXIT=$?
fi
if [[ ${INSTALL_EXIT} -eq 0 ]]; then
  python3 "${ROOT_DIR}/tools/copy_tree_deterministic.py" \
    "${BASE_VFS_DIR}" "${PROBE_VFS_DIR}" --epoch "${SOURCE_DATE_EPOCH}" \
    >"${PROBE_DIR}/logs/stage.log" 2>&1
  BASE_STAGE_EXIT=$?
  if [[ ${BASE_STAGE_EXIT} -eq 0 ]]; then
    python3 "${ROOT_DIR}/tools/stage_numpy_wasi_package.py" \
      "${NUMPY_INSTALL_ROOT}" "${PROBE_VFS_DIR}/site-packages/numpy" \
      --epoch "${SOURCE_DATE_EPOCH}" --manifest "${PACKAGE_MANIFEST}" \
      >>"${PROBE_DIR}/logs/stage.log" 2>&1
    STAGE_EXIT=$?
  else
    STAGE_EXIT=${BASE_STAGE_EXIT}
  fi
fi
if [[ ${COMPILE_EXIT} -eq 0 ]]; then
  SELECTED_MANIFESTS=(
    "${STATIC_MANIFEST_DIR}/numpy/_core/_multiarray_umath.json"
    "${STATIC_MANIFEST_DIR}/numpy/linalg/_umath_linalg.json"
  )
  for manifest in "${SELECTED_MANIFESTS[@]}"; do
    if [[ ! -f ${manifest} ]]; then
      echo "missing selected NumPy static-extension manifest: ${manifest}" >&2
      exit 12
    fi
  done
  mapfile -t NUMPY_LINK_INPUTS < <(python3 - "${SELECTED_MANIFESTS[@]}" "${PROBE_DIR}/build" <<'PY'
import json, pathlib, sys
manifest_paths = [pathlib.Path(value) for value in sys.argv[1:-1]]
build = pathlib.Path(sys.argv[-1])
expected_archives = [
    "numpy/_core/_multiarray_umath.a",
    "numpy/linalg/_umath_linalg.a",
]
expected_static = {
    "numpy/_core/libnpymath.a",
    "numpy/_core/libunique_hash.a",
    "numpy/_core/lib_multiarray_umath_mtargets.a",
}
seen = set()
static_inputs = set()
for manifest_path, expected_archive in zip(manifest_paths, expected_archives):
    manifest = json.loads(manifest_path.read_text())
    if manifest["archive"] != expected_archive:
        raise SystemExit(f"unexpected selected archive: {manifest['archive']}")
    static_inputs.update(manifest["static_inputs"])
    for value in [manifest["archive"], *manifest["static_inputs"]]:
        path = pathlib.Path(value)
        path = path if path.is_absolute() else build / path
        path = path.resolve()
        if path not in seen:
            seen.add(path)
            print(path)
if static_inputs != expected_static:
    raise SystemExit(f"unexpected selected static inputs: {sorted(static_inputs)}")
PY
  )
  if [[ ${#NUMPY_LINK_INPUTS[@]} -ne 5 ]]; then
    echo "expected two selected extension archives plus exactly three unique static inputs" >&2
    exit 13
  fi
  for required in "${NUMPY_LINK_INPUTS[@]}"; do
    if [[ ! -f ${required} ]]; then
      echo "missing selected NumPy link input: ${required}" >&2
      exit 14
    fi
  done
  mapfile -t PYTHON_LIBS < <(find "${WASI_BUILD_DIR}" -maxdepth 2 -type f -name 'libpython3.14.a')
  if [[ ${#PYTHON_LIBS[@]} -ne 1 ]]; then
    echo "expected exactly one CPython WASI static library" >&2
    exit 15
  fi
  MPDEC_LIB="${WASI_BUILD_DIR}/Modules/_decimal/libmpdec/libmpdec.a"
  EXPAT_LIB="${WASI_BUILD_DIR}/Modules/expat/libexpat.a"
  LONG_DOUBLE_LIB="${WASI_SDK_PATH}/share/wasi-sysroot/lib/wasm32-wasip1/libc-printscan-long-double.a"
  HACL_LIBS=("${WASI_BUILD_DIR}"/Modules/_hacl/libHacl_*.a)
  for required in "${MPDEC_LIB}" "${EXPAT_LIB}" "${LONG_DOUBLE_LIB}" "${HACL_LIBS[@]}"; do
    if [[ ! -f ${required} ]]; then
      echo "missing CPython static dependency: ${required}" >&2
      exit 16
    fi
  done
  LINK_PROBE_OBJECT="${PROBE_DIR}/numpy-core-link-probe.o"
  "${WASI_SDK_PATH}/bin/clang" --target=wasm32-wasip1 \
    --sysroot="${WASI_SDK_PATH}/share/wasi-sysroot" -O2 \
    -I"${CPYTHON_DIR}/Include" -I"${WASI_BUILD_DIR}" \
    -c "${ROOT_DIR}/experiments/numpy-wasi/link_probe.c" \
    -o "${LINK_PROBE_OBJECT}" >"${PROBE_DIR}/logs/link.log" 2>&1
  LINK_COMPILE_EXIT=$?
  if [[ ${LINK_COMPILE_EXIT} -eq 0 ]]; then
    "${WASI_SDK_PATH}/bin/clang++" --target=wasm32-wasip1 \
      --sysroot="${WASI_SDK_PATH}/share/wasi-sysroot" \
      -fno-exceptions \
      -mexec-model=reactor \
      "${LINK_PROBE_OBJECT}" \
      -Wl,--whole-archive "${NUMPY_LINK_INPUTS[@]}" -Wl,--no-whole-archive \
      "${PYTHON_LIBS[0]}" "${MPDEC_LIB}" "${HACL_LIBS[@]}" "${EXPAT_LIB}" "${WASI_VFS_LIB}" \
      -ldl -lwasi-emulated-getpid -lwasi-emulated-signal -lwasi-emulated-process-clocks \
      -lpthread -lm -lc-printscan-long-double \
      -Wl,--export=numpy_register_probe \
      -Wl,--export=numpy_import_probe \
      -Wl,--export=numpy_numeric_probe \
      -Wl,--export=numpy_python_initialized_probe \
      -Wl,--export-memory \
      -Wl,--initial-memory=134217728 \
      -Wl,--max-memory=536870912 \
      -Wl,-z,stack-size=16777216 \
      -o "${LINK_PROBE}" >>"${PROBE_DIR}/logs/link.log" 2>&1
    LINK_EXIT=$?
  else
    LINK_EXIT=${LINK_COMPILE_EXIT}
  fi
fi
if [[ ${LINK_EXIT} -eq 0 && ${STAGE_EXIT} -eq 0 ]]; then
  "${WASI_VFS}" pack "${LINK_PROBE}" \
    --dir "${PROBE_VFS_DIR}::/usr/lib/python3.14" \
    -o "${IMPORT_PROBE}" >"${PROBE_DIR}/logs/pack.log" 2>&1
  PACK_EXIT=$?
fi
set -e

export PROBE_DIR SETUP_EXIT COMPILE_EXIT INSTALL_EXIT STAGE_EXIT LINK_EXIT PACK_EXIT LINK_PROBE IMPORT_PROBE PACKAGE_MANIFEST TARGET_PYTHON
python3 - <<'PY'
import hashlib, json, os, pathlib
root = pathlib.Path(os.environ["PROBE_DIR"])
setup_exit = int(os.environ["SETUP_EXIT"])
compile_exit = int(os.environ["COMPILE_EXIT"])
install_exit = int(os.environ["INSTALL_EXIT"])
stage_exit = int(os.environ["STAGE_EXIT"])
link_exit = int(os.environ["LINK_EXIT"])
pack_exit = int(os.environ["PACK_EXIT"])
if setup_exit:
    outcome = "setup_failed"
elif compile_exit:
    outcome = "compile_failed"
elif install_exit:
    outcome = "install_failed"
elif stage_exit:
    outcome = "stage_failed"
elif link_exit:
    outcome = "link_failed"
elif pack_exit:
    outcome = "pack_failed"
else:
    outcome = "pack_succeeded"
modules = []
static_extensions = []
build_root = root / "build"
if build_root.is_dir():
    modules = sorted(str(p.relative_to(build_root)) for p in build_root.rglob("*.so"))
for manifest_path in sorted((root / "static-extension-manifests").rglob("*.json")):
    manifest = json.loads(manifest_path.read_text())
    output = manifest["output"]
    if not output.startswith("numpy/"):
        continue
    archive = pathlib.Path(manifest["archive"])
    if not archive.is_absolute():
        archive = build_root / archive
    archive = archive.resolve()
    archive.relative_to(build_root.resolve())
    digest = hashlib.sha256()
    with archive.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    static_extensions.append({
        "output": output,
        "archive": str(archive.relative_to(build_root.resolve())),
        "archive_sha256": digest.hexdigest(),
        "archive_size": archive.stat().st_size,
        "objects": len(manifest["objects"]),
        "static_inputs": manifest["static_inputs"],
        "manifest": str(manifest_path.relative_to(root)),
    })
static_extensions.sort(key=lambda item: item["output"])
def artifact(path):
    if not path.is_file():
        return None
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return {"filename": path.name, "sha256": digest.hexdigest(), "size": path.stat().st_size}

link_probe = pathlib.Path(os.environ["LINK_PROBE"])
monolithic_link = artifact(link_probe)
if monolithic_link is not None:
    monolithic_link.update({"initializer_executed": False, "module_imported": False})
import_probe = artifact(pathlib.Path(os.environ["IMPORT_PROBE"]))
package_manifest_path = pathlib.Path(os.environ["PACKAGE_MANIFEST"])
package_staging = None
if package_manifest_path.is_file():
    package = json.loads(package_manifest_path.read_text())
    package_staging = {
        "manifest": package_manifest_path.name,
        "manifest_sha256": artifact(package_manifest_path)["sha256"],
        "file_count": package["file_count"],
        "source": package["source"],
    }
evidence_error = (
    (compile_exit == 0 and not static_extensions)
    or (stage_exit == 0 and package_staging is None)
    or (link_exit == 0 and monolithic_link is None)
    or (pack_exit == 0 and import_probe is None)
)
if evidence_error:
    outcome = "evidence_failed"
report = {
    "schema_version": 5,
    "target": "wasm32-wasip1",
    "cxx_exception_mode": "selective-disabled",
    "long_double_runtime": "wasi-sdk-33 libc-printscan-long-double",
    "cxx_exception_adaptation": {
        "source": "numpy/_core/src/multiarray/unique.cpp",
        "explicit_string_load_error_preserved": True,
        "bad_alloc_translation": False,
        "production_qualified": False,
    },
    "numpy_version": "2.5.1",
    "numpy_source_sha256": "a48a113e6afea91f5608793bafa7ef2ad481fefbda87ec5069f483de61cb9fa3",
    "cython_version": "3.2.8",
    "setup_exit": setup_exit,
    "compile_exit": compile_exit,
    "install_exit": install_exit,
    "stage_exit": stage_exit,
    "link_exit": link_exit,
    "pack_exit": pack_exit,
    "outcome": outcome,
    "target_python_wrapper": pathlib.Path(os.environ["TARGET_PYTHON"]).name,
    "meson_target_python_adapter": pathlib.Path(os.environ["TARGET_PYTHON_ADAPTER"]).name,
    "extension_outputs": modules,
    "static_extension_count": len(static_extensions),
    "static_extensions": static_extensions,
    "selected_builtin_modules": [
        "numpy._core._multiarray_umath",
        "numpy.linalg._umath_linalg",
    ],
    "package_staging": package_staging,
    "monolithic_link": monolithic_link,
    "import_probe": import_probe,
    "claim": "diagnostic packed runtime probe; initializer/import/numeric execution is reported separately and is not production qualification",
}
(root / "report.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
print(json.dumps(report, sort_keys=True))
if evidence_error:
    raise SystemExit("successful stage did not produce its required evidence")
PY

# A compiler failure is diagnostic evidence. Missing/invalid locked inputs and
# failure to produce a structured report remain hard errors above.
test -s "${PROBE_DIR}/report.json"
