import importlib.util
import json
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-canary-report.py"
SPEC = importlib.util.spec_from_file_location("tau2_canary_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


def digest(ch):
    return "sha256:" + ch * 64


class Tau2CanaryReportTests(unittest.TestCase):
    def evidence(self):
        return {
            "schema_version": "pysolate.tau2-canary-private-evidence.v1",
            "source": {"revision": reporter.REVISION, "domain": "airline", "task_id": "3"},
            "artifact_sha256": digest("a"),
            "request_sha256": digest("b"),
            "guest_response_sha256": digest("c"),
            "capability_plan_sha256": digest("d"),
            "broker_call_count": 2,
            "receipts": [
                {"outcome": "ok", "response_sha256": "1" * 64},
                {"outcome": "ok", "response_sha256": "2" * 64},
            ],
            "source_occurrence_claim": "not_recorded",
            "result": {"answer": "4", "cabin": "economy", "membership": "silver", "passenger_count": 2},
            "raw_bodies": {"agent_request": "agent-request.json", "guest_response": "guest-response.json"},
        }

    def oracle(self):
        return {
            "schema_version": "pysolate.tau2-canary-oracle.v1",
            "source": {"repository": reporter.REPOSITORY, "revision": reporter.REVISION, "domain": "airline", "task_id": "3"},
            "reward_basis": ["DB", "COMMUNICATE"],
            "db_reward": 1.0,
            "communicate_reward": 1.0,
            "overall_reward": 1.0,
            "db_match": True,
            "communicate_met": True,
            "tool_calls": 2,
            "tool_bodies_included": False,
            "assistant_text_included": False,
        }

    def oracle_request(self):
        return {
            "calls": [
                {"content": "one"},
                {"content": "two"},
            ]
        }

    def test_report_requires_exact_receipt_result_oracle_and_tool_identity(self):
        evidence = self.evidence()
        request = self.oracle_request()
        evidence["receipts"][0]["response_sha256"] = reporter.response_digest("one")
        evidence["receipts"][1]["response_sha256"] = reporter.response_digest("two")
        report = reporter.build_report(evidence, self.oracle(), request)
        self.assertEqual(report["conclusion"], "SUPPORTED_WITH_RECORDED_GAP")
        self.assertTrue(report["oracle"]["tool_result_identity_match"])
        self.assertEqual(report["causal_join"]["source_occurrence"], "not_recorded")

    def test_report_rejects_mismatch(self):
        with self.assertRaises(ValueError):
            reporter.build_report(self.evidence(), self.oracle(), self.oracle_request())

    def test_report_requires_two_distinct_bound_occurrences_for_recorded_lane(self):
        evidence = self.evidence()
        request = self.oracle_request()
        evidence["receipts"][0]["response_sha256"] = reporter.response_digest("one")
        evidence["receipts"][1]["response_sha256"] = reporter.response_digest("two")
        evidence["fresh_runs"], evidence["tool_runs"] = 3, 2
        evidence["source_occurrence_claim"] = "recorded"
        for index, receipt in enumerate(evidence["receipts"]):
            receipt["source"] = {
                "schema_version": "pysolate.source-binding.v0",
                "claim_level": "source_bound",
                "source_sha256": digest("e"),
                "occurrence_id": digest(str(index + 1)),
                "dynamic_occurrence": 1,
                "start_line": 1,
                "end_line": 1,
            }
        report = reporter.build_report(evidence, self.oracle(), request)
        self.assertEqual(report["conclusion"], "SUPPORTED")
        self.assertEqual(report["lane"], "authored_reference_fresh_turns")
        self.assertEqual(report["runtime"]["fresh_runs"], 3)
        evidence["receipts"][1]["source"]["occurrence_id"] = evidence["receipts"][0]["source"]["occurrence_id"]
        with self.assertRaises(ValueError):
            reporter.build_report(evidence, self.oracle(), request)


if __name__ == "__main__":
    unittest.main()
