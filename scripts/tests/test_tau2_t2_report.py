import copy
import importlib.util
import pathlib
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-t2-report.py"
SPEC = importlib.util.spec_from_file_location("tau2_t2_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


class Tau2T2ReportTests(unittest.TestCase):
    def prereg(self):
        protocol = {"model": reporter.MODEL, "seed": 42, "temperature": 0.0, "post_provider_reruns": 0, "max_total_provider_invocations_per_trial": 32, "max_agent_model_invocations_per_trial": 16}
        private_tasks = []
        public_tasks = []
        for index in range(16):
            task_id = str(index + 1)
            body = {"id": task_id}
            actions = [{"name": "get_user_details", "arguments": {"user_id": f"u{index}"}}]
            private_tasks.append({"task_id": task_id, "task_body": body, "reference_actions": actions})
            public_tasks.append({"task_id": task_id, "task_sha256": reporter.sha(reporter.canonical(body)), "reference_actions_sha256": reporter.sha(reporter.canonical(actions)), "reference_action_count": 1})
        source = {"revision": reporter.REVISION}
        public = {"schema_version": reporter.PUBLIC_SCHEMA, "source": source, "protocol": protocol, "tasks": public_tasks}
        public["identity"] = reporter.sha(reporter.canonical(public))
        private = {"schema_version": reporter.PRIVATE_SCHEMA, "public_identity": public["identity"], "source": source, "protocol": protocol, "tasks": private_tasks}
        return public, private

    def test_preregistration_identity_and_private_task_digests(self):
        public, private = self.prereg()
        self.assertEqual(len(reporter.validate_prereg(public, private)), 16)
        private["tasks"][0]["reference_actions"][0]["arguments"]["user_id"] = "tampered"
        with self.assertRaisesRegex(ValueError, "frozen digest"):
            reporter.validate_prereg(public, private)

    def test_missing_cell_fails_without_denominator_drop(self):
        public, private = self.prereg()
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(ValueError, "missing frozen cell"):
                reporter.build(public, private, pathlib.Path(directory))

    def test_receipt_identity_changes_with_source(self):
        receipt = {
            "run_id": "r", "capability_plan_sha256": "sha256:" + "a" * 64, "call_id": "c", "parent_call_id": "p",
            "capability": "cap", "operation_index": 0, "request_sha256": "b" * 64, "response_sha256": "c" * 64,
            "outcome": "ok", "source": {
                "schema_version": "pysolate.source-binding.v0", "claim_level": "source_bound",
                "document_id": "sha256:" + "a" * 64, "source_sha256": "sha256:" + "e" * 64,
                "occurrence_id": "sha256:" + "d" * 64, "capability": "cap", "dynamic_occurrence": 1,
                "start_line": 1, "start_column": 0, "end_line": 1, "end_column": 1,
            },
        }
        first = reporter.receipt_id(receipt)
        changed = copy.deepcopy(receipt)
        changed["source"]["occurrence_id"] = "sha256:" + "f" * 64
        self.assertNotEqual(first, reporter.receipt_id(changed))


if __name__ == "__main__":
    unittest.main()
