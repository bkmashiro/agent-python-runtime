import importlib.util
import json
import pathlib
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
TOOL_PATH = ROOT / "guest" / "build" / "import_inventory.py"


def load_tool():
    spec = importlib.util.spec_from_file_location("import_inventory", TOOL_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load import inventory tool")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ImportInventoryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tool = load_tool()

    def test_request_is_bounded_and_profile_specific(self):
        request = self.tool.build_request("base")
        self.assertEqual("artifact-import-inventory", request["run_id"])
        self.assertEqual({"artifact_profile": "base"}, request["inputs"])
        self.assertNotIn("compatibility", request)
        self.assertIn("guest-importlib-find-spec-v1", request["code"])

        attrs_request = self.tool.build_request("attrs-770")
        self.assertEqual({"artifact_profile": "attrs-770"}, attrs_request["inputs"])

    def test_extract_validates_and_canonicalizes_guest_result(self):
        response = {
            "status": "ok",
            "result": {
                "schema_version": 1,
                "artifact_profile": "base",
                "probe": "guest-importlib-find-spec-v1",
                "implementation": "cpython",
                "python_version": "3.14.0 (main)",
                "discoverable_roots": ["agent_runtime", "json", "sys"],
                "failures": [{"name": "tkinter", "error": "ModuleNotFoundError"}],
            },
        }
        inventory = self.tool.extract_inventory(response, "base")
        self.assertEqual(response["result"], inventory)

        response["result"]["discoverable_roots"] = ["sys", "json"]
        with self.assertRaisesRegex(ValueError, "sorted unique"):
            self.tool.extract_inventory(response, "base")

    def test_write_and_read_inventory_are_strict(self):
        inventory = {
            "schema_version": 1,
            "artifact_profile": "base",
            "probe": "guest-importlib-find-spec-v1",
            "implementation": "cpython",
            "python_version": "3.14.0",
            "discoverable_roots": ["agent_runtime", "json", "sys"],
            "failures": [],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "import-inventory.json"
            self.tool.write_inventory(path, inventory)
            self.assertEqual(inventory, self.tool.load_inventory(path, "base"))
            path.write_text('{"schema_version":1,"schema_version":1}')
            with self.assertRaisesRegex(ValueError, "duplicate"):
                self.tool.load_inventory(path, "base")

    def test_attrs_profile_requires_packaged_root(self):
        value = {
            "schema_version": 1,
            "artifact_profile": "attrs-770",
            "probe": "guest-importlib-find-spec-v1",
            "implementation": "cpython",
            "python_version": "3.14.0",
            "discoverable_roots": ["agent_runtime", "attr", "json", "sys", "types", "typing"],
            "failures": [],
        }
        self.tool.validate_inventory(value, "attrs-770")
        value["discoverable_roots"].remove("attr")
        with self.assertRaisesRegex(ValueError, "required profile import root"):
            self.tool.validate_inventory(value, "attrs-770")


if __name__ == "__main__":
    unittest.main()
