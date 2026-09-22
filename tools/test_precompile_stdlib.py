import contextlib
import io
import os
from pathlib import Path
import runpy
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('precompile-stdlib.py')


class PrecompileTests(unittest.TestCase):
    def test_compiles_selected_packages_without_importing_them(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            paths = ['csv.py', 'inspect.py', 'site-packages/numpy/__init__.py',
                     'site-packages/numpy/core/numeric.py',
                     'site-packages/numpy/tests/test_skip.py',
                     'site-packages/unrelated/__init__.py']
            for name in paths:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('raise RuntimeError("must never import at build time")\n')
            # Selection/cache-policy test on the local interpreter. Actual target
            # bytecode compatibility is checked by the same-build Guest campaign.
            with patch.dict(os.environ, {'PYSOLATE_PRECOMPILE_SCIENTIFIC': '1'}), patch.object(sys, 'argv', [str(SCRIPT), directory]), patch.object(sys, 'version_info', (3, 14)), contextlib.redirect_stdout(io.StringIO()):
                runpy.run_path(str(SCRIPT), run_name='__main__')
            caches = list(root.rglob('*.pyc'))
            self.assertEqual(len(caches), 4)
            self.assertFalse(any('tests' in p.parts or 'unrelated' in p.parts for p in caches))
            for cache in caches:
                self.assertEqual(int.from_bytes(cache.read_bytes()[4:8], 'little'), 3)

    def test_default_keeps_original_selection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'json.py').write_text('value = 1\n')
            (root / 'csv.py').write_text('value = 1\n')
            with patch.dict(os.environ, {'PYSOLATE_PRECOMPILE_SCIENTIFIC': '0'}), patch.object(sys, 'argv', [str(SCRIPT), directory]), patch.object(sys, 'version_info', (3, 14)), contextlib.redirect_stdout(io.StringIO()):
                runpy.run_path(str(SCRIPT), run_name='__main__')
            caches = list(root.rglob('*.pyc'))
            self.assertEqual(len(caches), 1)
            self.assertTrue(caches[0].name.startswith('json.'))

    def test_rejects_mismatched_build_interpreter(self):
        with patch.object(sys, 'version_info', (3, 13)):
            with self.assertRaisesRegex(SystemExit, 'same 3.14 build'):
                runpy.run_path(str(SCRIPT), run_name='__main__')


if __name__ == '__main__':
    unittest.main()
