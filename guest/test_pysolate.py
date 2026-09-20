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

        low_level = types.ModuleType("_pysolate")
        low_level.call = call
        sys.modules["_pysolate"] = low_level
        sys.modules.pop("pysolate", None)
        self.shim = importlib.import_module("pysolate")
        self.exports = self.shim.configure([
            {"name": "lookup", "python_path": "lookup", "allow_early_read": True},
            {"name": "mcp.market/get-price", "python_path": "stock.getprice", "allow_early_read": True},
            {"name": "mcp.market/history", "python_path": "stock.history", "allow_early_read": True},
        ])

    def tearDown(self):
        sys.modules.pop("pysolate", None)
        sys.modules.pop("_pysolate", None)

    def test_named_function_and_namespace_dispatch_canonical_names(self):
        self.assertEqual(self.exports["lookup"](key="book"), {"tool": "lookup", "args": {"key": "book"}})
        self.assertEqual(
            self.exports["stock"].getprice(symbol="AAPL"),
            {"tool": "mcp.market/get-price", "args": {"symbol": "AAPL"}},
        )
        self.assertEqual(
            self.shim.stock.history(symbol="AAPL", days=2),
            {"tool": "mcp.market/history", "args": {"symbol": "AAPL", "days": 2}},
        )

    def test_runtime_discovery_api_is_not_public(self):
        self.assertFalse(hasattr(self.shim, "tools"))
        self.assertFalse(hasattr(self.shim, "describe"))
        self.assertFalse(hasattr(self.shim, "names"))

    def test_namespace_collisions_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "namespace collision"):
            self.shim.configure([
                {"name": "stock-root", "python_path": "stock"},
                {"name": "stock-price", "python_path": "stock.getprice"},
            ])


if __name__ == "__main__":
    unittest.main()
