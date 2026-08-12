import hashlib
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from guest.bootstrap.pysolate import fs


class FilesystemToolsTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.workspace = Path(self.temp.name, "workspace")
        self.scratch = Path(self.temp.name, "tmp")
        self.workspace.mkdir()
        self.scratch.mkdir()
        self.workspace.joinpath("src").mkdir()
        self.workspace.joinpath("src/app.py").write_text("# TODO: improve\nprint('hello')\n", encoding="utf-8")
        self.workspace.joinpath("src/test_app.py").write_text("def test_ok():\n    assert True\n", encoding="utf-8")
        self.workspace.joinpath("README.md").write_text("demo\n", encoding="utf-8")
        self.patch = mock.patch.object(fs, "_MOUNTS", {"/workspace": self.workspace, "/tmp": self.scratch})
        self.patch.start()

    def tearDown(self):
        self.patch.stop()
        self.temp.cleanup()

    def test_search_glob_stat_and_digest_are_structured_and_deterministic(self):
        self.assertEqual(fs.glob("**/*.py", path="/workspace"), ["/workspace/src/app.py", "/workspace/src/test_app.py"])
        matches = fs.search("TODO", glob="*.py", path="/workspace/src", max_matches=10)
        self.assertEqual(matches, [{
            "path": "/workspace/src/app.py", "line": 1, "column": 3,
            "match": "TODO", "text": "# TODO: improve",
        }])
        metadata = fs.stat("/workspace/README.md")
        self.assertEqual(metadata, {"path": "/workspace/README.md", "kind": "file", "size": 5, "executable": False})
        self.assertEqual(fs.digest("/workspace/README.md"), "sha256:" + hashlib.sha256(b"demo\n").hexdigest())

    def test_operations_are_generic_across_workspace_and_tmp(self):
        self.assertEqual(fs.mkdir("/tmp/reports"), {"path": "/tmp/reports", "created": True})
        self.assertEqual(fs.copy("/workspace/README.md", "/tmp/reports/copy.md")["bytes"], 5)
        self.assertEqual(fs.read_text("/tmp/reports/copy.md"), "demo\n")
        fs.write_text("/tmp/reports/actual.md", "changed\n")
        patch = fs.diff("/workspace/README.md", "/tmp/reports/actual.md")
        self.assertIn("--- /workspace/README.md", patch)
        self.assertEqual(fs.move("/tmp/reports/copy.md", "/tmp/reports/final.md"), {"from": "/tmp/reports/copy.md", "to": "/tmp/reports/final.md"})
        self.assertEqual(fs.remove("/tmp/reports/final.md"), {"path": "/tmp/reports/final.md", "removed": True})

    def test_paths_must_name_one_visible_mount(self):
        for invalid in ["README.md", "/etc/passwd", "/workspace/../tmp/secret", "/workspace/.git/config", "/tmp/./file"]:
            with self.subTest(path=invalid), self.assertRaises(ValueError):
                fs.read_text(invalid)

    def test_search_and_read_are_bounded_and_reject_symlinks(self):
        self.workspace.joinpath("large.txt").write_text("x" * (fs.MAX_TEXT_BYTES + 1), encoding="utf-8")
        with self.assertRaises(ValueError):
            fs.read_text("/workspace/large.txt")

        outside = Path(self.temp.name, "outside")
        outside.mkdir()
        outside.joinpath("secret.txt").write_text("secret", encoding="utf-8")
        try:
            self.workspace.joinpath("link").symlink_to(outside, target_is_directory=True)
        except (OSError, NotImplementedError):
            return
        with self.assertRaises(ValueError):
            fs.read_text("/workspace/link/secret.txt")
        with self.assertRaises(ValueError):
            fs.search("x", path="/workspace", max_matches=fs.MAX_MATCHES + 1)


if __name__ == "__main__":
    unittest.main()
