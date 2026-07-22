import json
import pathlib
import tempfile
import unittest

from tools.stage_numpy_wasi_package import stage_package


class StageNumPyWASIPackageTests(unittest.TestCase):
    def test_stages_one_runtime_tree_without_native_or_test_artifacts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / "install" / "usr" / "local" / "lib" / "python3.14" / "site-packages" / "numpy"
            (source / "_core" / "tests").mkdir(parents=True)
            (source / "__init__.py").write_text("version = '2.5.1'\n")
            (source / "version.py").write_text("__version__ = '2.5.1'\n")
            (source / "_core" / "__init__.py").write_text("")
            (source / "_core" / "numeric.py").write_text("VALUE = 1\n")
            (source / "_core" / "_multiarray_umath.cpython-314-wasm32-wasi.so").write_bytes(b"archive")
            (source / "_core" / "libnpymath.a").write_bytes(b"archive")
            (source / "_core" / "tests" / "test_core.py").write_text("raise AssertionError\n")
            (source / "py.typed").write_text("")

            destination = root / "vfs" / "site-packages" / "numpy"
            manifest = stage_package(root / "install", destination, 1_700_000_000)

            self.assertEqual("usr/local/lib/python3.14/site-packages/numpy", manifest["source"])
            self.assertEqual(5, manifest["file_count"])
            self.assertTrue((destination / "_core" / "numeric.py").is_file())
            self.assertTrue((destination / "py.typed").is_file())
            self.assertFalse((destination / "_core" / "tests").exists())
            self.assertFalse((destination / "_core" / "_multiarray_umath.cpython-314-wasm32-wasi.so").exists())
            self.assertFalse((destination / "_core" / "libnpymath.a").exists())
            self.assertEqual(
                sorted(item["path"] for item in manifest["files"]),
                [item["path"] for item in manifest["files"]],
            )
            self.assertTrue(all(len(item["sha256"]) == 64 for item in manifest["files"]))
            self.assertEqual(1_700_000_000, int((destination / "__init__.py").stat().st_mtime))

    def test_rejects_multiple_installed_numpy_roots(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            for prefix in ("a", "b"):
                package = root / prefix / "site-packages" / "numpy"
                (package / "_core").mkdir(parents=True)
                (package / "__init__.py").write_text("")
                (package / "version.py").write_text("")
                (package / "_core" / "__init__.py").write_text("")
            with self.assertRaisesRegex(ValueError, "exactly one installed NumPy package"):
                stage_package(root, root / "out", 1_700_000_000)

    def test_rejects_symlink_in_runtime_tree(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            package = root / "site-packages" / "numpy"
            (package / "_core").mkdir(parents=True)
            (package / "__init__.py").write_text("")
            (package / "version.py").write_text("")
            (package / "_core" / "__init__.py").write_text("")
            (package / "alias.py").symlink_to(package / "__init__.py")
            with self.assertRaisesRegex(ValueError, "symlink"):
                stage_package(root, root / "out", 1_700_000_000)


if __name__ == "__main__":
    unittest.main()
