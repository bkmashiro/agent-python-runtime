import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-read-adapter.py"
SPEC = importlib.util.spec_from_file_location("tau2_read_adapter", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
adapter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(adapter)


class Tau2ReadAdapterTests(unittest.TestCase):
    def request(self, tool="get_reservation_details", arguments=None):
        if arguments is None:
            arguments = {"reservation_id": "JMO1MG"}
        return {
            "schema_version": "pysolate.tau2-read-request.v1",
            "source_revision": adapter.EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "3",
            "call_id": "canary:1",
            "tool": tool,
            "arguments": arguments,
        }

    def test_accepts_only_exact_canary_read_scope(self):
        reservation = adapter.validate_request(self.request())
        self.assertEqual(reservation["tool"], "get_reservation_details")
        user = adapter.validate_request(
            self.request("get_user_details", {"user_id": "anya_garcia_5901"})
        )
        self.assertEqual(user["arguments"], {"user_id": "anya_garcia_5901"})

    def test_rejects_revision_task_tool_resource_and_unknown_fields(self):
        mutations = []
        wrong_revision = self.request()
        wrong_revision["source_revision"] = "0" * 40
        mutations.append(wrong_revision)
        wrong_task = self.request()
        wrong_task["task_id"] = "4"
        mutations.append(wrong_task)
        wrong_tool = self.request("cancel_reservation", {"reservation_id": "JMO1MG"})
        mutations.append(wrong_tool)
        wrong_resource = self.request()
        wrong_resource["arguments"] = {"reservation_id": "OTHER"}
        mutations.append(wrong_resource)
        unknown = self.request()
        unknown["operator_note"] = "no"
        mutations.append(unknown)
        for request in mutations:
            with self.subTest(request=request):
                with self.assertRaises(ValueError):
                    adapter.validate_request(request)

    def test_response_envelope_does_not_project_task_or_source_bodies(self):
        response = adapter.response_envelope(self.request(), '{"ok":true}')
        self.assertEqual(
            set(response),
            {"schema_version", "source_revision", "domain", "task_id", "call_id", "tool", "content"},
        )
        self.assertEqual(response["content"], '{"ok":true}')


if __name__ == "__main__":
    unittest.main()
