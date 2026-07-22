import importlib.util
import os
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "copy_tree_deterministic", ROOT / "tools" / "copy_tree_deterministic.py",
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class DeterministicCopyTests(unittest.TestCase):
    def test_normalizes_order_modes_timestamps_and_excludes_bytecode(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / "source"
            source.mkdir()
            (source / "z.py").write_text("z\n")
            (source / "a").mkdir()
            (source / "a" / "b.txt").write_text("b\n")
            (source / "__pycache__").mkdir()
            (source / "__pycache__" / "z.pyc").write_bytes(b"nondeterministic")
            os.chmod(source / "z.py", 0o775)
            destination = root / "destination"
            copied = MODULE.copy_source(source, destination, epoch=1234567890)
            self.assertEqual(["a", "a/b.txt", "z.py"], copied)
            self.assertFalse((destination / "__pycache__").exists())
            self.assertEqual(0o755, (destination / "z.py").stat().st_mode & 0o777)
            self.assertEqual(0o644, (destination / "a" / "b.txt").stat().st_mode & 0o777)
            for path in destination.rglob("*"):
                self.assertEqual(1234567890, int(path.stat().st_mtime))

    def test_overlay_file_and_rejects_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            source = root / "bootstrap.py"
            source.write_text("result = 1\n")
            destination = root / "lib" / "bootstrap.py"
            self.assertEqual(["bootstrap.py"], MODULE.copy_source(source, destination, epoch=10))
            self.assertEqual(source.read_bytes(), destination.read_bytes())
            link = root / "link"
            link.symlink_to(source)
            with self.assertRaisesRegex(ValueError, "symlink"):
                MODULE.copy_source(link, root / "other", epoch=10)


if __name__ == "__main__":
    unittest.main()
