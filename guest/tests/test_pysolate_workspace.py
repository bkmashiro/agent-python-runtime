import hashlib
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from guest.bootstrap.pysolate import workspace


class WorkspaceToolsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.root.joinpath("src").mkdir()
        self.root.joinpath("src/app.py").write_text("# TODO: improve\nprint('hello')\n", encoding="utf-8")
        self.root.joinpath("src/test_app.py").write_text("def test_ok():\n    assert True\n", encoding="utf-8")
        self.root.joinpath("README.md").write_text("demo\n", encoding="utf-8")
        self.patch = mock.patch.object(workspace, "_ROOT", self.root)
        self.patch.start()

    def tearDown(self):
        self.patch.stop()
        self.temp.cleanup()

    def test_search_glob_stat_and_digest_are_structured_and_deterministic(self):
        self.assertEqual(workspace.glob("**/*.py"), ["src/app.py", "src/test_app.py"])
        matches = workspace.search("TODO", glob="*.py", path="src", max_matches=10)
        self.assertEqual(matches, [{
            "path": "src/app.py", "line": 1, "column": 3,
            "match": "TODO", "text": "# TODO: improve",
        }])
        metadata = workspace.stat("README.md")
        self.assertEqual(metadata, {"path": "README.md", "kind": "file", "size": 5, "executable": False})
        self.assertEqual(workspace.digest("README.md"), "sha256:" + hashlib.sha256(b"demo\n").hexdigest())

    def test_copy_move_mkdir_remove_and_diff_stay_under_workspace(self):
        self.assertEqual(workspace.mkdir("reports"), {"path": "reports", "created": True})
        self.assertEqual(workspace.copy("README.md", "reports/copy.md")["bytes"], 5)
        self.assertEqual(workspace.move("reports/copy.md", "reports/final.md"), {"from": "reports/copy.md", "to": "reports/final.md"})
        workspace.write_text("reports/final.md", "changed\n")
        patch = workspace.diff("README.md", "reports/final.md")
        self.assertIn("--- README.md", patch)
        self.assertEqual(workspace.remove("reports/final.md"), {"path": "reports/final.md", "removed": True})
        with self.assertRaises(ValueError):
            workspace.read_text("../secret")
        with self.assertRaises(ValueError):
            workspace.glob("../*")

    def test_search_and_read_are_bounded(self):
        self.root.joinpath("large.txt").write_text("x" * (workspace.MAX_TEXT_BYTES + 1), encoding="utf-8")
        with self.assertRaises(ValueError):
            workspace.read_text("large.txt")

        outside = Path(self.temp.name) / "outside"
        outside.mkdir()
        (outside / "secret.txt").write_text("secret", encoding="utf-8")
        try:
            (self.root / "link").symlink_to(outside, target_is_directory=True)
        except (OSError, NotImplementedError):
            return
        with self.assertRaises(ValueError):
            workspace.read_text("link/secret.txt")
        with self.assertRaises(ValueError):
            workspace.search("x", max_matches=workspace.MAX_MATCHES + 1)


if __name__ == "__main__":
    unittest.main()
