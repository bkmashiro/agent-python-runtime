import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


class WorkflowTriggerContractTests(unittest.TestCase):
    def test_metered_workflows_are_manual_only(self) -> None:
        for relative in (
            ".github/workflows/ci.yml",
            ".github/workflows/guest-artifact.yml",
        ):
            with self.subTest(workflow=relative):
                text = (ROOT / relative).read_text(encoding="utf-8")
                trigger = text.split("\non:\n", 1)[1].split("\npermissions:\n", 1)[0]
                self.assertIn("  workflow_dispatch:", trigger)
                for forbidden in (
                    "  push:",
                    "  pull_request:",
                    "  schedule:",
                    "  workflow_run:",
                ):
                    self.assertNotIn(forbidden, trigger)


if __name__ == "__main__":
    unittest.main()
