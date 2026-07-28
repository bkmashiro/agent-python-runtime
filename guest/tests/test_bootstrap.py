import importlib.util
import json
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
BOOTSTRAP = ROOT / "guest" / "bootstrap" / "agent_runtime" / "__init__.py"


def load_bootstrap():
    spec = importlib.util.spec_from_file_location("agent_runtime_bootstrap", BOOTSTRAP)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load guest bootstrap")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.runtime = load_bootstrap()
        self.runtime._initialize("{}")

    def execute(self, **overrides):
        request = {
            "run_id": "run-1",
            "code": "result = {'value': inputs['value'] + 1}",
            "inputs": {"value": 2},
        }
        request.update(overrides)
        return json.loads(self.runtime._execute(json.dumps(request)))

    def test_executes_code_with_inputs_and_returns_result(self):
        response = self.execute()
        self.assertEqual("ok", response["status"])
        self.assertEqual({"value": 3}, response["result"])
        self.assertEqual([], response["receipts"])
        self.assertIsNone(response["error"])

    def test_rejects_unknown_request_fields(self):
        response = self.execute(capabilities=["fetch_many"])
        self.assertEqual("error", response["status"])
        self.assertEqual("invalid_request", response["error"]["code"])

    def test_returns_bounded_structured_exception(self):
        response = self.execute(code="raise ValueError('boom')")
        self.assertEqual("error", response["status"])
        self.assertEqual("python_exception", response["error"]["code"])
        self.assertEqual("ValueError", response["error"]["error_type"])
        self.assertIn("boom", response["error"]["message"])
        self.assertLessEqual(len(response["error"]["traceback"]), 16384)

    def test_accepts_every_json_value_shape(self):
        cases = [
            ("result = 'done'", "done"),
            ("result = 42", 42),
            ("result = 3.5", 3.5),
            ("result = True", True),
            ("result = None", None),
            ("result = [1, {'ok': True}, None]", [1, {"ok": True}, None]),
            ("result = {'items': [1, 2]}", {"items": [1, 2]}),
        ]
        for code, expected in cases:
            with self.subTest(code=code):
                response = self.execute(code=code)
                self.assertEqual("ok", response["status"])
                self.assertEqual(expected, response["result"])

    def test_rejects_results_outside_strict_json(self):
        for code in [
            "result = object()",
            "result = {1, 2}",
            "result = b'bytes'",
            "result = float('nan')",
            "result = float('inf')",
            "result = float('-inf')",
        ]:
            with self.subTest(code=code):
                response = self.execute(code=code)
                self.assertEqual("error", response["status"])
                self.assertEqual("result_not_json", response["error"]["code"])

    def test_trusted_prepare_is_available_to_later_execution(self):
        self.runtime._prepare("def transform(value):\n    return value * 4")
        response = self.execute(code="result = transform(inputs['value'])")
        self.assertEqual("ok", response["status"])
        self.assertEqual(8, response["result"])

    def test_request_shell_warmup_is_allowlisted(self):
        self.runtime._warmup("request-shell-v1")
        with self.assertRaises(ValueError):
            self.runtime._warmup("unknown")

    def test_initialize_and_prepare_require_json_or_source_strings(self):
        with self.assertRaises(TypeError):
            self.runtime._initialize(None)
        with self.assertRaises(TypeError):
            self.runtime._prepare(None)


if __name__ == "__main__":
    unittest.main()
