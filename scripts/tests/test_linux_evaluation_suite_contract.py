import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SUITE = ROOT / "scripts" / "run-linux-evaluation-suite.sh"
GATE = ROOT / "scripts" / "prepared-family-economics-gate.sh"


class LinuxEvaluationSuiteContractTests(unittest.TestCase):
    def test_suite_is_runtime_only_and_runs_three_pysolate_lanes(self) -> None:
        text = SUITE.read_text()
        self.assertIn("plm-economics-gate.sh", text)
        self.assertIn("plm-multiread-economics-gate.sh", text)
        self.assertIn("prepared-family-economics-gate.sh", text)
        self.assertIn("cmd/transparent-campaign", text)
        self.assertNotIn("shimmy", text.lower())
        self.assertNotIn("integrated-evidence", text)

    def test_suite_binds_source_and_refuses_output_overwrite(self) -> None:
        text = SUITE.read_text()
        for flag in ("--source-commit", "--source-tree", "--source-epoch", "--output"):
            self.assertIn(flag, text)
        self.assertIn("output must be absent or an empty real directory", text)
        self.assertIn("pysolate.linux-evaluation-suite.v1", text)

    def test_prepared_family_gate_is_explicit_and_bounded(self) -> None:
        text = GATE.read_text()
        self.assertIn("PYSOLATE_PREPARED_FAMILY_ECONOMICS_RUNS", text)
        self.assertIn("PYSOLATE_PREPARED_FAMILY_ECONOMICS_FANOUT", text)
        self.assertIn("RUNS < 3 || RUNS > 20", text)
        self.assertIn("FANOUT < 1 || FANOUT > 8", text)
        self.assertIn("TestPreparedFamilyEconomicsFixture", text)


if __name__ == "__main__":
    unittest.main()
