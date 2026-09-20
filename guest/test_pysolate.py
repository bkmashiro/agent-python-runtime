import importlib
import json
import sys
import types
import unittest


class ToolShimTests(unittest.TestCase):
    def setUp(self):
        self.requests = []

        def call(request):
            decoded = json.loads(request)
            self.requests.append(decoded)
            return json.dumps({"value": {"tool": decoded["tool"], "args": decoded["args"]}}).encode()

        sys.modules["_pysolate"] = types.SimpleNamespace(call=call)
        sys.modules.pop("pysolate", None)
        self.shim = importlib.import_module("pysolate")
        self.shim.configure([
            {
                "name": "lookup",
                "description": "Look up a value",
                "input_schema": {"type": "object"},
                "annotations": {"read_only_hint": True},
                "inject_global": True,
            },
            {
                "name": "mcp.filesystem.read-file",
                "description": "Read one approved file",
                "input_schema": {"type": "object", "required": ["path"]},
                "annotations": {"read_only_hint": True, "open_world_hint": False},
                "inject_global": False,
            },
        ])

    def tearDown(self):
        sys.modules.pop("pysolate", None)
        sys.modules.pop("_pysolate", None)

    def test_dynamic_attribute_and_canonical_lookup(self):
        self.assertEqual(self.shim.tools.lookup(key="book"), {"tool": "lookup", "args": {"key": "book"}})
        self.assertEqual(
            self.shim.tools["mcp.filesystem.read-file"](path="notes.txt"),
            {"tool": "mcp.filesystem.read-file", "args": {"path": "notes.txt"}},
        )

    def test_metadata_is_discoverable_but_immutable_to_callers(self):
        self.assertEqual(self.shim.tools.names(), ("lookup", "mcp.filesystem.read-file"))
        description = self.shim.tools.describe("mcp.filesystem.read-file")
        self.assertEqual(description["description"], "Read one approved file")
        description["description"] = "changed"
        self.assertEqual(self.shim.tools.describe("mcp.filesystem.read-file")["description"], "Read one approved file")

    def test_exact_call_and_unknown_tool(self):
        value = self.shim.tools.call("mcp.filesystem.read-file", path="a.txt")
        self.assertEqual(value["tool"], "mcp.filesystem.read-file")
        with self.assertRaisesRegex(KeyError, "unknown tool"):
            self.shim.tools.call("absent")


if __name__ == "__main__":
    unittest.main()
