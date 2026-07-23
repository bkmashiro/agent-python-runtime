import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
SPIKE_DIR = ROOT / "experiments" / "preinitialized-guest"


def load_module(name: str, filename: str):
    spec = importlib.util.spec_from_file_location(name, SPIKE_DIR / filename)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {filename}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def runtime_evidence(artifact_sha: str, runtime_init_ns: int, run_total_ns: int) -> dict:
    sample = {
        "instantiate_guest_ns": 10,
        "_initialize_ns": 10,
        "runtime_init_ns": runtime_init_ns,
        "prepare_ns": 10,
        "execute_ns": 10,
        "capability_ns": 0,
        "run_total_ns": run_total_ns,
        "request_bytes": 10,
        "result_bytes": 10,
    }
    return {
        "schema_version": 1,
        "evidence_class": "preinitialization-spike",
        "artifact": {
            "filename": "agent-python-runtime.wasm",
            "sha256": artifact_sha,
            "size_bytes": 100,
            "source_commit": "a" * 40,
            "target": "wasm32-wasip1",
            "execution_model": "reactor",
        },
        "host_source": {"revision": "a" * 40, "modified": False},
        "backend": {"name": "wazero", "reset_mode": "fresh-instance"},
        "environment": {"goos": "linux", "goarch": "amd64", "go_version": "go1.24.13"},
        "fixture": {"samples": 3, "capability_operations": 1, "provider_delay_ns_per_operation": 2_000_000},
        "compile_once": {"instantiate_host_ns": 10, "compile_ns": 20},
        "workloads": {
            "execute": [dict(sample) for _ in range(3)],
            "capability": [dict(sample) for _ in range(3)],
        },
        "limitations": [],
    }


def density_evidence(
    artifact_sha: str,
    ready_ns: int,
    runtime_init_ns: int,
    rss_bytes: int,
    strategy: str = "single-use-preinitialized",
) -> dict:
    samples = []
    for sample_index, slots in enumerate(slot for slot in [1, 2, 4, 8, 16] for _ in range(3)):
        samples.append(
            {
                "sample_index": sample_index,
                "repeat_index": sample_index % 3,
                "requested_slots": slots,
                "phases": {
                    "total_ns": {"status": "measured", "value": ready_ns * slots},
                    "compile_ns": {"status": "measured", "value": ready_ns * slots},
                    "instantiate_ns": {"status": "measured", "value": 10 * slots},
                    "runtime_init_ns": {"status": "measured", "value": runtime_init_ns * slots},
                },
                "process": {"rss_bytes": {"status": "measured", "value": rss_bytes * slots}},
            }
        )
    return {
        "schema_version": 1,
        "evidence_class": "lifecycle-density",
        "artifact": {
            "filename": "agent-python-runtime.wasm",
            "sha256": artifact_sha,
            "size_bytes": 100,
            "source_commit": "a" * 40,
            "artifact_profile": "base",
            "target": "wasm32-wasip1",
            "execution_model": "reactor",
        },
        "host_source": {"revision": "a" * 40, "modified": False},
        "backend": {"name": "wazero", "version": "v1.11.0", "reset_mode": "fresh-instance"},
        "environment": {
            "goos": "linux",
            "goarch": "amd64",
            "go_version": "go1.24.13",
            "kernel_release": "fixture",
            "page_size_bytes": 4096,
            "cgroup_version": "v2",
        },
        "strategy": {
            "requested": strategy,
            "active": strategy,
            "fallback": False,
        },
        "plan": {
            "workload": "idle-ready",
            "slot_counts": [1, 2, 4, 8, 16],
            "repeats_per_slot": 3,
            "fresh_process_per_sample": True,
            "max_process_rss_bytes": 5 * 1024 * 1024 * 1024,
            "child_timeout_ns": 180_000_000_000,
        },
        "samples": samples,
        "limitations": [
            "Preinitialization-spike fixture",
            "The first shard populates one borrowed cache; every shard keeps a separate wazero runtime."
            if strategy == "single-use-preinitialized-shared-cache"
            else "No shared compilation cache.",
            "This experimental strategy does not approve production use.",
        ],
    }


class PreinitializedGuestSpikeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.receipt_module = load_module("preinit_receipt", "write_transform_receipt.py")
        cls.compare_module = load_module("preinit_compare", "compare.py")
        cls.density_compare_module = load_module("preinit_density_compare", "compare_density.py")

    def test_transform_receipt_binds_input_and_repeat_determinism(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            input_path = root / "input.wasm"
            first_path = root / "candidate-a.wasm"
            second_path = root / "candidate-b.wasm"
            input_path.write_bytes(b"\x00asm-input")
            first_path.write_bytes(b"\x00asm-candidate")
            second_path.write_bytes(first_path.read_bytes())

            receipt = self.receipt_module.build_receipt(
                input_path=input_path,
                first_path=first_path,
                second_path=second_path,
                tool_version="wasmtime 44.0.1",
                host_revision="a" * 40,
            )

            self.assertEqual(1, receipt["schema_version"])
            self.assertTrue(receipt["repeat_deterministic"])
            self.assertEqual("runtime_preinitialize", receipt["transform"]["init_func"])
            self.assertEqual("wizer-owned", receipt["transform"]["reactor_initialization"])
            self.assertEqual("fixed-experiment-only:0xa9e17f5d", receipt["transform"]["python_hash_seed"])
            self.assertEqual(
                hashlib.sha256(first_path.read_bytes()).hexdigest(),
                receipt["candidate"]["sha256"],
            )
            self.assertEqual(
                receipt["candidate"]["sha256"],
                receipt["repeat_candidate"]["sha256"],
            )
            self.assertEqual(
                receipt["candidate"]["size_bytes"],
                receipt["repeat_candidate"]["size_bytes"],
            )

    def test_compare_validates_large_runtime_init_and_end_to_end_speedup(self):
        input_sha = hashlib.sha256(b"\x00asm-input").hexdigest()
        candidate_sha = hashlib.sha256(b"\x00asm-candidate").hexdigest()
        receipt = {
            "schema_version": 1,
            "host_revision": "a" * 40,
            "tool": {"name": "wasmtime-wizer", "version": "wasmtime 44.0.1"},
            "input": {"filename": "input.wasm", "size_bytes": 10, "sha256": input_sha},
            "candidate": {"filename": "agent-python-runtime.wasm", "size_bytes": 100, "sha256": candidate_sha},
            "repeat_candidate": {"filename": "candidate-b.wasm", "size_bytes": 100, "sha256": candidate_sha},
            "repeat_deterministic": True,
        }
        baseline = runtime_evidence(input_sha, 5_000_000_000, 5_100_000_000)
        candidate = runtime_evidence(candidate_sha, 20_000_000, 100_000_000)

        report = self.compare_module.compare(baseline, candidate, receipt)

        self.assertEqual("validated", report["verdict"])
        self.assertGreaterEqual(report["metrics"]["runtime_init_speedup"], 10)
        self.assertGreaterEqual(report["metrics"]["run_total_speedup"], 2)
        self.assertTrue(all(report["criteria"].values()))

    def test_compare_marks_nondeterministic_transform_partial(self):
        input_sha = "1" * 64
        candidate_sha = "2" * 64
        receipt = {
            "schema_version": 1,
            "host_revision": "a" * 40,
            "tool": {"name": "wasmtime-wizer", "version": "wasmtime 44.0.1"},
            "input": {"filename": "input.wasm", "size_bytes": 10, "sha256": input_sha},
            "candidate": {"filename": "agent-python-runtime.wasm", "size_bytes": 100, "sha256": candidate_sha},
            "repeat_candidate": {"filename": "candidate-b.wasm", "size_bytes": 101, "sha256": "3" * 64},
            "repeat_deterministic": False,
        }
        report = self.compare_module.compare(
            runtime_evidence(input_sha, 5_000_000_000, 5_100_000_000),
            runtime_evidence(candidate_sha, 20_000_000, 100_000_000),
            receipt,
        )
        self.assertEqual("partial", report["verdict"])
        self.assertFalse(report["criteria"]["repeat_deterministic"])

    def test_compare_rejects_candidate_identity_mismatch(self):
        receipt = {
            "schema_version": 1,
            "host_revision": "a" * 40,
            "tool": {"name": "wasmtime-wizer", "version": "wasmtime 44.0.1"},
            "input": {"filename": "input.wasm", "size_bytes": 10, "sha256": "1" * 64},
            "candidate": {"filename": "agent-python-runtime.wasm", "size_bytes": 100, "sha256": "2" * 64},
            "repeat_candidate": {"filename": "candidate-b.wasm", "size_bytes": 100, "sha256": "2" * 64},
            "repeat_deterministic": True,
        }
        with self.assertRaisesRegex(ValueError, "candidate artifact identity"):
            self.compare_module.compare(
                runtime_evidence("1" * 64, 5_000_000_000, 5_100_000_000),
                runtime_evidence("3" * 64, 20_000_000, 100_000_000),
                receipt,
            )

    def test_candidate_density_uses_the_exact_canonical_safety_plan(self):
        workflow = (ROOT / ".github" / "workflows" / "guest-artifact.yml").read_text()
        self.assertEqual(3, workflow.count("-max-rss-bytes 5368709120"))
        self.assertEqual(3, workflow.count("-child-timeout 3m"))
        self.assertIn("-strategy single-use-preinitialized-shared-cache", workflow)
        self.assertIn("lifecycle-density-shared-cache-candidate.json", workflow)
        self.assertIn("--intervention shared-compilation-cache", workflow)
        self.assertNotIn("-max-rss-bytes 4294967296", workflow)

    def test_checked_in_density_comparisons_are_reproducible(self):
        benchmark_root = ROOT / "docs" / "benchmarks"
        baseline = json.loads(
            (benchmark_root / "preinitialization-spike-lifecycle-density-baseline-linux-amd64.json").read_text()
        )
        candidate = json.loads(
            (benchmark_root / "preinitialization-spike-lifecycle-density-candidate-linux-amd64.json").read_text()
        )
        preinitialization_report = json.loads(
            (benchmark_root / "preinitialization-spike-lifecycle-density-comparison-linux-amd64.json").read_text()
        )
        self.assertEqual(preinitialization_report, self.density_compare_module.compare(baseline, candidate))

        shared_candidate = json.loads(
            (
                benchmark_root
                / "preinitialization-spike-lifecycle-density-shared-cache-candidate-linux-amd64.json"
            ).read_text()
        )
        shared_report = json.loads(
            (
                benchmark_root
                / "preinitialization-spike-lifecycle-density-shared-cache-comparison-linux-amd64.json"
            ).read_text()
        )
        self.assertEqual(
            shared_report,
            self.density_compare_module.compare(
                candidate,
                shared_candidate,
                intervention="shared-compilation-cache",
            ),
        )

    def test_density_compare_reports_exact_n16_speed_and_rss_tradeoff(self):
        report = self.density_compare_module.compare(
            density_evidence("1" * 64, ready_ns=100, runtime_init_ns=50, rss_bytes=1000),
            density_evidence("2" * 64, ready_ns=10, runtime_init_ns=1, rss_bytes=1100),
        )
        headline = report["headline_n16"]
        self.assertEqual("descriptive", report["comparison"])
        self.assertEqual(10, headline["ready_wall_speedup"])
        self.assertEqual(50, headline["runtime_init_speedup"])
        self.assertEqual(1.1, headline["ready_rss_ratio_candidate_over_baseline"])

    def test_density_compare_rejects_plan_drift(self):
        baseline = density_evidence("1" * 64, 100, 50, 1000)
        candidate = density_evidence("2" * 64, 10, 1, 1100)
        candidate["plan"]["child_timeout_ns"] = 1
        with self.assertRaisesRegex(ValueError, "plan drifted"):
            self.density_compare_module.compare(baseline, candidate)

    def test_density_compare_isolates_shared_cache_on_the_same_artifact(self):
        artifact_sha = "1" * 64
        report = self.density_compare_module.compare(
            density_evidence(artifact_sha, 100, 1, 1000),
            density_evidence(
                artifact_sha,
                25,
                1,
                900,
                strategy="single-use-preinitialized-shared-cache",
            ),
            intervention="shared-compilation-cache",
        )
        self.assertEqual("shared-wazero-compilation-cache-lifecycle-density", report["experiment"])
        self.assertEqual(4, report["headline_n16"]["ready_wall_speedup"])
        self.assertEqual(4, report["headline_n16"]["compile_speedup"])

    def test_density_compare_rejects_shared_cache_without_strategy_transition(self):
        artifact_sha = "1" * 64
        with self.assertRaisesRegex(ValueError, "strategy transition"):
            self.density_compare_module.compare(
                density_evidence(artifact_sha, 100, 1, 1000),
                density_evidence(artifact_sha, 25, 1, 900),
                intervention="shared-compilation-cache",
            )

    def test_density_compare_rejects_shared_cache_without_no_production_boundary(self):
        artifact_sha = "1" * 64
        candidate = density_evidence(
            artifact_sha,
            25,
            1,
            900,
            strategy="single-use-preinitialized-shared-cache",
        )
        candidate["limitations"] = [
            limitation for limitation in candidate["limitations"] if "does not approve" not in limitation
        ]
        with self.assertRaisesRegex(ValueError, "no-production"):
            self.density_compare_module.compare(
                density_evidence(artifact_sha, 100, 1, 1000),
                candidate,
                intervention="shared-compilation-cache",
            )


if __name__ == "__main__":
    unittest.main()
