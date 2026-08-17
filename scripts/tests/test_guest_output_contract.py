import importlib.util
import json
import pathlib
import unittest


BOOTSTRAP = pathlib.Path(__file__).resolve().parents[2] / "guest" / "bootstrap" / "agent_runtime" / "__init__.py"


def load_runtime():
    spec = importlib.util.spec_from_file_location("agent_runtime_output_contract_test", BOOTSTRAP)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
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
        self.assertEqual(set(response["source_contract"]), {"schema_version", "model_source_sha256", "effective_ast_sha256", "wrapper_contract_sha256"})

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
        self.assertEqual(response["status"], "ok")
        self.assertEqual(response["result"], {"counter": 2, "freevars": []})

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
