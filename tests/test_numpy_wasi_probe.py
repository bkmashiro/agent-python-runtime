import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
LOCK = ROOT / "experiments" / "numpy-wasi" / "sources.lock.json"
PROBE = ROOT / "experiments" / "numpy-wasi" / "probe.sh"
WORKFLOW = ROOT / ".github" / "workflows" / "numpy-wasi-probe.yml"


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
        self.assertIn('outcome = "setup_failed"', script)
        self.assertIn('outcome = "compile_failed"', script)
        self.assertIn("TARGET_PYTHON_ADAPTER", script)
        self.assertIn('TARGET_PYTHON_SCRIPT_GUEST="/.numpy-wasi-probe/python_info.py"', script)
        self.assertIn('[[ $# -eq 1 && -f $1 && ${1##*/} == python_info.py ]]', script)
        self.assertIn("python3.pc", script)
        self.assertIn('PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="${TARGET_PKGCONFIG_DIR}"', script)
        self.assertIn("expected exactly one NumPy target-Python Meson seam", script)
        self.assertIn("find_installation({target_python!r}, pure: false)", script)
        self.assertIn("CYTHON_WRAPPER_DIR", script)
        self.assertIn('PATH="${CYTHON_WRAPPER_DIR}:${PATH}"', script)
        self.assertIn("diagnostic only", script)
        self.assertNotIn("pip install", script)
        self.assertNotIn("wasi-wheels", script.lower())

    def test_workflow_is_manual_only_and_uploads_diagnostic_evidence(self):
        text = WORKFLOW.read_text()
        self.assertIn("workflow_dispatch:", text)
        self.assertNotIn("pull_request:", text)
        self.assertNotIn("\n  push:", text)
        self.assertIn("run: bash experiments/numpy-wasi/probe.sh", text)
        self.assertIn("numpy-wasi-probe-${{ github.sha }}", text)
        self.assertRegex(text, r"actions/checkout@[0-9a-f]{40}")
        self.assertRegex(text, r"actions/setup-python@[0-9a-f]{40}")
        self.assertRegex(text, r"actions/setup-go@[0-9a-f]{40}")
        self.assertRegex(text, r"actions/upload-artifact@[0-9a-f]{40}")


if __name__ == "__main__":
    unittest.main()
