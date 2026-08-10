import importlib.util
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "patch_cpython_import_gate.py"
SPEC = importlib.util.spec_from_file_location("patch_cpython_import_gate", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class PatchCPythonImportGateTests(unittest.TestCase):
    def test_inserts_gate_before_cache_lookup_and_fails_closed_on_drift(self):
        source = "before\n" + MODULE.OLD + "after\n"
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "import.c"
            path.write_text(source)
            MODULE.patch(path)
            patched = path.read_text()
            self.assertEqual(1, patched.count(MODULE.MARKER))
            self.assertLess(patched.index(MODULE.MARKER), patched.index("mod = import_get_module"))
            with self.assertRaisesRegex(ValueError, "already present"):
                MODULE.patch(path)
            path.write_text("upstream drift")
            with self.assertRaisesRegex(ValueError, "drifted"):
                MODULE.patch(path)


if __name__ == "__main__":
    unittest.main()
