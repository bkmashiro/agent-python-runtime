import importlib.util
import json
import pathlib
import stat
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "tau2-t2-run.py"
SPEC = importlib.util.spec_from_file_location("tau2_t2_run", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
runner = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runner)


class Tau2T2RunTests(unittest.TestCase):
    def test_chunking_preserves_frozen_order(self):
        public = {"tasks": [{"task_id": str(index)} for index in range(1, 17)]}
        cells = runner.planned_cells(public)
        self.assertEqual(cells[:4], [
            ("1", "direct", "direct"), ("1", "programmatic_python", "programmatic_python"),
            ("2", "direct", "direct"), ("2", "programmatic_python", "programmatic_python"),
        ])

    def fixture(self):
        protocol = {"model": runner.MODEL, "post_provider_reruns": 0, "max_total_provider_invocations_per_trial": 20, "seed": 42, "temperature": 0.0}
        public = {"schema_version": runner.PUBLIC_SCHEMA, "protocol": protocol, "tasks": [{"task_id": str(index)} for index in range(16)]}
        public["identity"] = runner.digest(runner.canonical(public))
        private = {"schema_version": runner.PRIVATE_SCHEMA, "public_identity": public["identity"], "protocol": protocol}
        preflight = {"schema_version": runner.PREFLIGHT_SCHEMA, "classification": "PREFLIGHT_SUPPORTED", "preregistration_identity": public["identity"], "provider_calls": 0}
        return public, private, preflight

    def test_protocol_and_cell_plan_are_exact(self):
        public, private, preflight = self.fixture()
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for name, value in (("public.json", public), ("private.json", private), ("preflight.json", preflight)):
                (root / name).write_text(json.dumps(value))
            loaded, _, _ = runner.load_protocol(root / "public.json", root / "private.json", root / "preflight.json")
            cells = runner.planned_cells(loaded)
            self.assertEqual(len(cells), 32)
            self.assertEqual(cells[0][1:], ("direct", "direct"))
            self.assertEqual(cells[1][1:], ("programmatic_python", "programmatic_python"))

    def test_process_failure_is_not_recorded_not_zero(self):
        _, private, _ = self.fixture()
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "cell.json"
            runner.write_not_recorded(path, "1", "direct", private["protocol"], b"private failure", 2)
            body = json.loads(path.read_text())
            self.assertEqual(body["status"], "not_recorded")
            self.assertIsNone(body["simulation"])
            self.assertNotIn("reward", body)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)


if __name__ == "__main__":
    unittest.main()
