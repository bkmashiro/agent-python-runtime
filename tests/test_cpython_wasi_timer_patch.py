import importlib.util
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[1]
MODULE_PATH = ROOT / "tools" / "patch_cpython_wasi_timer_config.py"
SPEC = importlib.util.spec_from_file_location("patch_cpython_wasi_timer_config", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class CPythonWASITimerConfigPatchTests(unittest.TestCase):
    def test_disables_absolute_clock_nanosleep(self) -> None:
        source = "# CPython WASI config\nac_cv_func_eventfd=no\n"
        patched = MODULE.patch_config_site(source)
        self.assertIn(MODULE.MARKER, patched)
        for setting in MODULE.SETTINGS:
            self.assertEqual(patched.count(setting), 1)
        self.assertTrue(patched.endswith(f"{MODULE.SETTINGS[-1]}\n"))

    def test_rejects_duplicate_or_upstream_setting(self) -> None:
        with self.assertRaisesRegex(ValueError, "already present"):
            MODULE.patch_config_site(f"{MODULE.SETTINGS[0]}\n")

        with self.assertRaisesRegex(ValueError, "already present"):
            MODULE.patch_config_site(f"{MODULE.SETTINGS[1]}\n")


if __name__ == "__main__":
    unittest.main()
