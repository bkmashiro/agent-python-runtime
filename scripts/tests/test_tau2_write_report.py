import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-write-report.py"
SPEC = importlib.util.spec_from_file_location("tau2_write_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


class Tau2WriteReportTests(unittest.TestCase):
    def fixture(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        root = pathlib.Path(temp.name)
        arguments = {"exact": "operation"}
        content = "private body"
        source_body = b"result = tools.apply_task_11_reference_update()\n"
        source_sha = reporter.digest_bytes(source_body)
        operation = reporter.digest_json(arguments)
        grant_policy = {
            "benchmark": "tau2", "domain": "airline", "effect": "workspace_write", "operation_sha256": operation,
            "source_revision": reporter.EXPECTED_REVISION, "task_id": "11", "workspace_scope": "attempt_private",
        }
        grant_bytes = json.dumps(grant_policy, sort_keys=True, separators=(",", ":")).encode()
        (root / "grant-policy.json").write_bytes(grant_bytes)
        grant_sha = reporter.grant_identity(grant_policy)
        initial = "sha256:" + "1" * 64
        final_state = {"final": "private-state"}
        final_bytes = json.dumps(final_state, sort_keys=True, separators=(",", ":")).encode()
        final = reporter.digest_bytes(final_bytes)
        (root / "accepted-final-state.json").write_bytes(final_bytes)
        span = (1, 9, 1, 47)
        source_binding = {
            "schema_version": "pysolate.source-binding.v0", "claim_level": "source_bound",
            "document_id": reporter.source_document_identity(source_sha), "source_sha256": source_sha,
            "occurrence_id": reporter.source_occurrence_identity(source_sha, reporter.CAPABILITY, span),
            "capability": reporter.CAPABILITY, "dynamic_occurrence": 1,
            "start_line": 1, "start_column": 9, "end_line": 1, "end_column": 47,
        }
        specs = {
            "accepted": (1, "ok", "approved", "ok", True, final, "published", 2000, False),
            "rejected": (0, "denied", "rejected", None, False, initial, "discarded", 2000, False),
            "expired": (0, "denied", "expired", None, False, initial, "discarded", 20, False),
            "failure": (1, "error", "approved", "error", True, initial, "discarded", 2000, True),
        }
        lanes = []
        for name, (calls, outcome, status, approval_outcome, executed, final_sha, disposition, lease, injected) in specs.items():
            handler = "pysolate.tau2.airline.private-write.handler." + reporter.EXPECTED_REVISION
            if injected:
                handler += ".injected-failure"
            plan = {
                "schema_version": "pysolate.capability-plan.v7", "max_calls": 1,
                "capabilities": [{
                    "capability": reporter.CAPABILITY, "version": "v1", "description": "exact",
                    "effect_class": "workspace_write", "playback": "live_only", "handler_identity": handler,
                    "input_schema": {"type": "object", "properties": {}, "additionalProperties": False},
                    "output_schema": {"type": "object"},
                    "python": {"module": "tools", "method": "apply_task_11_reference_update", "arguments": [], "result_field": "content"},
                    "approval": {"mode": "lease", "lease_milliseconds": lease},
                }],
                "grants": [{"capability": reporter.CAPABILITY, "policy_sha256": grant_sha}],
            }
            plan_body = json.dumps(plan, separators=(",", ":")).encode()
            plan_sha = reporter.digest_bytes(plan_body)
            parent = "parent-" + name
            call_id = parent + ":program:1"
            approval_id = "apr_" + ("a" if name == "accepted" else "b" if name == "rejected" else "c" if name == "expired" else "d") * 64
            approval = {
                "request_id": approval_id, "run_id": parent, "plan_sha256": plan_sha, "call_id": call_id,
                "parent_call_id": parent, "capability": reporter.CAPABILITY, "arguments_sha256": reporter.digest_json({}),
                "status": status, "executed": executed,
            }
            if approval_outcome is not None:
                approval["dispatch_outcome"] = approval_outcome
            receipt = {
                "run_id": parent, "capability_plan_sha256": plan_sha, "call_id": call_id, "parent_call_id": parent,
                "approval_request_id": approval_id, "capability": reporter.CAPABILITY, "operation_index": 0,
                "request_sha256": reporter.digest_json({}), "outcome": outcome, "source": dict(source_binding),
            }
            if name == "accepted":
                receipt["response_sha256"] = reporter.digest_json({"content": content, "operation_sha256": operation, "state_sha256": final})
            receipt["receipt_id"] = reporter.receipt_identity(receipt)
            request = {"run_id": parent, "code": "wrapper\n" + source_body.decode(), "inputs": {}}
            response = {
                "status": "ok" if name == "accepted" else "error", "result": content if name == "accepted" else None,
                "receipts": [receipt], "capability_plan_sha256": plan_sha,
            }
            raw = {
                "request": name + "-request.json", "response": name + "-response.json",
                "source": name + "-source.py", "plan": name + "-plan.json",
            }
            request_body = json.dumps(request, separators=(",", ":")).encode()
            response_body = json.dumps(response, separators=(",", ":")).encode()
            for filename, body in ((raw["request"], request_body), (raw["response"], response_body), (raw["source"], source_body), (raw["plan"], plan_body)):
                (root / filename).write_bytes(body)
            workspace_ref = "sha256:" + ("6" if name == "accepted" else "7" if name == "rejected" else "8" if name == "expired" else "9") * 64
            event = {
                "action": "publish" if name == "accepted" else "discard", "attempt_ref": workspace_ref,
                "result_ref": workspace_ref if name == "accepted" else "", "verified": True,
                "post_state_sha256": final if name == "accepted" else "", "post_state_absent": name != "accepted",
            }
            lanes.append({
                "name": name, "handler_calls": calls, "initial_state_sha256": initial, "final_state_sha256": final_sha,
                "disposition": disposition, "plan_sha256": plan_sha, "workspace_ref": workspace_ref,
                "approval": [approval], "receipt": receipt, "request_sha256": reporter.digest_bytes(request_body),
                "guest_response_sha256": reporter.digest_bytes(response_body), "workspace_event": event, "raw_bodies": raw,
            })
        evidence = {
            "schema_version": reporter.PRIVATE_SCHEMA,
            "source": {"revision": reporter.EXPECTED_REVISION, "domain": "airline", "task_id": "11"},
            "artifact_sha256": "sha256:" + "2" * 64, "arguments": arguments,
            "grant_operation_sha256": operation, "accepted_tool_content": content,
            "grant_policy_file": "grant-policy.json", "grant_policy_sha256": reporter.digest_bytes(grant_bytes),
            "accepted_final_state_file": "accepted-final-state.json", "accepted_final_state_sha256": final,
            "lanes": lanes,
        }
        oracle_request = {
            "schema_version": "pysolate.tau2-write-oracle-request.v1", "source_revision": reporter.EXPECTED_REVISION,
            "domain": "airline", "task_id": "11", "call": {"call_id": "oracle-write", "tool": "update_reservation_flights", "arguments": arguments, "content": content},
            "assistant_text": "private communication", "observed_final_state": final_state,
        }
        oracle = {
            "schema_version": "pysolate.tau2-write-oracle.v1",
            "source": {"revision": reporter.EXPECTED_REVISION, "domain": "airline", "task_id": "11", "repository": "https://github.com/sierra-research/tau2-bench"},
            "reward_basis": ["DB", "COMMUNICATE"], "db_reward": 1.0, "communicate_reward": 1.0,
            "overall_reward": 1.0, "db_match": True, "communicate_met": True, "observed_final_state_match": True,
            "tool_calls": 1, "tool_bodies_included": False, "assistant_text_included": False,
        }
        return root, evidence, oracle_request, oracle

    def test_emits_body_safe_supported_report(self):
        root, evidence, request, oracle = self.fixture()
        report = reporter.validate(evidence, request, oracle, root)
        self.assertEqual(report["classification"], "SUPPORTED_BENCHMARK_PRIVATE_ONLY")
        self.assertTrue(report["causal_join"]["raw_guest_bodies_verified"])
        self.assertNotIn("private body", str(report))
        self.assertEqual(report["operation"]["capability"], reporter.CAPABILITY)

    def test_rejects_identity_and_oracle_tampering(self):
        mutations = [
            lambda e, q, o: e["lanes"][0]["receipt"]["source"].__setitem__("occurrence_id", "not-a-digest"),
            lambda e, q, o: e["lanes"][0]["receipt"]["source"].pop("capability"),
            lambda e, q, o: e["lanes"][0]["receipt"].__setitem__("receipt_id", "rcpt_" + "f" * 64),
            lambda e, q, o: e["lanes"][0].__setitem__("plan_sha256", "sha256:" + "f" * 64),
            lambda e, q, o: e["lanes"][0]["receipt"].__setitem__("capability", "private body injection"),
            lambda e, q, o: q.__setitem__("source_revision", "0" * 40),
            lambda e, q, o: q["call"].__setitem__("call_id", "different"),
            lambda e, q, o: o.clear(),
            lambda e, q, o: e["lanes"][0]["workspace_event"].__setitem__("verified", False),
        ]
        for index, mutate in enumerate(mutations):
            root, evidence, request, oracle = self.fixture()
            mutate(evidence, request, oracle)
            with self.subTest(index=index), self.assertRaises(ValueError):
                reporter.validate(evidence, request, oracle, root)

    def test_rejects_tampered_raw_guest_body(self):
        root, evidence, request, oracle = self.fixture()
        response = root / evidence["lanes"][0]["raw_bodies"]["response"]
        response.write_bytes(response.read_bytes() + b" ")
        with self.assertRaises(ValueError):
            reporter.validate(evidence, request, oracle, root)


if __name__ == "__main__":
    unittest.main()
