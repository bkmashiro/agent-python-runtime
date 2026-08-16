import importlib.util
import json
import pathlib
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).parents[1] / "natural-placement-control.py"
SPEC = importlib.util.spec_from_file_location("natural_placement_control", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
control = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(control)


class NaturalPlacementControlTests(unittest.TestCase):
    def test_rejects_public_source_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            root.chmod(0o755)
            with self.assertRaisesRegex(ValueError, "0700"):
                control.build(root)

    def test_rejects_source_descriptor_drift(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            root.chmod(0o700)
            (root / "download-manifest.json").write_text(json.dumps({"schema_version": control.SOURCE_SCHEMA, "sources": [{"dataset": control.DATASET, "revision": "wrong"}]}))
            with self.assertRaisesRegex(ValueError, "source descriptor mismatch"):
                control.build(root)


if __name__ == "__main__":
    unittest.main()
