import importlib.util
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-pure-report.py"
SPEC = importlib.util.spec_from_file_location("tau2_pure_report", MODULE_PATH)
assert SPEC and SPEC.loader
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


class Tau2PureReportTests(unittest.TestCase):
    def fixture(self):
        rows = [
            {"domain": "airline", "task_id": str(index), "communicate_items": 0, "nl_assertions": 1, "reward_basis": ["DB", "COMMUNICATE"]}
            for index in range(7)
        ]
        rows += [
            {"domain": "retail", "task_id": "24", "communicate_items": 2, "nl_assertions": 1, "reward_basis": ["DB", "NL_ASSERTION"]},
            {"domain": "retail", "task_id": "57", "communicate_items": 0, "nl_assertions": 0, "reward_basis": ["DB", "NL_ASSERTION"]},
            {"domain": "mock", "task_id": "x", "communicate_items": 0, "nl_assertions": 2, "reward_basis": ["DB", "COMMUNICATE"]},
        ]
        qualification = {
            "schema_version": "pysolate.tau2-pure-oracle.v1",
            "oracle": {"correct_control": 1.0, "empty_control": 0.0, "wrong_control": 0.0, "official_upstream_component": True, "official_task_overall": False},
        }
        direct = {
            "schema_version": "pysolate.tau2-pure-paired-private.v1", "source_revision": reporter.EXPECTED_REVISION,
            "lane": "direct", "model": "deepseek/deepseek-v4-pro", "seed": 42, "temperature": 0.0,
            "evaluation_type": "COMMUNICATE", "status": "completed", "tool_calls": 2,
            "simulation": {"reward_info": {"reward": 0.0}},
        }
        authored = {
            "schema_version": "pysolate.tau2-pure-turn-private.v1",
            "semantic_call_sites": 0, "broker_call_count": 0, "receipt_count": 0,
            **{field: "sha256:" + "1" * 64 for field in ("artifact_sha256", "capability_plan_sha256", "source_sha256", "request_sha256", "response_sha256")},
        }
        return rows, qualification, direct, authored

    def test_emits_fail_closed_body_safe_closeout(self):
        report = reporter.validate(*self.fixture())
        self.assertEqual(report["classification"], "NO_ELIGIBLE_PURE_NATURAL_TASK")
        self.assertFalse(report["natural_direct_probe"]["treatment_run"])
        self.assertEqual(report["zero_authority_runtime_control"]["broker_call_count"], 0)
        self.assertFalse(report["raw_model_bodies_included"])

    def test_rejects_hidden_direct_tool_calls(self):
        rows, qualification, direct, authored = self.fixture()
        direct["tool_calls"] = 0
        with self.assertRaises(ValueError):
            reporter.validate(rows, qualification, direct, authored)


if __name__ == "__main__":
    unittest.main()
