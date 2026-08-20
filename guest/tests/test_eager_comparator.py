import pathlib
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
BOOTSTRAP = ROOT / "guest" / "bootstrap"
if str(BOOTSTRAP) not in sys.path:
    sys.path.insert(0, str(BOOTSTRAP))

from agent_runtime.eager_comparator import EagerStyleGateSession


class EagerStyleGateSessionTests(unittest.TestCase):
    def test_waits_for_one_token_and_reuses_one_namespace(self):
        session = EagerStyleGateSession({"value": 2}, {})

        first = session.chunk("base = inputs['value'] + 1\n")
        self.assertEqual("lookahead_pending", first["status"])
        self.assertNotIn("base", session.namespace)

        second = session.chunk("derived = base * 4\n")
        self.assertEqual("executed", second["status"])
        self.assertEqual(3, session.namespace["base"])
        self.assertNotIn("derived", session.namespace)

        session.chunk("result = derived\n")
        final = session.end()
        self.assertEqual(12, final["result"])
        self.assertEqual(2, final["prefix_python_executions"])
        self.assertEqual(3, final["python_executions"])
        self.assertFalse(final["sealed"])

    def test_defers_low_yield_definition_until_executable_chunk(self):
        session = EagerStyleGateSession({}, {})
        session.chunk("def value():\n    return 7\n")
        deferred = session.chunk("result = value()\n")

        self.assertEqual("deferred_low_yield", deferred["status"])
        self.assertNotIn("value", session.namespace)
        final = session.end()
        self.assertEqual(7, final["result"])
        self.assertEqual(0, final["prefix_python_executions"])
        self.assertEqual(1, final["python_executions"])

    def test_denied_name_seals_candidate_and_remaining_suffix(self):
        calls = []
        session = EagerStyleGateSession({}, {
            "time": lambda: calls.append("denied"),
            "mark": lambda value: calls.append(value),
        })
        session.chunk("time()\n")
        sealed = session.chunk("mark('after')\n")

        self.assertEqual("sealed", sealed["status"])
        self.assertEqual([], calls)
        self.assertEqual("time", sealed["denied_name"])

        final = session.end()
        self.assertEqual(["denied", "after"], calls)
        self.assertTrue(final["sealed"])
        self.assertEqual(0, final["prefix_python_executions"])
        self.assertEqual(1, final["python_executions"])

    def test_invalid_final_suffix_preserves_executed_prefix_without_running_suffix(self):
        calls = []
        session = EagerStyleGateSession({}, {"mark": calls.append})
        session.chunk("mark('prefix')\n")
        event = session.chunk("result = )\n")

        self.assertEqual("executed", event["status"])
        self.assertEqual(["prefix"], calls)
        with self.assertRaises(SyntaxError):
            session.end()
        self.assertEqual(["prefix"], calls)
        self.assertEqual(1, session.prefix_python_executions)

    def test_else_token_keeps_compound_statement_together(self):
        session = EagerStyleGateSession({"flag": False}, {})
        session.chunk("if inputs['flag']:\n    result = 'if'\n")
        held = session.chunk("else:\n    result = 'else'\n")

        self.assertEqual("lookahead_pending", held["status"])
        self.assertNotIn("result", session.namespace)
        self.assertEqual("else", session.end()["result"])

    def test_cancel_does_not_execute_pending_source(self):
        calls = []
        session = EagerStyleGateSession({}, {"mark": calls.append})
        session.chunk("mark('pending')\n")

        cancelled = session.cancel()
        self.assertEqual("cancelled", cancelled["status"])
        self.assertEqual([], calls)
        with self.assertRaises(ValueError):
            session.end()


    def test_future_flags_survive_prefix_execution_and_deferred_batch(self):
        session = EagerStyleGateSession({}, {})
        session.chunk("from __future__ import annotations\n")
        session.chunk("def identity(value: MissingType) -> MissingType:\n    return value\n")
        session.chunk("result = identity(5)\n")
        self.assertEqual(5, session.end()["result"])

    def test_unknown_wrapper_is_not_promoted_into_static_denylist(self):
        calls = []

        def wrapper(value):
            calls.append(value)
            return value

        session = EagerStyleGateSession({}, {"wrapper": wrapper})
        session.chunk("value = wrapper('physical-read-shape')\n")
        event = session.chunk("result = value\n")
        self.assertEqual("executed", event["status"])
        self.assertEqual(["physical-read-shape"], calls)
        self.assertFalse(event["sealed"])
        self.assertEqual("physical-read-shape", session.end()["result"])

    def test_unreachable_denied_name_still_seals_syntax_chunk(self):
        session = EagerStyleGateSession({}, {"time": lambda: None})
        session.chunk("if False:\n    time()\n")
        event = session.chunk("result = 3\n")
        self.assertEqual("sealed", event["status"])
        self.assertEqual("time", event["denied_name"])
        self.assertEqual(3, session.end()["result"])

    def test_static_name_gate_covers_import_attribute_and_dynamic_name(self):
        cases = (
            ("import os\n", "os"),
            ("value = subprocess.run\n", "subprocess"),
            ("value = eval\n", "eval"),
        )
        for source, expected in cases:
            with self.subTest(source=source):
                session = EagerStyleGateSession({}, {})
                session.chunk(source)
                event = session.chunk("result = 1\n")
                self.assertEqual("sealed", event["status"])
                self.assertEqual(expected, event["denied_name"])


    def test_finish_returns_body_free_terminal_projection(self):
        calls = []
        invalid = EagerStyleGateSession({}, {"mark": calls.append})
        invalid.chunk("mark('prefix')\n")
        invalid.chunk("result = )\n")
        terminal = invalid.finish()
        self.assertEqual("syntax_error", terminal["outcome"])
        self.assertEqual("SyntaxError", terminal["error_class"])
        self.assertNotIn("error_message", terminal)
        self.assertEqual(["prefix"], calls)

        failed = EagerStyleGateSession({}, {"mark": calls.append})
        failed.chunk("raise ValueError('private-body')\n")
        event = failed.chunk("mark('must-not-run')\n")
        self.assertEqual("runtime_error_frozen", event["status"])
        failed.chunk("result = 9\n")
        terminal = failed.finish()
        self.assertEqual("runtime_error", terminal["outcome"])
        self.assertEqual("ValueError", terminal["error_class"])
        self.assertEqual(1, terminal["prefix_python_executions"])
        self.assertEqual(["prefix"], calls)

    def test_private_prepared_globals_and_unadmitted_imports_are_not_authority(self):
        session = EagerStyleGateSession({}, {"_agent_runtime_host": object(), "public_value": 4})
        session.chunk("result = ('_agent_runtime_host' in globals(), public_value)\n")
        self.assertEqual((False, 4), session.end()["result"])

        denied = EagerStyleGateSession({}, {})
        denied.chunk("import pathlib\n")
        with self.assertRaises(ImportError):
            denied.end()

        admitted = EagerStyleGateSession({}, {}, allowed_import_roots=("math",))
        admitted.chunk("import math\nresult = math.sqrt(9)\n")
        self.assertEqual(3.0, admitted.end()["result"])


if __name__ == "__main__":
    unittest.main()
