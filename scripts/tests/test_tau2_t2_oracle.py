import importlib.util
import json
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-t2-oracle.py"
SPEC = importlib.util.spec_from_file_location("tau2_t2_oracle", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
oracle = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(oracle)


class Tau2T2OracleTests(unittest.TestCase):
    def test_terminal_event_contents_binds_answers_and_whitespace_invalid(self):
        events = [
            {"kind": "program", "model_response": "program"},
            {"kind": "answer", "model_response": json.dumps({"kind": "answer", "content": "done"})},
            {"kind": "invalid_model_action", "model_response": "   "},
        ]
        self.assertEqual(
            oracle.terminal_event_contents(events),
            ["done", "[empty or whitespace-only invalid model action]"],
        )

    def test_malformed_terminal_answer_is_rejected(self):
        with self.assertRaises(ValueError):
            oracle.terminal_event_contents([
                {"kind": "answer", "model_response": json.dumps({"kind": "answer", "content": ""})}
            ])


if __name__ == "__main__":
    unittest.main()
