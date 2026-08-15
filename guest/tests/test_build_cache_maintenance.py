import importlib.util
import os
import pathlib
import tempfile
import unittest


MODULE_PATH = pathlib.Path(__file__).parents[1] / "build" / "cache_maintenance.py"
SPEC = importlib.util.spec_from_file_location("cache_maintenance", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
maintenance = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(maintenance)


class CacheMaintenanceTests(unittest.TestCase):
    def test_keeps_protected_and_newest_second_key_only(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            old, newest, protected = (root / (character * 64) for character in "abc")
            for index, entry in enumerate((old, newest, protected), start=1):
                entry.mkdir()
                os.utime(entry, ns=(index, index))
            unrelated = root / "do-not-touch"
            unrelated.mkdir()
            link = root / ("d" * 64)
            link.symlink_to(unrelated, target_is_directory=True)
            removed = maintenance.prune(root, protected.name, keep=2)
            self.assertEqual([old.name], removed)
            self.assertTrue(protected.is_dir())
            self.assertTrue(newest.is_dir())
            self.assertTrue(unrelated.is_dir())
            self.assertTrue(link.is_symlink())

    def test_removes_only_bounded_crash_residue(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            protected = root / ("a" * 64)
            protected.mkdir()
            publish = root / ".publish.Ab12Cd34"
            final = root / (".tmp." + "c" * 64 + ".Ef56Gh78")
            publish.mkdir()
            final.mkdir()
            unrelated = root / ".tmp.not-a-cache-key.Ab12Cd34"
            unrelated.mkdir()
            linked = root / (".publish." + "d" * 64 + ".Ij90Kl12")
            linked.symlink_to(unrelated, target_is_directory=True)
            removed = maintenance.prune(root, protected.name, keep=2)
            self.assertEqual(sorted([publish.name, final.name]), removed)
            self.assertTrue(unrelated.is_dir())
            self.assertTrue(linked.is_symlink())

    def test_rejects_missing_protected_key(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(ValueError):
                maintenance.prune(pathlib.Path(directory), "a" * 64)


if __name__ == "__main__":
    unittest.main()
