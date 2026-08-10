import importlib.util
import json
import pathlib
import sys
import types
import unittest
from unittest import mock

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
        self._native_module = sys.modules.get("_agent_runtime_host")
        native_stub = types.ModuleType("_agent_runtime_host")
        native_stub.seal_imports = lambda names: None  # type: ignore[attr-defined]
        sys.modules["_agent_runtime_host"] = native_stub
        self.runtime = load_bootstrap()
        self.runtime._initialize("{}")

    def tearDown(self):
        if self._native_module is None:
            sys.modules.pop("_agent_runtime_host", None)
        else:
            sys.modules["_agent_runtime_host"] = self._native_module

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

    def test_accepts_empty_preflight_requirements_and_rejects_unhandled_nonempty_requirements(self):
        response = self.execute(requirements=[])
        self.assertEqual("ok", response["status"])
        response = self.execute(requirements=["posix"])
        self.assertEqual("error", response["status"])
        self.assertEqual("invalid_request", response["error"]["code"])

    def test_accepts_host_admitted_compatibility_manifest(self):
        response = self.execute(code="import json\nresult = {'value': inputs['value'] + 1}", compatibility={"profile": "base", "imports": ["json"]})
        self.assertEqual("ok", response["status"])

        response = self.execute(compatibility={"profile": "base"})
        self.assertEqual("error", response["status"])
        self.assertEqual("invalid_request", response["error"]["code"])

    def test_validates_static_absolute_import_preamble_with_exact_declared_roots(self):
        request = {
            "run_id": "static-imports",
            "code": (
                '\"\"\"agent program\"\"\"\n'
                "import json.decoder as decoder\n"
                "from pathlib import PurePosixPath\n"
                "result = decoder.JSONDecoder().decode('1')\n"
            ),
            "inputs": {},
            "compatibility": {"profile": "base", "imports": ["json", "pathlib"]},
        }
        raw = json.dumps(request)
        self.assertEqual(0, self.runtime._validate_request_source(raw))
        response = json.loads(self.runtime._execute(raw))
        self.assertEqual("ok", response["status"])
        self.assertEqual(1, response["result"])

    def test_rejects_non_static_agent_import_forms_before_execution(self):
        cases = {
            "dunder": "result = __import__(inputs['module'])",
            "dunder alias": "loader = __import__\nresult = loader(inputs['module'])",
            "importlib": "import importlib\nresult = importlib.import_module('json')",
            "nested": "def load():\n    import json\n    return json\nresult = load()",
            "conditional": "if inputs['enabled']:\n    import json\nresult = 1",
            "relative": "from .helpers import run\nresult = run()",
            "star": "from json import *\nresult = 1",
            "late": "result = 1\nimport json",
            "eval": "result = eval(inputs['expression'])",
            "exec": "exec(inputs['source'])\nresult = 1",
        }
        for name, code in cases.items():
            with self.subTest(name=name):
                runtime = load_bootstrap()
                runtime._initialize("{}")
                request = {
                    "run_id": name,
                    "code": code,
                    "inputs": {"module": "json", "enabled": True, "expression": "1", "source": "result=1"},
                    "compatibility": {"profile": "base", "imports": ["json"]},
                }
                raw = json.dumps(request)
                self.assertEqual(1, runtime._validate_request_source(raw))
                response = json.loads(runtime._execute(raw))
                self.assertEqual("source_contract_unsupported", response["error"]["code"])

    def test_static_import_aliases_preserve_original_preamble_order(self):
        response = self.execute(
            code="import json as inputs\nresult = inputs.dumps({'ok': True})",
            inputs={"would": "otherwise overwrite alias"},
            compatibility={"profile": "base", "imports": ["json"]},
        )
        self.assertEqual("ok", response["status"])
        self.assertEqual('{"ok": true}', response["result"])

    def test_profile_contract_fails_closed_without_native_seal(self):
        with mock.patch.dict(sys.modules, {"_agent_runtime_host": None}):
            response = self.execute(
                code="import json\nresult = 1",
                compatibility={"profile": "base", "imports": ["json"]},
            )
        self.assertEqual("error", response["status"])
        self.assertEqual("source_contract_unsupported", response["error"]["code"])

    def test_restricted_execution_builtins_remove_dynamic_compilation_entrypoints(self):
        request = {
            "run_id": "restricted-builtins",
            "code": "result = [name in __builtins__ for name in inputs['names']]",
            "inputs": {"names": ["__import__", "eval", "exec"]},
            "compatibility": {"profile": "base", "imports": []},
        }
        response = json.loads(self.runtime._execute(json.dumps(request)))
        self.assertEqual("ok", response["status"])
        self.assertEqual([False, False, False], response["result"])

        request["code"] = "result = __builtins__['__' + 'import__']('json')"
        response = json.loads(self.runtime._execute(json.dumps(request)))
        self.assertEqual("python_exception", response["error"]["code"])
        self.assertEqual("KeyError", response["error"]["error_type"])

    def test_rejects_import_declaration_drift_and_invalid_syntax(self):
        for imports in ([], ["json", "math"]):
            runtime = load_bootstrap()
            runtime._initialize("{}")
            request = {
                "run_id": "declaration-drift",
                "code": "import json\nresult = 1",
                "inputs": {},
                "compatibility": {"profile": "base", "imports": imports},
            }
            self.assertEqual(1, runtime._validate_request_source(json.dumps(request)))
        request["code"] = "result ="
        request["compatibility"]["imports"] = []
        self.assertEqual(2, runtime._validate_request_source(json.dumps(request)))

    def test_legacy_internal_request_without_profile_manifest_remains_available_for_build_probes(self):
        response = self.execute(
            code="import importlib\nresult = importlib.import_module(inputs['module']).__name__",
            inputs={"module": "json"},
        )
        self.assertEqual("ok", response["status"])
        self.assertEqual("json", response["result"])

    def test_exception_reporting_preserves_primary_error_when_traceback_formatting_fails(self):
        with mock.patch.object(self.runtime.traceback, "format_exc", side_effect=ImportError("sealed lazy import")):
            response = self.execute(
                code="raise ValueError('primary')",
                compatibility={"profile": "base", "imports": []},
            )
        self.assertEqual("python_exception", response["error"]["code"])
        self.assertEqual("ValueError", response["error"]["error_type"])
        self.assertEqual("ValueError: primary", response["error"]["traceback"])

    def test_returns_bounded_structured_exception(self):
        response = self.execute(code="raise ValueError('boom')")
        self.assertEqual("error", response["status"])
        self.assertEqual("python_exception", response["error"]["code"])
        self.assertEqual("ValueError", response["error"]["error_type"])
        self.assertIn("boom", response["error"]["message"])
        self.assertLessEqual(len(response["error"]["traceback"]), 16384)

    def test_returns_nonempty_message_for_exception_with_empty_string(self):
        response = self.execute(code="raise MemoryError()")
        self.assertEqual("error", response["status"])
        self.assertEqual("python_exception", response["error"]["code"])
        self.assertEqual("MemoryError", response["error"]["error_type"])
        self.assertEqual("MemoryError", response["error"]["message"])

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

    def test_numpy_ready_warmup_imports_and_retains_numpy_namespace(self):
        fake_numpy = types.ModuleType("numpy")

        class FakeArray:
            def sum(self):
                return 6

        setattr(fake_numpy, "arange", lambda _stop: FakeArray())
        setattr(fake_numpy, "__version__", "test")
        with mock.patch.dict(sys.modules, {"numpy": fake_numpy}):
            self.runtime._warmup("numpy-ready-v1")
        response = self.execute(code="result = {'prepared': prepared, 'sum': int(np.arange(4).sum())}")
        self.assertEqual("ok", response["status"])
        self.assertEqual({"prepared": 41, "sum": 6}, response["result"])

    def test_request_shell_warmup_is_allowlisted(self):
        self.runtime._warmup("request-shell-v1")
        called = []
        self.runtime.register_warmup_profile("custom.v1", lambda: called.append(True))
        self.runtime._warmup("custom.v1")
        self.assertEqual([True], called)
        with self.assertRaises(ValueError):
            self.runtime.register_warmup_profile("custom.v1", lambda: None)
        with self.assertRaises(ValueError):
            self.runtime.register_warmup_profile("Invalid", lambda: None)
        with self.assertRaises(ValueError):
            self.runtime._warmup("unknown")

    def test_initialize_and_prepare_require_json_or_source_strings(self):
        with self.assertRaises(TypeError):
            self.runtime._initialize(None)
        with self.assertRaises(TypeError):
            self.runtime._prepare(None)


if __name__ == "__main__":
    unittest.main()
