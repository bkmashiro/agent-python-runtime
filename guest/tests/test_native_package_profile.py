import importlib.util
import json
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
TOOL_PATH = ROOT / "guest" / "build" / "native_package_profile.py"
LOCK_PATH = ROOT / "guest" / "build" / "profiles" / "numpy-core.lock.json"
SOURCE_LOCK = ROOT / "guest" / "build" / "sources.lock.json"


def load_tool():
    spec = importlib.util.spec_from_file_location("native_package_profile", TOOL_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load native package profile tool")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class NativePackageProfileTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tool = load_tool()

    def test_locked_numpy_profile_is_closed_and_static(self):
        lock = self.tool.load_lock(LOCK_PATH)
        self.assertEqual(lock["schema_version"], 1)
        self.assertEqual(lock["kind"], "static-native-package")
        self.assertEqual(lock["artifact_profile"], "numpy-core")
        self.assertEqual(lock["target"], "wasm32-wasip1")
        self.assertEqual(lock["package"]["name"], "numpy")
        self.assertEqual(lock["package"]["source_commit"], "7bc18034031f32e5d03bb646c472dabd1623e9d5")
        self.assertEqual(lock["package"]["source_archive_sha256"], "9a34aaef957033ff8a3a865e8f0172eb7de4cf4c2891195a56c13e915fb86014")
        self.assertEqual(lock["build"]["reference_commit"], "184cce0b537088be76e1e8a06d6fe742e2f29ff4")
        self.assertEqual(lock["build"]["wasi_sdk_version"], "33")
        self.assertEqual(lock["build"]["cpython_version"], "3.14.0")
        modules = lock["native_modules"]
        self.assertEqual(len(modules), 19)
        self.assertEqual(modules, sorted(modules, key=lambda row: row["name"]))
        self.assertEqual(len({row["archive"] for row in modules}), len(modules))
        support = lock["support_libraries"]
        self.assertEqual([row["name"] for row in support], ["npymath", "npyrandom"])
        self.assertEqual(len({row["archive"] for row in support}), 2)
        self.assertTrue(all(row["archive"].endswith(".a") for row in modules))
        self.assertTrue(all(row["init_symbol"].startswith("PyInit_") for row in modules))

    def test_source_lock_projection_is_fetcher_compatible_and_merged(self):
        lock = self.tool.load_lock(LOCK_PATH)
        projected = self.tool.source_lock(lock)
        self.assertEqual(projected["schema_version"], 1)
        self.assertEqual(projected["target"], "wasm32-wasip1")
        self.assertEqual([row["id"] for row in projected["sources"]], ["cython-source", "numpy-source", "setuptools-wheel"])
        base = json.loads(SOURCE_LOCK.read_text())
        merged = self.tool.merge_source_lock(base, lock)
        identifiers = [row["id"] for row in merged["sources"]]
        self.assertEqual(identifiers, sorted(identifiers))
        self.assertEqual(len(identifiers), len(set(identifiers)))
        self.assertIn("numpy-source", identifiers)

    def test_generated_registration_header_is_closed_over_locked_modules(self):
        lock = self.tool.load_lock(LOCK_PATH)
        header = self.tool.registration_header(lock)
        self.assertIn("register_selected_builtins", header)
        for module in lock["native_modules"]:
            self.assertIn(module["name"], header)
            self.assertIn(module["init_symbol"], header)
        self.assertNotIn("dlopen", header)

    def test_selection_binds_tree_archives_and_recipe(self):
        lock = self.tool.load_lock(LOCK_PATH)
        with tempfile.TemporaryDirectory() as raw:
            root = pathlib.Path(raw)
            package_root = root / "numpy"
            package_root.mkdir()
            (package_root / "__init__.py").write_text("__version__ = '1.26.0b1'\n")
            for index, module in enumerate(lock["native_modules"]):
                archive = root / module["archive"]
                archive.parent.mkdir(parents=True, exist_ok=True)
                archive.write_bytes(f"archive-{index}".encode())
            for index, library in enumerate(lock["support_libraries"]):
                archive = root / library["archive"]
                archive.parent.mkdir(parents=True, exist_ok=True)
                archive.write_bytes(f"support-{index}".encode())
            selection = self.tool.build_selection(lock, package_root, root)
            self.tool.validate_selection(selection, lock)
            self.assertEqual(selection["profile"], "numpy-core")
            self.assertEqual(selection["link_input_count"], 21)
            self.assertEqual([row["name"] for row in selection["support_libraries"]], ["npymath", "npyrandom"])
            self.assertEqual(selection["package"]["tree_sha256"], self.tool.package_tree_identity(package_root)["tree_sha256"])
            forged = json.loads(json.dumps(selection))
            forged["native_modules"][0]["archive_sha256"] = "0" * 64
            with self.assertRaises(ValueError):
                self.tool.validate_selection(forged, lock)

    def test_lock_rejects_unknown_fields_and_module_drift(self):
        value = json.loads(LOCK_PATH.read_text())
        value["surprise"] = True
        with self.assertRaises(ValueError):
            self.tool.validate_lock(value)
        value = json.loads(LOCK_PATH.read_text())
        value["native_modules"][0]["name"] = "numpy.evil"
        with self.assertRaises(ValueError):
            self.tool.validate_lock(value)


if __name__ == "__main__":
    unittest.main()
