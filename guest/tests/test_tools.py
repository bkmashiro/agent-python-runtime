import importlib.util
import json
import pathlib
import sys
import types
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
TOOLS = ROOT / "guest" / "bootstrap" / "agent_runtime" / "tools.py"


def load_tools(host_call):
    host = types.ModuleType("_agent_runtime_host")
    setattr(host, "call", host_call)
    sys.modules["_agent_runtime_host"] = host
    spec = importlib.util.spec_from_file_location("agent_runtime_tools_test", TOOLS)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load tools module")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ToolsTests(unittest.TestCase):
    def tearDown(self):
        sys.modules.pop("_agent_runtime_host", None)

    def test_fetch_many_sends_typed_envelope_and_returns_ordered_items(self):
        observed = []

        def host_call(payload):
            observed.append(json.loads(payload))
            return json.dumps(
                {
                    "call_id": "fetch_many:1",
                    "status": "ok",
                    "result": {
                        "items": [
                            {
                                "request_id": "r1",
                                "status": "ok",
                                "http_status": 200,
                                "body": "{\"value\":42}",
                                "content_type": "application/json",
                                "error": None,
                            }
                        ]
                    },
                    "error": None,
                }
            )

        tools = load_tools(host_call)
        items = tools.fetch_many([{"request_id": "r1", "target": "fixture", "path": "/ok"}])
        self.assertEqual("r1", items[0]["request_id"])
        self.assertEqual(
            {
                "call_id": "fetch_many:1",
                "capability": "fetch_many",
                "arguments": {"requests": [{"request_id": "r1", "target": "fixture", "path": "/ok"}]},
            },
            observed[0],
        )

    def test_rejects_guest_url_headers_and_unknown_fields_before_host_call(self):
        calls = []
        tools = load_tools(lambda payload: calls.append(payload))
        for request in [
            {"request_id": "r1", "target": "fixture", "path": "/ok", "url": "https://evil.example"},
            {"request_id": "r1", "target": "fixture", "path": "/ok", "headers": {"Authorization": "guest"}},
            {"request_id": "r1", "target": "fixture", "path": "https://evil.example"},
            {"request_id": "r1", "target": "fixture", "path": "//evil.example"},
        ]:
            with self.assertRaises((TypeError, ValueError)):
                tools.fetch_many([request])
        self.assertEqual([], calls)

    def test_host_denial_raises_bounded_capability_error(self):
        def denied(_payload):
            return json.dumps(
                {
                    "call_id": "fetch_many:1",
                    "status": "denied",
                    "result": {"items": []},
                    "error": {"code": "capability_denied", "message": "not granted"},
                }
            )

        tools = load_tools(denied)
        with self.assertRaises(tools.CapabilityError) as raised:
            tools.fetch_many([{"request_id": "r1", "target": "fixture", "path": "/ok"}])
        self.assertEqual("capability_denied", raised.exception.code)

    def test_rejects_malformed_host_response(self):
        tools = load_tools(lambda _payload: "{\"status\":\"ok\",\"result\":{}}")
        with self.assertRaises(tools.CapabilityProtocolError):
            tools.fetch_many([{"request_id": "r1", "target": "fixture", "path": "/ok"}])


if __name__ == "__main__":
    unittest.main()
