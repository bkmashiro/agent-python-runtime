import hashlib
import importlib.util
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


class PreinitializedGuestSpikeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.receipt_module = load_module("preinit_receipt", "write_transform_receipt.py")
        cls.compare_module = load_module("preinit_compare", "compare.py")

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
                init_func="runtime_preinitialize",
                explicit_reactor_status=0,
                without_reactor_status=134,
            )

            self.assertEqual(1, receipt["schema_version"])
            self.assertTrue(receipt["repeat_deterministic"])
            self.assertEqual("runtime_preinitialize", receipt["transform"]["init_func"])
            self.assertEqual(134, receipt["transform"]["variant_exit_statuses"]["runtime_preinitialize_without_reactor"])
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


if __name__ == "__main__":
    unittest.main()
