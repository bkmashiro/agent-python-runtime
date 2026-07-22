import importlib.util
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "compare_runtime_benchmarks.py"
spec = importlib.util.spec_from_file_location("compare_runtime_benchmarks", MODULE_PATH)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class RuntimeBenchmarkComparisonTests(unittest.TestCase):
    def evidence(self, evidence_class="production-safe", multiplier=1):
        sample = {
            "instantiate_guest_ns": 10 * multiplier,
            "_initialize_ns": 2 * multiplier,
            "runtime_init_ns": 100 * multiplier,
            "prepare_ns": 1 * multiplier,
            "execute_ns": 4 * multiplier,
            "capability_ns": 0,
            "run_total_ns": 120 * multiplier,
            "request_bytes": 10,
            "result_bytes": 20,
        }
        capability = dict(sample)
        capability["capability_ns"] = 3 * multiplier
        return {
            "schema_version": 1,
            "evidence_class": evidence_class,
            "artifact": {"sha256": "a" * 64, "source_commit": "b" * 40},
            "host_source": {"revision": "c" * 40, "modified": False},
            "backend": {"name": "wazero", "reset_mode": "fresh-instance"},
            "environment": {"goos": "linux", "goarch": "amd64", "go_version": "go1.24"},
            "fixture": {"samples": 3, "capability_operations": 1},
            "compile_once": {"instantiate_host_ns": multiplier, "compile_ns": 50 * multiplier},
            "workloads": {
                "execute": [dict(sample) for _ in range(3)],
                "capability": [dict(capability) for _ in range(3)],
            },
        }

    def test_compares_medians_and_ratios_without_threshold_claim(self):
        comparison = module.compare(self.evidence(multiplier=1), self.evidence(multiplier=2))
        self.assertEqual(comparison["schema_version"], 1)
        self.assertEqual(comparison["workloads"]["execute"]["runtime_init_ns"]["baseline_median"], 100)
        self.assertEqual(comparison["workloads"]["execute"]["runtime_init_ns"]["candidate_median"], 200)
        self.assertEqual(comparison["workloads"]["execute"]["runtime_init_ns"]["candidate_over_baseline"], 2.0)
        self.assertNotIn("pass", comparison)
        self.assertNotIn("threshold", comparison)

    def test_rejects_class_and_sample_count_mismatch(self):
        with self.assertRaisesRegex(ValueError, "evidence class"):
            module.compare(self.evidence(), self.evidence("full"))
        candidate = self.evidence()
        candidate["workloads"]["execute"].pop()
        with self.assertRaisesRegex(ValueError, "sample"):
            module.compare(self.evidence(), candidate)


if __name__ == "__main__":
    unittest.main()
