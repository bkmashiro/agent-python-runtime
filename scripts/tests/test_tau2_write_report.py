import importlib.util
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-write-report.py"
SPEC = importlib.util.spec_from_file_location("tau2_write_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


class Tau2WriteReportTests(unittest.TestCase):
    def fixture(self):
        arguments = {"exact": "operation"}
        content = "private body"
        initial = "sha256:" + "1" * 64
        observed_final_state = {"final": "private-state"}
        final = reporter.digest_json(observed_final_state)
        operation = reporter.digest_json(arguments)
        source = {
            "schema_version": "pysolate.source-binding.v0", "claim_level": "source_bound",
            "source_sha256": "sha256:" + "3" * 64, "occurrence_id": "occurrence-1",
            "dynamic_occurrence": 1, "start_line": 1, "start_column": 0, "end_line": 1, "end_column": 10,
        }
        specs = {
            "accepted": (1, "ok", "approved", "ok", True, final, "published"),
            "rejected": (0, "denied", "rejected", None, False, initial, "discarded"),
            "expired": (0, "denied", "expired", None, False, initial, "discarded"),
            "failure": (1, "error", "approved", "error", True, initial, "discarded"),
        }
        lanes = []
        for name, (calls, outcome, status, approval_outcome, executed, final_state, disposition) in specs.items():
            plan = "sha256:" + ("4" if name in ("accepted", "rejected") else "5") * 64
            parent = "parent-" + name
            call_id = parent + ":program:1"
            response_sha = None
            if name == "accepted":
                response_sha = reporter.digest_json({"content": content, "operation_sha256": operation, "state_sha256": final})
            lanes.append({
                "name": name, "handler_calls": calls, "initial_state_sha256": initial,
                "final_state_sha256": final_state, "disposition": disposition,
                "plan_sha256": plan,
                "approval": [{"status": status, "dispatch_outcome": approval_outcome, "executed": executed, "plan_sha256": plan, "call_id": call_id, "parent_call_id": parent}],
                "receipt": {
                    "outcome": outcome, "capability": "tau2.exact.write",
                    "capability_plan_sha256": plan, "call_id": call_id, "parent_call_id": parent,
                    "request_sha256": reporter.digest_json({}), "response_sha256": response_sha,
                    "source": dict(source),
                },
            })
        evidence = {
            "schema_version": "pysolate.tau2-private-write-evidence.v1",
            "source": {"revision": reporter.EXPECTED_REVISION, "domain": "airline", "task_id": "11"},
            "arguments": arguments, "grant_operation_sha256": operation, "accepted_tool_content": content, "lanes": lanes,
        }
        oracle_request = {"call": {"arguments": arguments, "content": content}, "observed_final_state": observed_final_state}
        oracle = {
            "schema_version": "pysolate.tau2-write-oracle.v1", "db_reward": 1.0,
            "communicate_reward": 1.0, "overall_reward": 1.0, "db_match": True, "communicate_met": True,
            "observed_final_state_match": True,
        }
        return evidence, oracle_request, oracle

    def test_emits_body_safe_supported_report(self):
        evidence, request, oracle = self.fixture()
        report = reporter.validate(evidence, request, oracle)
        self.assertEqual(report["classification"], "SUPPORTED_BENCHMARK_PRIVATE_ONLY")
        self.assertTrue(report["causal_join"]["final_state_to_receipt_response"])
        self.assertNotIn("private body", str(report))
        self.assertFalse(report["scope"]["model_generated"])
        self.assertFalse(report["oracle"]["observed_final_state_included"])

    def test_rejects_tampered_final_state_join(self):
        evidence, request, oracle = self.fixture()
        evidence["lanes"][0]["final_state_sha256"] = "sha256:" + "9" * 64
        with self.assertRaises(ValueError):
            reporter.validate(evidence, request, oracle)


if __name__ == "__main__":
    unittest.main()
