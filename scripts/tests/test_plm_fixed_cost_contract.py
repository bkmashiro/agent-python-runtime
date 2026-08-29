import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
RUNNER = ROOT / "scripts/run-workstation-plm-fixed-cost.py"


def load_runner():
    spec = importlib.util.spec_from_file_location("run_workstation_plm_fixed_cost", RUNNER)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class PLMFixedCostContractTests(unittest.TestCase):
    def setUp(self) -> None:
        self.runner = load_runner()
        self.source = {"commit": "a" * 40, "tree": "b" * 40, "epoch": 1234567890}

    def write_host(self, root: pathlib.Path, host: str, offset: int, runs: int, artifact: bytes = b"wasm") -> None:
        suite = root / "hosts" / host / "plm-fixed-cost"
        (suite / "artifacts").mkdir(parents=True)
        artifact_path = suite / "artifacts/base.wasm"
        artifact_path.write_bytes(artifact)
        platform = {
            "schema_version": "pysolate.plm-fixed-cost-platform.v1",
            "source_commit": self.source["commit"],
            "source_tree": self.source["tree"],
            "source_epoch": self.source["epoch"],
            "host_id": host,
        }
        (suite / "platform.json").write_text(json.dumps(platform) + "\n")
        profiles = []
        for name in ("cold_end_to_end", "engine_precompiled"):
            samples = []
            for pair in range(runs):
                for mode in ("baseline", "plm"):
                    samples.append({
                        "profile": name,
                        "pair_iteration": pair,
                        "mode": mode,
                        "read_count": 0,
                        "delay_ms": 0,
                        "provider_starts": 0,
                        "provider_max_concurrency": 0,
                        "call_count": 0,
                        "result": [750],
                        "source_sha256": "sha256:" + "c" * 64,
                    })
            profiles.append({"name": name, "samples": samples, "comparisons": [{}]})
        evidence = {
            "schema_version": "pysolate.plm-crossover-economics.v1",
            "target_commit": self.source["commit"],
            "source_tree": self.source["tree"],
            "artifact_source_commit": self.source["commit"],
            "artifact_sha256": self.runner.sha256(artifact_path),
            "source_sha256": "sha256:" + "c" * 64,
            "runs_per_arm": 3,
            "zero_read_runs": runs,
            "zero_read": True,
            "zero_only": True,
            "read_counts": [],
            "delays_ms": [],
            "evaluation_host_id": host,
            "evaluation_order_offset": offset,
            "profiles": profiles,
        }
        evidence_path = suite / "plm-fixed-cost.json"
        evidence_path.write_text(json.dumps(evidence) + "\n")
        manifest = {
            "schema_version": "pysolate.plm-fixed-cost-host.v1",
            "source": self.source,
            "host_id": host,
            "order_offset": offset,
            "zero_read_runs": runs,
            "profiles": ["cold_end_to_end", "engine_precompiled"],
            "evidence_sha256": self.runner.sha256(evidence_path),
            "artifact_sha256": self.runner.sha256(artifact_path),
            "platform_sha256": self.runner.sha256(suite / "platform.json"),
        }
        (suite / "manifest.json").write_text(json.dumps(manifest) + "\n")

    def test_merge_accepts_exact_source_bound_pairs(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_host(root, "gpu31", 0, 3)
            self.write_host(root, "gpu33", 1, 3)
            merged = self.runner.merge_hosts(root, ["gpu31", "gpu33"], 3, self.source)
            self.assertEqual(6, merged["pairs_per_profile"])
            self.assertEqual(["gpu31", "gpu33"], merged["selected_hosts"])

    def test_merge_rejects_cross_host_artifact_drift(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_host(root, "gpu31", 0, 3, b"wasm-a")
            self.write_host(root, "gpu33", 1, 3, b"wasm-b")
            with self.assertRaisesRegex(ValueError, "artifact or source drift"):
                self.runner.merge_hosts(root, ["gpu31", "gpu33"], 3, self.source)

    def test_merge_rejects_missing_pair(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_host(root, "gpu31", 0, 3)
            path = root / "hosts/gpu31/plm-fixed-cost/plm-fixed-cost.json"
            evidence = json.loads(path.read_text())
            evidence["profiles"][0]["samples"].pop()
            path.write_text(json.dumps(evidence) + "\n")
            manifest_path = root / "hosts/gpu31/plm-fixed-cost/manifest.json"
            manifest = json.loads(manifest_path.read_text())
            manifest["evidence_sha256"] = self.runner.sha256(path)
            manifest_path.write_text(json.dumps(manifest) + "\n")
            with self.assertRaisesRegex(ValueError, "sample count drift"):
                self.runner.merge_hosts(root, ["gpu31"], 3, self.source)

    def test_scripts_bind_zero_only_suite_and_bounded_runs(self) -> None:
        linux = (ROOT / "scripts/run-linux-plm-fixed-cost.sh").read_text()
        wrapper = (ROOT / "scripts/test-host-workstation.sh").read_text()
        worker = (ROOT / "scripts/internal/test-host-workstation-worker.sh").read_text()
        gate = (ROOT / "scripts/plm-crossover-economics-gate.sh").read_text()
        for token in ("PLM_CROSSOVER_ZERO_ONLY=1", 'PLM_CROSSOVER_ZERO_READ_RUNS="$runs"', "PLM_CROSSOVER_GO_TEST_TIMEOUT=1h"):
            self.assertIn(token, linux)
        self.assertIn("plm-fixed-cost", wrapper)
        self.assertIn("run-linux-plm-fixed-cost.sh", worker)
        self.assertIn("PLM_CROSSOVER_ZERO_READ_RUNS", gate)
        self.assertIn("PLM_CROSSOVER_ZERO_ONLY", gate)


if __name__ == "__main__":
    unittest.main()
