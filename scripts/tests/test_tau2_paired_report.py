import importlib.util
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-paired-report.py"
SPEC = importlib.util.spec_from_file_location("tau2_paired_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


class Tau2PairedReportTests(unittest.TestCase):
    def rows(self):
        direct = {
            "schema_version": "pysolate.tau2-paired-private.v1", "source_revision": reporter.REVISION,
            "lane": "direct", "model": reporter.MODEL, "seed": 42, "temperature": 0.0,
            "simulation": {"termination_reason": "agent_error", "reward_info": {"reward": 0.0}, "messages": [
                {"role": "assistant", "content": "text", "tool_calls": [{"name": "get_user_details"}]}
            ]},
        }
        treatment = {
            "schema_version": "pysolate.tau2-paired-private.v1", "source_revision": reporter.REVISION,
            "lane": "treatment", "model": reporter.MODEL, "seed": 42, "temperature": 0.0,
            "status": "unscorable", "failure_stage": "orchestrator_protocol_validation",
            "official_simulation_written": False, "official_reward": None,
            "pysolate_model_turns_started": 0, "pysolate_physical_calls": 0,
            "logs": [{"name": f"attempt-{i}.log", "sha256": "sha256:" + str(i + 1) * 64} for i in range(3)],
        }
        return direct, treatment

    def test_report_refuses_comparative_claim(self):
        direct, treatment = self.rows()
        report = reporter.build_report(direct, treatment)
        self.assertEqual(report["conclusion"], "PAIR_NOT_COMPARABLE")
        self.assertFalse(report["interpretation"]["model_runtime_comparison_supported"])
        self.assertEqual(report["treatment"]["pysolate_physical_calls"], 0)

    def test_rejects_fake_treatment_score_or_runtime_start(self):
        for field, value in (("official_reward", 0.0), ("pysolate_physical_calls", 1)):
            direct, treatment = self.rows()
            treatment[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                reporter.build_report(direct, treatment)


if __name__ == "__main__":
    unittest.main()
