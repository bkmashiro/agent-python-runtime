import importlib.util
import pathlib
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-pure-oracle.py"
SPEC = importlib.util.spec_from_file_location("tau2_pure_oracle", MODULE_PATH)
assert SPEC and SPEC.loader
oracle = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(oracle)


class Tau2PureOracleTests(unittest.TestCase):
    def request(self):
        return {
            "schema_version": oracle.REQUEST_SCHEMA,
            "source_revision": oracle.EXPECTED_REVISION,
            "domain": "retail",
            "task_id": "24",
            "assistant_text": "private answer",
        }

    def test_accepts_exact_request(self):
        self.assertEqual(oracle.validate_request(self.request())["task_id"], "24")

    def test_rejects_empty_text_and_unknown_fields(self):
        candidate = self.request()
        candidate["assistant_text"] = ""
        with self.assertRaises(ValueError):
            oracle.validate_request(candidate)
        candidate = self.request()
        candidate["extra"] = True
        with self.assertRaises(ValueError):
            oracle.validate_request(candidate)


if __name__ == "__main__":
    unittest.main()
