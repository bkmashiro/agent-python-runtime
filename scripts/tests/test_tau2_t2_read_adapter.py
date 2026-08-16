import importlib.util
import json
import pathlib
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-t2-read-adapter.py"
SPEC = importlib.util.spec_from_file_location("tau2_t2_read_adapter", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
adapter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(adapter)


class Tau2T2ReadAdapterTests(unittest.TestCase):
    def actions(self):
        return [
            {"name": "get_user_details", "requestor": "assistant", "arguments": {"user_id": "u1"}},
            {"name": "search_direct_flight", "requestor": "assistant", "arguments": {"origin": "A", "destination": "B", "date": "2024-01-01"}},
        ]

    def request(self):
        return {
            "schema_version": adapter.REQUEST_SCHEMA, "source_revision": adapter.REVISION,
            "domain": "airline", "task_id": "1", "call_id": "c1",
            "tool": "get_user_details", "arguments": {"user_id": "u1"},
        }

    def test_accepts_only_exact_reference_read(self):
        self.assertEqual(adapter.validate_request(self.request(), self.actions(), "1")["tool"], "get_user_details")
        for mutation in (
            lambda value: value.update(extra=True),
            lambda value: value.update(task_id="2"),
            lambda value: value.update(tool="cancel_reservation"),
            lambda value: value.update(arguments={"user_id": "other"}),
        ):
            candidate = self.request()
            mutation(candidate)
            with self.assertRaises(ValueError):
                adapter.validate_request(candidate, self.actions(), "1")

    def test_load_scope_is_task_exact(self):
        manifest = {
            "schema_version": adapter.PRIVATE_SCHEMA,
            "source": {"revision": adapter.REVISION, "domain": "airline"},
            "tasks": [{"task_id": "1", "reference_actions": self.actions()}],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "manifest.json"
            path.write_text(json.dumps(manifest))
            self.assertEqual(adapter.load_scope(path, "1"), self.actions())
            with self.assertRaises(ValueError):
                adapter.load_scope(path, "2")


if __name__ == "__main__":
    unittest.main()
