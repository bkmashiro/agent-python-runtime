import pathlib
import unittest


class PLMGateContractTest(unittest.TestCase):
    def test_guest_mode_runs_the_current_exact_plm_matrix(self):
        script = (pathlib.Path(__file__).parents[1] / "unified-split-phase-gate.sh").read_text()
        self.assertIn("-run 'TestRealGuestPLM'", script)
        self.assertNotIn("TestRealGuestUnifiedSplitPhase", script)


if __name__ == "__main__":
    unittest.main()
