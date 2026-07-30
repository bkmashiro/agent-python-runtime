import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "tools" / "benchmark_native_python.py"
SPEC = importlib.util.spec_from_file_location("benchmark_native_python", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class NativePythonBenchmarkTests(unittest.TestCase):
    def test_nearest_rank_percentile_and_summary(self):
        self.assertEqual(5, MODULE.percentile([1, 2, 3, 4, 5], 95))
        self.assertEqual(
            {"median_ns": 3, "p95_ns": 5, "p99_ns": 5, "min_ns": 1, "max_ns": 5},
            MODULE.summarize([1, 2, 3, 4, 5]),
        )

    def test_numpy_fixture_binds_version_and_import(self):
        fixture = MODULE.FIXTURES["numpy-import"]
        self.assertIn("import numpy as np", fixture["prepare_source"])
        self.assertIn("np.arange", fixture["execute_source"])
        self.assertEqual("2.5.1", fixture["expected_result"]["numpy_version"])

    def test_runs_exact_fixture_in_cold_and_warm_native_processes(self):
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "native.json"
            completed = subprocess.run(
                [
                    sys.executable,
                    str(SCRIPT),
                    "--python",
                    sys.executable,
                    "--repository",
                    str(ROOT),
                    "--samples",
                    "3",
                    "--output",
                    str(output),
                ],
                cwd=ROOT,
                text=True,
                capture_output=True,
                timeout=60,
                check=False,
            )
            self.assertEqual("", completed.stderr)
            self.assertEqual(0, completed.returncode)
            evidence = json.loads(output.read_text())
        self.assertEqual("native-cpython-cold-warm", evidence["evidence_kind"])
        self.assertEqual(3, len(evidence["cold_process"]["samples"]))
        self.assertEqual(3, len(evidence["warm_process"]["samples"]))
        self.assertEqual(MODULE.FIXTURES["basic"]["expected_result"], evidence["fixture"]["expected_result"])
        self.assertEqual("basic", evidence["fixture"]["name"])
        self.assertEqual("CPython", evidence["python"]["implementation"])
        self.assertGreater(evidence["cold_process"]["total"]["median_ns"], 0)
        self.assertGreater(evidence["warm_process"]["total"]["median_ns"], 0)


if __name__ == "__main__":
    unittest.main()
