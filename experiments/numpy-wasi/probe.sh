#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK_DIR=${AGENT_RUNTIME_BUILD_DIR:?AGENT_RUNTIME_BUILD_DIR must point at a completed guest build}
PROBE_DIR=${NUMPY_WASI_PROBE_DIR:-"${RUNNER_TEMP:-/tmp}/numpy-wasi-probe"}
LOCK="${ROOT_DIR}/experiments/numpy-wasi/sources.lock.json"
WASI_BUILD_DIR="${WORK_DIR}/cpython/cross-build/wasm32-wasip1"
WASI_SDK_PATH="${WORK_DIR}/tools/wasi-sdk"

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
  "${ROOT_DIR}/tools/archive_wasm_extension.py"; do
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
exec python3 "${ROOT_DIR}/tools/archive_wasm_extension.py" \\
  --real-compiler "${WASI_SDK_PATH}/bin/clang++" \\
  --archiver "${WASI_SDK_PATH}/bin/llvm-ar" \\
  --manifest-dir "${STATIC_MANIFEST_DIR}" \\
  --build-root "${PROBE_DIR}/build" -- "\$@"
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
  >"${PROBE_DIR}/logs/setup.log" 2>&1
SETUP_EXIT=$?
COMPILE_EXIT=-1
if [[ ${SETUP_EXIT} -eq 0 ]]; then
  PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="${TARGET_PKGCONFIG_DIR}" PATH="${CYTHON_WRAPPER_DIR}:${PATH}" PYTHONPATH="${PROBE_DIR}/cython" "${MESON[@]}" compile \
    -C "${PROBE_DIR}/build" -j 2 \
    >"${PROBE_DIR}/logs/compile.log" 2>&1
  COMPILE_EXIT=$?
fi
set -e

export PROBE_DIR SETUP_EXIT COMPILE_EXIT TARGET_PYTHON
python3 - <<'PY'
import hashlib, json, os, pathlib
root = pathlib.Path(os.environ["PROBE_DIR"])
setup_exit = int(os.environ["SETUP_EXIT"])
compile_exit = int(os.environ["COMPILE_EXIT"])
if setup_exit:
    outcome = "setup_failed"
elif compile_exit:
    outcome = "compile_failed"
else:
    outcome = "compile_succeeded"
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
evidence_error = compile_exit == 0 and not static_extensions
if evidence_error:
    outcome = "evidence_failed"
report = {
    "schema_version": 2,
    "target": "wasm32-wasip1",
    "numpy_version": "2.5.1",
    "numpy_source_sha256": "a48a113e6afea91f5608793bafa7ef2ad481fefbda87ec5069f483de61cb9fa3",
    "cython_version": "3.2.8",
    "setup_exit": setup_exit,
    "compile_exit": compile_exit,
    "outcome": outcome,
    "target_python_wrapper": pathlib.Path(os.environ["TARGET_PYTHON"]).name,
    "meson_target_python_adapter": pathlib.Path(os.environ["TARGET_PYTHON_ADAPTER"]).name,
    "extension_outputs": modules,
    "static_extension_count": len(static_extensions),
    "static_extensions": static_extensions,
    "claim": "diagnostic only; static archives are not yet linked, registered, or importable",
}
(root / "report.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
print(json.dumps(report, sort_keys=True))
if evidence_error:
    raise SystemExit("compile succeeded without static extension evidence")
PY

# A compiler failure is diagnostic evidence. Missing/invalid locked inputs and
# failure to produce a structured report remain hard errors above.
test -s "${PROBE_DIR}/report.json"
