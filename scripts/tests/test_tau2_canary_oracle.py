import importlib.util
import unittest
import pathlib


MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-canary-oracle.py"
SPEC = importlib.util.spec_from_file_location("tau2_canary_oracle", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
oracle = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(oracle)


class Tau2CanaryOracleTests(unittest.TestCase):
    def payload(self):
        return {
            "schema_version": "pysolate.tau2-canary-oracle-request.v1",
            "source_revision": oracle.EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "3",
            "calls": [
                {
                    "call_id": "one",
                    "tool": "get_reservation_details",
                    "arguments": {"reservation_id": "JMO1MG"},
                    "content": '{"reservation_id":"JMO1MG"}',
                },
                {
                    "call_id": "two",
                    "tool": "get_user_details",
                    "arguments": {"user_id": "anya_garcia_5901"},
                    "content": '{"membership":"silver"}',
                },
            ],
            "assistant_text": "You may bring 4 suitcases.",
        }

    def test_accepts_exact_ordered_private_trajectory(self):
        self.assertEqual(oracle.validate_request(self.payload())["task_id"], "3")

    def test_rejects_missing_reordered_or_unknown_fields(self):
        missing = self.payload()
        missing["calls"] = missing["calls"][:1]
        reversed_calls = self.payload()
        reversed_calls["calls"] = list(reversed(reversed_calls["calls"]))
        unknown = self.payload()
        unknown["raw_task"] = "forbidden"
        for payload in [missing, reversed_calls, unknown]:
            with self.subTest(payload=payload):
                with self.assertRaises(ValueError):
                    oracle.validate_request(payload)

    def test_public_projection_contains_no_tool_or_task_bodies(self):
        report = oracle.public_report(
            env_reward=1.0,
            communicate_reward=1.0,
            db_match=True,
            communicate_met=True,
        )
        self.assertEqual(report["overall_reward"], 1.0)
        self.assertNotIn("calls", report)
        self.assertNotIn("assistant_text", report)


if __name__ == "__main__":
    unittest.main()
