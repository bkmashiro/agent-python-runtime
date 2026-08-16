import importlib.util
import json
import pathlib
import tempfile
import types
import unittest
from unittest import mock

MODULE_PATH = pathlib.Path(__file__).parents[1] / "natural-placement-report.py"
SPEC = importlib.util.spec_from_file_location("natural_placement_report", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
reporter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(reporter)


class NaturalPlacementReportTests(unittest.TestCase):
    def fixture(self, root: pathlib.Path):
        request = {
            "schema_version": "pysolate.natural-placement-request.v1",
            "source": {"dataset": "nvidia/Open-SWE-Traces", "revision": "r", "license_id": "cc-by-4.0", "source_sha256": "sha256:" + "1" * 64},
            "task": {"record_sha256": "sha256:" + "2" * 64, "record_body_sha256": "sha256:" + "3" * 64, "trajectory_sha256": "sha256:" + "4" * 64, "language": "python", "resolved": 1, "trajectory_messages": 95, "tool_name_counts": {"execute_bash": 31, "finish": 1, "str_replace_editor": 15}},
            "placement_contract": {"required_features": ["shell", "subprocess"], "mutable_workspace_observed": True, "expected_backend": "native_sandbox", "expected_reason": "required_native_feature", "pysolate_guest_calls": 0},
            "private_bodies_included": False,
        }
        request_path = root / "request.json"
        request_path.write_bytes(reporter.canonical(request) + b"\n")
        run_sha = reporter.digest(reporter.run_request_bytes(request))
        selected_decision = {"schema_version": "pysolate.placement-decision.v1", "status": "selected", "backend": "native_sandbox", "reason": "required_native_feature", "analyzer_version": "static-v1", "request_sha256": run_sha, "state_class": "portable_value"}
        selected_decision["identity"] = reporter.decision_identity(selected_decision)
        unavailable_decision = {"schema_version": "pysolate.placement-decision.v1", "status": "unavailable", "reason": "native_unavailable", "analyzer_version": "static-v1", "request_sha256": run_sha, "state_class": "portable_value"}
        unavailable_decision["identity"] = reporter.decision_identity(unavailable_decision)
        evidence = {
            "schema_version": reporter.EVIDENCE_SCHEMA, "source_request_sha256": reporter.digest(request_path.read_bytes()),
            "run_request_sha256": run_sha, "source": request["source"], "task": request["task"], "requirements": ["shell", "subprocess"],
            "lanes": {
                "selected_native": {"decision": selected_decision, "pysolate_backend_calls": 0, "native_backend_calls": 1, "promotion": False, "workspace_started": False, "effects_started": False},
                "native_unavailable": {"decision": unavailable_decision, "pysolate_backend_calls": 0, "native_backend_calls": 0, "error": "backend_unavailable", "workspace_started": False, "effects_started": False},
            },
            "model_calls": 0, "private_bodies_included": False,
        }
        evidence_path = root / "evidence.json"
        evidence_path.write_text(json.dumps(evidence))
        return request, request_path, evidence, evidence_path

    def validate(self, root, request, request_path, evidence_path):
        module = types.SimpleNamespace(build=lambda _root: request)
        with mock.patch.object(reporter, "load_control", return_value=module):
            return reporter.validate(root, request_path, evidence_path, root / "control.py")

    def test_rebuilds_body_safe_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            request, request_path, _, evidence_path = self.fixture(root)
            result = self.validate(root, request, request_path, evidence_path)
            self.assertEqual(result["classification"], "SUPPORTED_PLACEMENT_CONTROL")
            self.assertEqual(result["placement"]["pysolate_guest_calls"], 0)

    def test_rejects_decision_and_execution_tampering(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            request, request_path, evidence, evidence_path = self.fixture(root)
            for mutate in (
                lambda value: value["lanes"]["selected_native"].update(native_backend_calls=0),
                lambda value: value["lanes"]["selected_native"]["decision"].update(identity="sha256:" + "0" * 64),
                lambda value: value["lanes"]["native_unavailable"].update(pysolate_backend_calls=1),
            ):
                candidate = json.loads(json.dumps(evidence))
                mutate(candidate)
                evidence_path.write_text(json.dumps(candidate))
                with self.assertRaises(ValueError):
                    self.validate(root, request, request_path, evidence_path)


if __name__ == "__main__":
    unittest.main()
