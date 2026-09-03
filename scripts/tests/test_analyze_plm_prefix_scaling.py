import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
ANALYZER = ROOT / "scripts/analyze-plm-prefix-scaling.py"


def load_analyzer():
    spec = importlib.util.spec_from_file_location("analyze_plm_prefix_scaling", ANALYZER)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class PLMPrefixScalingAnalysisTests(unittest.TestCase):
    def setUp(self) -> None:
        self.analyzer = load_analyzer()

    def write_matrix(self, root: pathlib.Path, runs: int = 2) -> None:
        raw = root / "raw"
        raw.mkdir(parents=True)
        for calls in (1, 2, 4, 8):
            for window in (0, 100, 200, 400):
                samples = []
                for trial in range(runs):
                    for order, treatment in enumerate(("serial_whole_file", "pysolate_pooled_prefix")):
                        saving = window * 1_000_000 + (calls - 1) * 200_000_000
                        serial = 4_000_000_000 + calls * 200_000_000 + window * 1_000_000 + trial
                        total = serial if treatment == "serial_whole_file" else serial - saving
                        outcome = {
                            "FinalProgramOutcome": "success",
                            "ResultSHA256": "sha256:" + "d" * 64,
                            "LogicalCalls": calls,
                            "PhysicalAttempts": calls,
                            "ReadyBeforeFinalize": calls if treatment == "pysolate_pooled_prefix" and window == 400 else 0,
                        }
                        sample = {
                            "trial": trial,
                            "order": order,
                            "treatment": treatment,
                            "post_begin_nanos": total,
                            "finalize_nanos": total - window * 1_000_000,
                            "provider_max_concurrent": calls if treatment == "pysolate_pooled_prefix" else 1,
                            "outcome": outcome,
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
                    "schema_version": "pysolate.plm-prefix-source-scaling.v1",
                    "cell_id": f"calls-{calls}-window-{window}ms",
                    "source_commit": "a" * 40,
                    "source_tree": "b" * 40,
                    "source_tree_state": "clean",
                    "host_id": "gpu31",
                    "artifact_sha256": "sha256:" + "c" * 64,
                    "runs": runs,
                    "provider_delay_ms": 200,
                    "call_count": calls,
                    "source_window_ms": window,
                    "source_sha256": "sha256:" + "e" * 64,
                    "samples": samples,
                }
                (raw / f"calls-{calls}-window-{window}ms.json").write_text(json.dumps(payload) + "\n")

    def test_analysis_accepts_complete_paired_matrix(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_matrix(root)
            summary = self.analyzer.analyze_directory(root / "raw", expected_runs=2, bootstrap_samples=100, seed=7)
            self.assertEqual(16, summary["cell_count"])
            self.assertEqual(64, summary["sample_count"])
            self.assertEqual(32, summary["paired_comparison_count"])
            self.assertTrue(summary["correctness"]["all_samples_valid"])
            self.assertEqual(16, len(summary["cells"]))

    def test_analysis_rejects_missing_cell(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_matrix(root)
            (root / "raw/calls-8-window-400ms.json").unlink()
            with self.assertRaisesRegex(ValueError, "cell matrix"):
                self.analyzer.analyze_directory(root / "raw", expected_runs=2, bootstrap_samples=100, seed=7)

    def test_analysis_rejects_wrong_call_accounting(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            self.write_matrix(root)
            path = root / "raw/calls-4-window-100ms.json"
            payload = json.loads(path.read_text())
            payload["samples"][0]["outcome"]["PhysicalAttempts"] = 3
            path.write_text(json.dumps(payload) + "\n")
            with self.assertRaisesRegex(ValueError, "call accounting"):
                self.analyzer.analyze_directory(root / "raw", expected_runs=2, bootstrap_samples=100, seed=7)

    def test_bootstrap_is_seeded(self) -> None:
        first = self.analyzer.bootstrap_median_interval([1, 2, 3, 4, 5], 200, 11)
        second = self.analyzer.bootstrap_median_interval([1, 2, 3, 4, 5], 200, 11)
        self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()
