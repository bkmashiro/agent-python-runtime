import importlib.util
import json
import pathlib
import sys
import types
import unittest


BOOTSTRAP = pathlib.Path(__file__).resolve().parents[2] / "guest" / "bootstrap" / "agent_runtime" / "__init__.py"


def load_runtime():
    spec = importlib.util.spec_from_file_location("agent_runtime_output_contract_test", BOOTSTRAP)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    # The bootstrap is now a package with relative helper imports. Register the
    # fresh package instance while its loader resolves those imports.
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    module._initialize("{}")
    return module


def execute(module, code):
    request = json.dumps({"run_id": "output-contract", "code": code, "inputs": {}})
    assert module._validate_request_source(request) == module._SOURCE_CONTRACT_OK
    return json.loads(module._execute(request))


class GuestOutputContractTests(unittest.TestCase):
    def test_return_and_multiple_prints(self):
        response = execute(load_runtime(), "print('first')\nprint('second')\nreturn {'value': 42}")
        self.assertEqual(response["status"], "ok")
        self.assertEqual(response["logs"], ["first", "second"])
        self.assertEqual(response["result"], {"value": 42})
        self.assertTrue(response["result_present"])
        self.assertEqual(response["result_source"], "return")
        self.assertEqual(set(response["source_contract"]), {"schema_version", "authority", "model_source_sha256", "effective_ast_sha256", "wrapper_contract_sha256"})

    def test_legacy_missing_and_explicit_none_are_distinct(self):
        legacy = execute(load_runtime(), "result = {'value': 7}")
        missing = execute(load_runtime(), "value = 7")
        explicit_none = execute(load_runtime(), "return None")
        self.assertEqual((legacy["result_source"], legacy["result_present"]), ("legacy_result", True))
        self.assertEqual((missing["result_source"], missing["result_present"], missing["result"]), ("missing", False, None))
        self.assertEqual((explicit_none["result_source"], explicit_none["result_present"], explicit_none["result"]), ("return", True, None))

    def test_future_import_and_single_execution(self):
        module = load_runtime()
        module._prepared_globals["calls"] = []
        response = execute(module, "from __future__ import annotations\ncalls.append('once')\nreturn {'calls': len(calls)}")
        self.assertEqual(response["result"], {"calls": 1})
        self.assertEqual(module._prepared_globals["calls"], ["once"])

    def test_reserved_wrapper_names_are_rejected(self):
        sources = [
            "_pysolate_agent_main = 1\nreturn 1",
            "def _pysolate_agent_main():\n    pass\nreturn 1",
            "class _pysolate_agent_main:\n    pass\nreturn 1",
            "import json as _pysolate_agent_main\nreturn 1",
            "match {'value': 1}:\n    case {'value': _pysolate_agent_main}:\n        return 1",
        ]
        for index, source in enumerate(sources):
            with self.subTest(source=source):
                module = load_runtime()
                request = json.dumps({"run_id": f"reserved-{index}", "code": source, "inputs": {}})
                self.assertEqual(module._validate_request_source(request), module._SOURCE_CONTRACT_INVALID)

    def test_late_future_import_is_rejected(self):
        module = load_runtime()
        request = json.dumps({"run_id": "late-future", "code": "value = 1\nfrom __future__ import annotations\nreturn value", "inputs": {}})
        self.assertEqual(module._validate_request_source(request), module._SOURCE_CONTRACT_INVALID)

    def test_wrapper_does_not_legalize_module_level_yield(self):
        module = load_runtime()
        request = json.dumps({"run_id": "yield", "code": "yield 1", "inputs": {}})
        self.assertEqual(module._validate_request_source(request), module._SOURCE_CONTRACT_INVALID)

    def test_wrapper_preserves_module_global_semantics(self):
        response = execute(load_runtime(), "counter = 0\ndef callback():\n    global counter\n    counter += 1\ndef program():\n    callback()\ncallback()\nprogram()\nreturn {'counter': counter, 'freevars': list(program.__code__.co_freevars)}")
        shadowed = execute(load_runtime(), "globals = 1\nvalue: int = 2\nresult = {'globals': globals, 'value': value, 'annotation': __annotations__['value'].__name__}")
        annotated = execute(load_runtime(), "def f(x: (annotation_binding := int)) -> (return_binding := str):\n    pass\nreturn {'argument': annotation_binding.__name__, 'return': return_binding.__name__}")
        self.assertEqual(response["status"], "ok")
        self.assertEqual(response["result"], {"counter": 2, "freevars": []})
        self.assertEqual(shadowed["result"], {"globals": 1, "value": 2, "annotation": "int"})
        self.assertEqual(annotated["result"], {"argument": "int", "return": "str"})

    def test_wrapper_preserves_module_annotations(self):
        plain = execute(load_runtime(), "if True:\n    x: list[int] = [1]\nreturn {'x': x, 'annotation': __annotations__['x'].__name__}")
        future = execute(load_runtime(), "from __future__ import annotations\ny: list[int] = [2]\nreturn {'y': y, 'annotation': __annotations__['y']}")
        self.assertEqual(plain["result"], {"x": [1], "annotation": "list"})
        self.assertEqual(future["result"], {"y": [2], "annotation": "list[int]"})

    def test_wrapper_preserves_comprehension_walrus_global(self):
        response = execute(load_runtime(), "[z := i for i in range(2)]\ndef inspect_z():\n    return globals().get('z'), list(inspect_z.__code__.co_freevars)\nreturn inspect_z()")
        self.assertEqual(response["status"], "ok")
        self.assertEqual(response["result"], [1, []])

    def test_stdout_aliases_and_swallowed_limit_cannot_bypass_capture(self):
        response = execute(load_runtime(), "import sys\nsys.__stdout__.write('raw\\n')\nsys.stdout = sys.__stdout__\nprint('printed')\nreturn 1")
        self.assertEqual(response["logs"], ["raw", "printed"])
        swallowed = execute(load_runtime(), "try:\n    print('x' * 70000)\nexcept BaseException:\n    pass\nreturn 1")
        self.assertEqual((swallowed["status"], swallowed["error"]["code"]), ("error", "output_limit_exceeded"))

    def test_streaming_print_uses_same_bound_and_terminal_metadata(self):
        previous = sys.modules.get("_agent_runtime_host")
        host = types.ModuleType("_agent_runtime_host")
        setattr(host, "seal_imports", lambda _: None)
        sys.modules["_agent_runtime_host"] = host
        try:
            runtime = load_runtime()
            runtime._stream_begin({}, 0)
            runtime._stream_chunk("import sys\n")
            runtime._stream_chunk("sys.__stdout__.write('first\\n')\nprint('second')\nresult = {'value': 1}\n")
            ended = runtime._stream_end()
            self.assertEqual(ended["logs"], ["first", "second"])
            self.assertEqual((ended["result_source"], ended["result_present"], ended["result"]), ("legacy_result", True, {"value": 1}))

            runtime._stream_begin({}, 0)
            with self.assertRaises(runtime._OutputLimitExceeded):
                runtime._stream_chunk("try:\n    print('🙂' * 70000)\nexcept BaseException:\n    pass\n")
            self.assertTrue(runtime._stream_session.ended)
            self.assertEqual(runtime._stream_session.stdout.logs()[-1], runtime._STDOUT_TRUNCATION_MARKER)
            with self.assertRaises((ValueError, RuntimeError)):
                runtime._stream_chunk("result = 1\n")
            self.assertTrue(runtime._stream_session.ended)
        finally:
            if previous is None:
                sys.modules.pop("_agent_runtime_host", None)
            else:
                sys.modules["_agent_runtime_host"] = previous

    def test_print_is_bounded_and_auditable(self):
        runtime = load_runtime()
        response = execute(runtime, "print('🙂' * 70000)\nreturn 1")
        self.assertEqual(response["status"], "error")
        self.assertEqual(response["error"]["code"], "output_limit_exceeded")
        self.assertEqual(response["logs"][-1], runtime._STDOUT_TRUNCATION_MARKER)
        self.assertLessEqual(sum(len(line.encode("utf-8")) for line in response["logs"]), runtime._STDOUT_MAX_BYTES)

        line_response = execute(load_runtime(), "print('\\n'.join(str(i) for i in range(300)))\nreturn 1")
        self.assertEqual(line_response["status"], "ok")
        self.assertEqual(len(line_response["logs"]), 256)
        self.assertEqual(line_response["logs"][-1], runtime._STDOUT_TRUNCATION_MARKER)


if __name__ == "__main__":
    unittest.main()
