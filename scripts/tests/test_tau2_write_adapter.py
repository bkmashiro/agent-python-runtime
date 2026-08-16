import importlib.util
import pathlib
import tempfile
import unittest
from unittest import mock

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-write-adapter.py"
SPEC = importlib.util.spec_from_file_location("tau2_write_adapter", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
adapter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(adapter)


class Tau2WriteAdapterTests(unittest.TestCase):
    def init_request(self):
        return {
            "schema_version": adapter.REQUEST_SCHEMA,
            "source_revision": adapter.EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "11",
            "operation": "init",
        }

    def apply_request(self):
        return {
            "schema_version": adapter.REQUEST_SCHEMA,
            "source_revision": adapter.EXPECTED_REVISION,
            "domain": "airline",
            "task_id": "11",
            "operation": "apply",
            "call_id": "broker:update_reservation_flights",
            "tool": "update_reservation_flights",
            "arguments": dict(adapter.EXPECTED_ARGUMENTS),
            "state": {"flights": {}, "reservations": {}, "users": {}},
            "inject_failure": False,
        }

    def test_accepts_only_exact_init_and_apply_envelopes(self):
        self.assertEqual(adapter.validate_request(self.init_request())["operation"], "init")
        self.assertEqual(adapter.validate_request(self.apply_request())["operation"], "apply")
        for mutation in (
            lambda value: value.update(extra=True),
            lambda value: value.update(task_id="3"),
            lambda value: value.update(source_revision="bad"),
        ):
            candidate = self.init_request()
            mutation(candidate)
            with self.assertRaises(ValueError):
                adapter.validate_request(candidate)

    def test_apply_is_exactly_scoped_and_failure_flag_is_host_only_boolean(self):
        candidate = self.apply_request()
        candidate["arguments"] = dict(adapter.EXPECTED_ARGUMENTS, reservation_id="different")
        with self.assertRaises(ValueError):
            adapter.validate_request(candidate)
        candidate = self.apply_request()
        candidate["tool"] = "get_reservation_details"
        with self.assertRaises(ValueError):
            adapter.validate_request(candidate)
        candidate = self.apply_request()
        candidate["inject_failure"] = 1
        with self.assertRaises(ValueError):
            adapter.validate_request(candidate)
        candidate = self.apply_request()
        candidate["state"] = []
        with self.assertRaises(ValueError):
            adapter.validate_request(candidate)

    def test_state_encoding_and_identity_are_canonical(self):
        first = {"b": 2, "a": {"y": 1, "x": 0}}
        second = {"a": {"x": 0, "y": 1}, "b": 2}
        self.assertEqual(adapter.canonical_state(first), adapter.canonical_state(second))
        self.assertEqual(adapter.state_identity(first), adapter.state_identity(second))
        self.assertTrue(adapter.state_identity(first).startswith("sha256:"))

    def test_checkout_verification_rejects_dirty_tree(self):
        completed = adapter.subprocess.CompletedProcess
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.object(adapter.subprocess, "run", side_effect=[
                completed([], 0, stdout=adapter.EXPECTED_REVISION + "\n"),
                completed([], 0, stdout=" M src/tau2/example.py\n"),
                completed([], 0, stdout=""),
            ]):
                with self.assertRaisesRegex(ValueError, "not clean"):
                    adapter._verify_checkout(pathlib.Path(directory))


if __name__ == "__main__":
    unittest.main()
