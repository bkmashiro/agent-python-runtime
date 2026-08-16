import importlib.util
import pathlib
import unittest
from types import SimpleNamespace

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-pure-canary.py"
SPEC = importlib.util.spec_from_file_location("tau2_pure_canary", MODULE_PATH)
assert SPEC and SPEC.loader
canary = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(canary)


class Tau2PureCanaryTests(unittest.TestCase):
    def test_accepts_one_literal_result_assignment(self):
        action = canary.parse_pure_program_action('{"kind":"program","source":"result = \'hello\'"}')
        self.assertEqual(action["literal_content"], "hello")

    def test_rejects_calls_imports_and_answer_bypass(self):
        invalid = [
            '{"kind":"program","source":"result = tools.read()"}',
            '{"kind":"program","source":"import json\\nresult = \'hello\'"}',
            '{"kind":"answer","content":"hello"}',
        ]
        for value in invalid:
            with self.subTest(value=value), self.assertRaises((ValueError, SyntaxError)):
                canary.parse_pure_program_action(value)

    def test_rejects_extra_fields_and_empty_result(self):
        for value in [
            '{"kind":"program","source":"result = \'hello\'","extra":true}',
            '{"kind":"program","source":"result = \'\'"}',
        ]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                canary.parse_pure_program_action(value)

    def test_counts_nested_tool_calls(self):
        messages = [
            SimpleNamespace(tool_calls=[]),
            SimpleNamespace(tool_calls=[object()]),
            SimpleNamespace(tool_calls=[object(), object()]),
            SimpleNamespace(),
        ]
        self.assertEqual(canary.count_embedded_tool_calls(messages), 3)


if __name__ == "__main__":
    unittest.main()
