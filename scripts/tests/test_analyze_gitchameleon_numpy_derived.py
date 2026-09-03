import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
ANALYZER = ROOT / "scripts/analyze-gitchameleon-numpy-derived.py"
MANIFEST = ROOT / "integration/e2e/testdata/gitchameleon_numpy_subset_v1.json"


def load_analyzer():
    spec = importlib.util.spec_from_file_location("analyze_gitchameleon_numpy_derived", ANALYZER)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class GitChameleonNumPyDerivedAnalysisTests(unittest.TestCase):
    def setUp(self) -> None:
        self.analyzer = load_analyzer()
        self.manifest = json.loads(MANIFEST.read_text())
        self.manifest_sha = self.analyzer.digest(MANIFEST.read_bytes())

    def write_matrix(self, root: pathlib.Path, runs: int = 2) -> None:
        raw = root / "raw"
        raw.mkdir(parents=True)
        for task in self.manifest["tasks"]:
            calls = len(task["inputs"])
            for cell in task["cells"]:
                rate = cell["tokens_per_second"]
                samples = []
                for trial in range(runs):
                    for order, treatment in enumerate(("serial_whole_file", "pysolate_pooled_prefix")):
                        serial = 20_000_000_000 + cell["source_window_ms"] * 1_000_000 + trial
                        saving = cell["source_window_ms"] * 1_000_000 + (calls - 1) * 200_000_000
                        total = serial if treatment == "serial_whole_file" else serial - saving
                        sample = {
                            "trial": trial,
                            "order": order,
                            "treatment": treatment,
                            "post_begin_nanos": total,
                            "finalize_nanos": total - cell["source_window_ms"] * 1_000_000,
                            "provider_max_concurrent": calls if treatment == "pysolate_pooled_prefix" else 1,
                            "outcome": {
                                "FinalProgramOutcome": "success",
                                "ResultSHA256": "sha256:" + task["example_id"].zfill(64),
                                "LogicalCalls": calls,
                                "PhysicalAttempts": calls,
                                "ReadyBeforeFinalize": calls if treatment == "pysolate_pooled_prefix" else 0,
                            },
                        }
                        if treatment == "pysolate_pooled_prefix":
                            sample["prefix_analyzer_invocations"] = 1
                            sample["split_phase"] = {
                                "Reused": calls,
                                "MaximumConcurrent": calls,
                                "JobsLinearized": calls,
                                "JobsMaterialized": calls,
                                "LogicalClaims": calls,
                                "PhysicalStarts": calls,
                                "PhysicalFinishes": calls,
                                "Consumed": calls,
                                "Failed": 0,
                                "Cancelled": 0,
                                "Discarded": 0,
                            }
                        samples.append(sample)
                payload = {
                    "schema_version": "pysolate.gitchameleon-numpy-derived-plm.evidence.v1",
                    "cell_id": f"example-{task['example_id']}-rate-{rate}tps",
                    "source_commit": "a" * 40,
                    "source_tree": "b" * 40,
                    "source_tree_state": "clean",
                    "host_id": "gpu31",
                    "artifact_sha256": "sha256:" + "c" * 64,
                    "manifest_sha256": self.manifest_sha,
                    "dataset_name": self.manifest["dataset"]["name"],
                    "dataset_commit": self.manifest["dataset"]["commit"],
                    "dataset_sha256": self.manifest["dataset"]["sha256"],
                    "dataset_row": task["dataset_row"],
                    "example_id": task["example_id"],
                    "target_numpy_version": task["target_numpy_version"],
                    "api": task["api"],
                    "input_count": calls,
                    "suffix_tokens": task["suffix_tokens"],
                    "tokens_per_second": rate,
                    "source_window_ms": cell["source_window_ms"],
                    "provider_delay_ms": 200,
                    "source_sha256": task["source_sha256"],
                    "mock_stream_tokenizer": self.manifest["mock_stream"]["tokenizer"],
                    "mock_stream_tokenizer_version": self.manifest["mock_stream"]["tokenizer_version"],
                    "runs": runs,
                    "samples": samples,
                }
                (raw / f"example-{task['example_id']}-rate-{rate}tps.json").write_text(json.dumps(payload) + "\n")

    def test_analysis_accepts_complete_paired_matrix(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_matrix(root)
            summary = self.analyzer.analyze_directory(root / "raw", MANIFEST, expected_runs=2, bootstrap_samples=100, seed=7)
            self.assertEqual(60, summary["cell_count"])
            self.assertEqual(240, summary["sample_count"])
            self.assertEqual(120, summary["paired_comparison_count"])
            self.assertEqual([30, 30, 30, 30], [row["end_to_end"]["pair_count"] for row in summary["rates"]])
            self.assertTrue(summary["correctness"]["manifest_identity_valid"])

    def test_analysis_rejects_missing_cell(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_matrix(root)
            next((root / "raw").glob("*.json")).unlink()
            with self.assertRaisesRegex(ValueError, "cell matrix"):
                self.analyzer.analyze_directory(root / "raw", MANIFEST, expected_runs=2, bootstrap_samples=100, seed=7)

    def test_analysis_rejects_manifest_identity_drift(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_matrix(root)
            path = next((root / "raw").glob("*.json"))
            payload = json.loads(path.read_text())
            payload["manifest_sha256"] = "sha256:" + "0" * 64
            path.write_text(json.dumps(payload) + "\n")
            with self.assertRaisesRegex(ValueError, "manifest identity"):
                self.analyzer.analyze_directory(root / "raw", MANIFEST, expected_runs=2, bootstrap_samples=100, seed=7)


if __name__ == "__main__":
    unittest.main()
