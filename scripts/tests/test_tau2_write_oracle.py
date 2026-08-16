import importlib.util
import pathlib
import unittest
from unittest import mock

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-write-oracle.py"
SPEC = importlib.util.spec_from_file_location("tau2_write_oracle", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
oracle = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(oracle)


class Tau2WriteOracleTests(unittest.TestCase):
    def request(self):
        return {
            "schema_version": oracle.REQUEST_SCHEMA,
            "source_revision": oracle.EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "11",
            "call": {
                "call_id": "oracle-write",
                "tool": "update_reservation_flights",
                "arguments": dict(oracle.EXPECTED_ARGUMENTS),
                "content": "private tool body",
            },
            "assistant_text": "The refund is $5244.",
            "observed_final_state": {"private": "state"},
        }

    def test_validates_exact_write_and_communication(self):
        self.assertEqual(oracle.validate_request(self.request())["task_id"], "11")
        candidate = self.request()
        candidate["call"]["arguments"] = dict(oracle.EXPECTED_ARGUMENTS, cabin="economy")
        with self.assertRaises(ValueError):
            oracle.validate_request(candidate)
        candidate = self.request()
        candidate["assistant_text"] = "Done."
        with self.assertRaises(ValueError):
            oracle.validate_request(candidate)

    def test_public_report_is_body_safe_and_multiplicative(self):
        report = oracle.public_report(1.0, 0.0, True, False, True)
        self.assertEqual(report["overall_reward"], 0.0)
        self.assertFalse(report["tool_bodies_included"])
        self.assertFalse(report["assistant_text_included"])
        self.assertNotIn("5244", str(report))
        self.assertNotIn("private tool body", str(report))

    def test_checkout_verification_rejects_untracked_source(self):
        completed = oracle.subprocess.CompletedProcess
        with mock.patch.object(oracle.subprocess, "run", side_effect=[
            completed([], 0, stdout=oracle.EXPECTED_REVISION + "\n"),
            completed([], 0, stdout=""),
            completed([], 0, stdout="src/tau2/injected.py\n"),
        ]):
            with self.assertRaisesRegex(ValueError, "not clean"):
                oracle._verify_checkout(pathlib.Path("/private/source"))


if __name__ == "__main__":
    unittest.main()
