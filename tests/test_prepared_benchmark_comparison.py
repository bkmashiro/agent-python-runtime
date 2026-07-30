import importlib.util
import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "compare_prepared_benchmarks", ROOT / "tools" / "compare_prepared_benchmarks.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def fresh_fixture():
    sample = {
        "runtime_init_ns": 100,
        "run_total_ns": 120,
        "capability_ns": 0,
    }
    capability = dict(sample, run_total_ns=140, capability_ns=5)
    return {
        "schema_version": 1,
        "evidence_class": "production-safe",
        "artifact": {"sha256": "a" * 64, "source_commit": "b" * 40},
        "host_source": {"revision": "c" * 40, "modified": False},
        "backend": {"name": "wazero", "reset_mode": "fresh-instance"},
        "environment": {"goos": "linux", "goarch": "amd64", "go_version": "go1.24"},
        "fixture": {"samples": 3, "capability_operations": 1, "provider_delay_ns_per_operation": 2_000_000},
        "workloads": {"execute": [sample] * 3, "capability": [capability] * 3},
    }


def prepared_fixture():
    sample = {
        "run_total_ns": 20,
        "capability_ns": 0,
        "refill_runtime_init_ns": 101,
        "retained_guest_memory_bytes": 128 * 1024 * 1024,
    }
    capability = dict(sample, run_total_ns=40, capability_ns=5)
    return {
        "schema_version": 1,
        "evidence_kind": "single-use-preinitialized",
        "evidence_class": "production-safe",
        "artifact": {"sha256": "a" * 64, "source_commit": "b" * 40},
        "host_source": {"revision": "c" * 40, "modified": False},
        "backend": {"name": "wazero", "reset_mode": "fresh-instance"},
        "environment": {"goos": "linux", "goarch": "amd64", "go_version": "go1.24"},
        "fixture": {"samples": 3, "capability_operations": 1, "provider_delay_ns_per_operation": 2_000_000, "prepared_capacity": 1},
        "readiness": {"factory_new_total_ns": 130, "retained_guest_memory_bytes": 128 * 1024 * 1024},
        "state_copy": {"applicable": False, "reason": "single use"},
        "workloads": {"first_execute": sample, "steady_execute": [sample] * 3, "steady_capability": [capability] * 3},
    }


class PreparedComparisonTests(unittest.TestCase):
    def test_compares_fresh_and_prepared_without_threshold(self):
        result = MODULE.compare(fresh_fixture(), prepared_fixture())
        self.assertEqual(result["medians_ns"]["fresh_execute_run_total"], 120)
        self.assertEqual(result["medians_ns"]["prepared_steady_execute_run_total"], 20)
        self.assertEqual(result["ratios"]["execute_fresh_over_prepared"], 6.0)
        self.assertEqual(result["ratios"]["capability_fresh_over_prepared"], 3.5)
        self.assertFalse(result["state_copy"]["applicable"])
        self.assertEqual(result["retained_guest_memory_bytes"], 128 * 1024 * 1024)
        self.assertNotIn("threshold", result)
        self.assertNotIn("pass", result)

    def test_rejects_identity_or_fixture_mismatch(self):
        prepared = prepared_fixture()
        prepared["artifact"]["sha256"] = "d" * 64
        with self.assertRaisesRegex(ValueError, "identity"):
            MODULE.compare(fresh_fixture(), prepared)

        prepared = prepared_fixture()
        prepared["fixture"]["capability_operations"] = 20
        with self.assertRaisesRegex(ValueError, "fixture"):
            MODULE.compare(fresh_fixture(), prepared)


if __name__ == "__main__":
    unittest.main()
