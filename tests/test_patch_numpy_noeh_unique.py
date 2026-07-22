import unittest

from tools.patch_numpy_noeh_unique import REPLACEMENTS, patch_text


class PatchNumPyNoEHUniqueTests(unittest.TestCase):
    def setUp(self):
        self.source = "\n/* seam */\n".join(old for _, old, _ in REPLACEMENTS)

    def test_rewrites_exact_exception_seams_and_preserves_python_error(self):
        patched = patch_text(self.source)

        for _, _, new in REPLACEMENTS:
            self.assertIn(new, patched)
        self.assertIn("PyErr_SetString(PyExc_RuntimeError", patched)
        self.assertIn("if (load_failed)", patched)
        self.assertNotIn("throw std::", patched)
        self.assertNotIn("catch (const std::", patched)
        self.assertIn("not production-qualified", patched)

    def test_fails_closed_on_missing_upstream_seam(self):
        with self.assertRaisesRegex(ValueError, "expected exactly one"):
            patch_text(self.source.replace(REPLACEMENTS[0][1], ""))

    def test_fails_closed_on_duplicate_upstream_seam(self):
        with self.assertRaisesRegex(ValueError, "expected exactly one"):
            patch_text(self.source + REPLACEMENTS[0][1])

    def test_rejects_already_patched_input(self):
        with self.assertRaisesRegex(ValueError, "expected exactly one"):
            patch_text(patch_text(self.source))


if __name__ == "__main__":
    unittest.main()
