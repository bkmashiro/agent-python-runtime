import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
LOCK = ROOT / "experiments" / "numpy-wasi" / "sources.lock.json"
PROBE = ROOT / "experiments" / "numpy-wasi" / "probe.sh"
NOEH_PATCHER = ROOT / "tools" / "patch_numpy_noeh_unique.py"
LINK_PROBE = ROOT / "experiments" / "numpy-wasi" / "link_probe.c"
WORKFLOW = ROOT / ".github" / "workflows" / "numpy-wasi-probe.yml"
REGISTRATION_VERIFIER = ROOT / "cmd" / "validate-numpy-link-probe" / "main.go"
LOCK = ROOT / "experiments" / "numpy-wasi" / "sources.lock.json"

class NumPyWASIProbeContractTests(unittest.TestCase):
    def test_probe_uses_isolated_exact_sources_and_bounded_features(self):
        document = json.loads(LOCK.read_text())
        sources = {source["id"]: source for source in document["sources"]}
        self.assertEqual(sources["numpy-source"]["version"], "2.5.1")
        self.assertEqual(
            sources["numpy-source"]["sha256"],
            "a48a113e6afea91f5608793bafa7ef2ad481fefbda87ec5069f483de61cb9fa3",
        )
        self.assertEqual(sources["numpy-source"]["artifact_relation"], "build-only")
        self.assertEqual(sources["cython-host-wheel-linux-x86_64-cp313"]["version"], "3.2.8")

        script = PROBE.read_text()
        self.assertIn("--lock \"${LOCK}\"", script)
        self.assertIn("-Dallow-noblas=true", script)
        self.assertIn("-Dblas=none", script)
        self.assertIn("-Dlapack=none", script)
        self.assertIn("-Ddisable-threading=true", script)
        self.assertIn("-Ddisable-optimization=true", script)
        self.assertIn("longdouble_format = 'IEEE_QUAD_LE'", script)
        self.assertNotIn("longdouble_format = 'IEEE_DOUBLE_LE'", script)
        self.assertIn('outcome = "setup_failed"', script)
        self.assertIn('outcome = "compile_failed"', script)
        self.assertIn("TARGET_PYTHON_ADAPTER", script)
        self.assertIn('TARGET_PYTHON_SCRIPT_GUEST="/.numpy-wasi-probe/python_info.py"', script)
        self.assertIn('[[ $# -eq 1 && -f $1 && ${1##*/} == python_info.py ]]', script)
        self.assertIn('python-3.14.pc', script)
        self.assertIn("python3.pc", script)
        self.assertIn('PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="${TARGET_PKGCONFIG_DIR}"', script)
        self.assertIn("expected exactly four NumPy host codegen command seams", script)
        self.assertIn("find_program('python3', native: true)", script)
        self.assertIn("tools/archive_wasm_extension.py", script)
        self.assertIn("STATIC_MANIFEST_DIR", script)
        self.assertIn('"schema_version": 5', script)
        self.assertIn('"install_exit"', script)
        self.assertIn("install --no-rebuild", script)
        self.assertIn("--tags python-runtime", script)
        self.assertIn("-Dpython.bytecompile=-1", script)
        self.assertIn("tools/stage_numpy_wasi_package.py", script)
        self.assertIn("numpy-package-manifest.json", script)
        self.assertIn("wasi-vfs", script)
        self.assertIn("numpy-import-probe.wasm", script)
        self.assertIn('"static_extension_count"', script)
        self.assertIn('outcome = "link_failed"', script)
        self.assertIn('outcome = "pack_succeeded"', script)
        self.assertIn("CPYTHON_DIR=\"${WORK_DIR}/cpython\"", script)
        self.assertIn("expected NumPy core archive plus exactly three static inputs", script)
        self.assertIn("--whole-archive", script)
        self.assertEqual(script.count("-fno-exceptions"), 2)
        self.assertIn("*/numpy/_core/src/multiarray/unique.cpp", script)
        self.assertNotIn("-fwasm-exceptions", script)
        self.assertNotIn("-lunwind", script)
        self.assertIn('"cxx_exception_mode": "selective-disabled"', script)
        self.assertIn('"explicit_string_load_error_preserved": True', script)
        self.assertIn('"bad_alloc_translation": False', script)
        self.assertIn("tools/patch_numpy_noeh_unique.py", script)
        noeh_patcher = NOEH_PATCHER.read_text()
        self.assertIn("expected exactly one NumPy no-exception unique hash", noeh_patcher)
        self.assertIn("unexpected C++ exception syntax remains", noeh_patcher)
        self.assertIn("--export=numpy_register_probe", script)
        self.assertIn("expected exactly one NumPy target-Python Meson seam", script)
        self.assertIn("find_installation({target_python!r}, pure: false)", script)
        self.assertIn("CYTHON_WRAPPER_DIR", script)
        self.assertIn('PATH="${CYTHON_WRAPPER_DIR}:${PATH}"', script)
        self.assertIn("initializer/import execution is reported separately", script)
        link_source = LINK_PROBE.read_text()
        self.assertIn("PyInit__multiarray_umath", link_source)
        self.assertIn('PyImport_AppendInittab("numpy._core._multiarray_umath"', link_source)
        self.assertIn('export_name("numpy_register_probe")', link_source)
        self.assertIn('export_name("numpy_import_probe")', link_source)
        self.assertIn("Py_InitializeFromConfig", link_source)
        self.assertIn("_imp.is_builtin", link_source)
        self.assertIn('PyImport_ImportModule("numpy")', link_source)
        self.assertNotIn("return PyInit__multiarray_umath()", link_source)
        verifier = REGISTRATION_VERIFIER.read_text()
        self.assertIn('ExportedFunction("numpy_import_probe")', verifier)
        self.assertIn('"import_succeeded"', verifier)
        self.assertNotIn("pip install", script)
        self.assertNotIn("wasi-wheels", script.lower())

    def test_workflow_is_manual_only_and_uploads_diagnostic_evidence(self):
        text = WORKFLOW.read_text()
        self.assertIn("workflow_dispatch:", text)
        self.assertNotIn("pull_request:", text)
        self.assertNotIn("\n  push:", text)
        self.assertIn("run: bash experiments/numpy-wasi/probe.sh", text)
        self.assertIn("numpy-wasi-probe-${{ github.sha }}", text)
        self.assertIn("static-extension-manifests/", text)
        self.assertIn("numpy-core-link-probe.wasm", text)
        self.assertIn("numpy-import-probe.wasm", text)
        self.assertIn("numpy-package-manifest.json", text)
        self.assertIn("go run ./cmd/validate-numpy-link-probe", text)
        self.assertIn("registration-report.json", text)
        self.assertRegex(text, r"actions/checkout@[0-9a-f]{40}")
        self.assertRegex(text, r"actions/setup-python@[0-9a-f]{40}")
        self.assertRegex(text, r"actions/setup-go@[0-9a-f]{40}")
        self.assertRegex(text, r"actions/upload-artifact@[0-9a-f]{40}")


if __name__ == "__main__":
    unittest.main()
