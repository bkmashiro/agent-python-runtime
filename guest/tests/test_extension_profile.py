import importlib.util
import json
import pathlib
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
TOOL_PATH = ROOT / "guest" / "build" / "extension_profile.py"


def load_tool():
    spec = importlib.util.spec_from_file_location("extension_profile", TOOL_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load extension profile tool")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ExtensionProfileTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tool = load_tool()

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = pathlib.Path(self.directory.name)
        self.package = self.root / "attr"
        self.package.mkdir()
        (self.package / "__init__.py").write_text("from .core import value\n")
        (self.package / "core.py").write_text("value = 1\n")
        identity = self.tool.package_tree_identity(self.package)
        self.lock = {
            "schema_version": 1,
            "artifact_profile": "attrs-770",
            "artifact_filename": "agent-python-runtime-attrs-770.wasm",
            "source": {
                "id": "attrs-source",
                "version": "20.3.0-39-g58d2adc",
                "url": "https://example.test/attrs.tar.gz",
                "sha256": "a" * 64,
                "license": "MIT",
                "role": "python-package",
                "artifact_relation": "packaged",
                "source_commit": "58d2adce57f2c4e447eb12b892ebbb09cccbdcc3",
                "source_subdirectory": "src/attr",
                "patch_sha256": "b" * 64,
            },
            "package": {
                "name": "attrs",
                "status": "selected-pure-python",
                "import_root": "attr",
                "install_path": "site-packages/attr",
                **identity,
            },
            "qualification": [
                {"name": "attr", "operation": "generic_dynamic_class"},
                {"name": "types", "operation": "new_class"},
                {"name": "typing", "operation": "generic_alias"},
            ],
        }

    def test_build_selection_binds_source_patch_and_tree(self):
        selection = self.tool.build_selection(self.lock, self.package)
        self.assertEqual("attrs-770", selection["profile"])
        self.assertEqual("pure-python-package", selection["kind"])
        self.assertEqual(self.lock["package"]["tree_sha256"], selection["package"]["tree_sha256"])
        self.assertEqual("b" * 64, selection["package"]["patch_sha256"])
        self.assertEqual(2, selection["package"]["file_count"])

    def test_tree_tamper_and_symlink_fail_closed(self):
        (self.package / "core.py").write_text("value = 2\n")
        with self.assertRaisesRegex(ValueError, "tree identity"):
            self.tool.build_selection(self.lock, self.package)
        (self.package / "core.py").write_text("value = 1\n")
        (self.package / "escape.py").symlink_to(self.root / "outside.py")
        with self.assertRaisesRegex(ValueError, "regular files"):
            self.tool.package_tree_identity(self.package)

    def test_lock_and_effective_source_lock_are_strict(self):
        self.tool.validate_lock(self.lock)
        duplicate = json.dumps(self.lock).replace(
            '"schema_version": 1,', '"schema_version": 1, "schema_version": 1,', 1
        )
        with self.assertRaisesRegex(ValueError, "duplicate"):
            self.tool.strict_json_loads(duplicate)
        base = {"schema_version": 1, "target": "wasm32-wasip1", "sources": [{"id": "cpython-source"}]}
        effective = self.tool.merge_source_lock(base, self.lock)
        self.assertEqual(["cpython-source", "attrs-source"], [row["id"] for row in effective["sources"]])
        with self.assertRaisesRegex(ValueError, "already exists"):
            self.tool.merge_source_lock({**base, "sources": [{"id": "attrs-source"}]}, self.lock)


if __name__ == "__main__":
    unittest.main()
